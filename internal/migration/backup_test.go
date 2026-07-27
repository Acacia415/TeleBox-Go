package migration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gotd/td/session"
)

func TestInspectAndConvertBackup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "backup.tar.gz")
	writeTestBackup(t, archivePath)

	inventory, err := InspectBackup(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.PluginCount != 2 || !reflect.DeepEqual(inventory.Plugins, []string{"bin", "ip"}) {
		t.Fatalf("plugins = %v", inventory.Plugins)
	}
	if inventory.SessionFormat != StringSessionGramJS || inventory.SessionDC != 2 {
		t.Fatalf("session inventory = format:%s dc:%d", inventory.SessionFormat, inventory.SessionDC)
	}

	configPath := filepath.Join(dir, "output", "config.json")
	sessionPath := filepath.Join(dir, "output", "data", "session.json")
	assetsPath := filepath.Join(dir, "output", "data", "assets")
	result, err := ConvertBackupWithOptions(context.Background(), archivePath, ConvertOptions{
		ConfigPath:  configPath,
		SessionPath: sessionPath,
		AssetsPath:  assetsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Assets.Files != 1 || result.Assets.Bytes != 4 {
		t.Fatalf("asset extraction = %+v", result.Assets)
	}
	if data, err := os.ReadFile(filepath.Join(assetsPath, "ip", "data.db")); err != nil || string(data) != "test" {
		t.Fatalf("migrated asset = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(assetsPath, "_migration.json")); err != nil {
		t.Fatalf("asset manifest: %v", err)
	}
	loader := session.Loader{Storage: &session.FileStorage{Path: sessionPath}}
	data, err := loader.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if data.DC != 2 || len(data.AuthKey) != 256 {
		t.Fatalf("converted session = DC:%d auth-key:%d", data.DC, len(data.AuthKey))
	}

	if _, err := ConvertBackup(context.Background(), archivePath, configPath, sessionPath); err == nil {
		t.Fatal("ConvertBackup() overwrote existing outputs")
	}
}

func TestSafeArchivePathRejectsTraversal(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"../secret", "/absolute", `..\secret`} {
		if _, err := safeArchivePath(value); err == nil {
			t.Fatalf("safeArchivePath(%q) accepted unsafe path", value)
		}
	}
}

func TestAssetSelectorsOnlyIncludeRequestedPlugins(t *testing.T) {
	t.Parallel()

	selectors := assetSelectors([]string{"ai", "bulk_delete", "yt-dlp"})
	for _, expected := range []string{
		"ai/",
		"ai_config.db",
		"bd/",
		"bulk_delete/",
		"yt-dlp/",
		"ytdlp/",
		"ytdlp_gemini_config.db",
	} {
		if !matchesAssetSelector(expected, selectors) {
			t.Fatalf("selectors %v do not match %q", selectors, expected)
		}
	}
	if matchesAssetSelector("trace/db.json", selectors) {
		t.Fatalf("selectors %v unexpectedly include trace assets", selectors)
	}
}

func writeTestBackup(t *testing.T, target string) {
	t.Helper()
	file, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)

	key := bytes.Repeat([]byte{0x33}, 256)
	address := "149.154.167.50"
	payload := []byte{2, 0, byte(len(address))}
	payload = append(payload, address...)
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, 443)
	payload = append(payload, port...)
	payload = append(payload, key...)
	legacy := LegacyConfig{
		APIID:   123,
		APIHash: "hash",
		Session: "1" + base64.StdEncoding.EncodeToString(payload),
	}
	configData, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"telebox/config.json":       configData,
		"telebox/plugins/ip.ts":     []byte("export {}"),
		"telebox/plugins/bin.ts":    []byte("export {}"),
		"telebox/assets/ip/data.db": []byte("test"),
	}
	for name, data := range files {
		if err := archive.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(data)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
