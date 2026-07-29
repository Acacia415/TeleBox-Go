package speedlink

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/service"
)

func TestParseConnection(t *testing.T) {
	for _, test := range []struct {
		input string
		host  string
		port  int
	}{
		{"root@example.com:22", "example.com", 22},
		{"root@[2001:db8::1]:2222", "2001:db8::1", 2222},
	} {
		user, host, port, err := parseConnection(test.input)
		if err != nil {
			t.Fatal(err)
		}
		if user != "root" || host != test.host || port != test.port {
			t.Fatalf("connection = %q, %q, %d", user, host, port)
		}
	}
}

func TestCredentialRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	encrypted, err := encryptCredential(key, "very secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "very secret") {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := decryptCredential(key, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if got != "very secret" {
		t.Fatalf("decrypted = %q", got)
	}
}

func TestParseSpeedResult(t *testing.T) {
	got, err := parseSpeedResult(`notice
{"isp":"Example","server":{"id":1,"name":"Node","location":"Tokyo"},"interface":{"externalIp":"1.2.3.4","name":"eth0"},"ping":{"latency":10,"jitter":1},"download":{"bandwidth":125000},"upload":{"bandwidth":62500},"timestamp":"2026-07-26T00:00:00Z"}`)
	if err != nil {
		t.Fatal(err)
	}
	text := formatSpeedResult("Tokyo", got)
	for _, fragment := range []string{"Tokyo", "1.00 Mbps", "500.00 Kbps"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("result missing %q:\n%s", fragment, text)
		}
	}
}

func TestRedactHost(t *testing.T) {
	if got := redactHost("1.2.3.4"); strings.Contains(got, "1.2.3.4") {
		t.Fatalf("IP leaked: %q", got)
	}
	if got := redactHost("node.example.com"); got != "***.example.com" {
		t.Fatalf("hostname redaction = %q", got)
	}
}

func TestExtractSpeedtestArchive(t *testing.T) {
	var document bytes.Buffer
	compressed := gzip.NewWriter(&document)
	archive := tar.NewWriter(compressed)
	content := []byte("#!/bin/sh\nexit 0\n")
	if err := archive.WriteHeader(&tar.Header{
		Name: "speedtest",
		Mode: 0o755,
		Size: int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "speedtest")
	if err := extractSpeedtestArchive(document.Bytes(), target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("extracted = %q", got)
	}
}

func TestSpeedtestEnvironmentWithoutSystemdHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	assetDir := t.TempDir()
	p := &Plugin{assetDir: assetDir}

	got := strings.Join(p.speedtestEnvironment(), "\n")
	for _, want := range []string{
		"HOME=" + assetDir,
		"XDG_CONFIG_HOME=" + filepath.Join(assetDir, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(assetDir, ".local", "share"),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("environment does not contain %q:\n%s", want, got)
		}
	}
}

func TestLegacySpeedlinkAssetDir(t *testing.T) {
	root := t.TempDir()
	got := legacySpeedlinkAssetDir(root)
	want := filepath.Join(root, "speedlink")
	if got != want {
		t.Fatalf("legacy speedlink directory = %q, want %q", got, want)
	}
}

func TestUniquePathsOmitsEmptyAndDuplicatePaths(t *testing.T) {
	root := t.TempDir()
	got := uniquePaths("", root, filepath.Join(root, "."))
	if len(got) != 1 || filepath.Clean(got[0]) != filepath.Clean(root) {
		t.Fatalf("unique paths = %#v", got)
	}
}

func TestMigrateLegacyFallsBackToPreservedAssets(t *testing.T) {
	root := t.TempDir()
	legacyRoot := filepath.Join(root, "legacy-assets")
	legacyDir := filepath.Join(legacyRoot, "speedlink")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(legacyDir, "secret.key"),
		bytes.Repeat([]byte{0x42}, 32),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(legacyDir, "servers.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			host TEXT NOT NULL,
			port INTEGER NOT NULL,
			username TEXT NOT NULL,
			auth_method TEXT NOT NULL,
			credentials TEXT NOT NULL
		);
		INSERT INTO servers(name, host, port, username, auth_method, credentials)
		VALUES ('测试节点', 'example.com', 22, 'root', 'key', '/tmp/id_ed25519');
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	p := New(service.Container{
		AssetsDir:       filepath.Join(root, "assets"),
		LegacyAssetsDir: legacyRoot,
	})
	p.masterKey = bytes.Repeat([]byte{0x24}, 32)
	servers, err := p.migrateLegacy()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "测试节点" ||
		servers[0].Host != "example.com" {
		t.Fatalf("migrated servers = %#v", servers)
	}
}
