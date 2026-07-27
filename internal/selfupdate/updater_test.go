package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckReportsAvailableRelease(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_ = json.NewEncoder(writer).Encode(releaseMetadata{TagName: "v1.2.0"})
	}))
	defer server.Close()

	updater := testUpdater(server, "v1.1.0", "")
	status, err := updater.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.CurrentVersion != "v1.1.0" ||
		status.LatestVersion != "v1.2.0" ||
		!status.UpdateAvailable {
		t.Fatalf("status = %#v", status)
	}
}

func TestUpdateVerifiesAndReplacesExecutable(t *testing.T) {
	t.Parallel()

	const (
		currentVersion = "v1.1.0"
		latestVersion  = "v1.2.0"
		assetName      = "telebox-go_v1.2.0_linux_amd64.tar.gz"
	)
	archive := testArchive(t, "telebox-go_v1.2.0_linux_amd64/telebox", []byte("new binary"))
	sum := sha256.Sum256(archive)
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n")

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/latest":
			_ = json.NewEncoder(writer).Encode(releaseMetadata{
				TagName: latestVersion,
				Assets: []releaseAsset{
					{Name: assetName, URL: server.URL + "/archive", Size: int64(len(archive))},
					{Name: "SHA256SUMS.txt", URL: server.URL + "/sums", Size: int64(len(checksums))},
				},
			})
		case "/archive":
			_, _ = writer.Write(archive)
		case "/sums":
			_, _ = writer.Write(checksums)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	directory := t.TempDir()
	executable := filepath.Join(directory, "telebox")
	if err := os.WriteFile(executable, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	updater := testUpdater(server, currentVersion, executable)
	updater.replace = func(staged, target string) error {
		data, err := os.ReadFile(staged)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o755); err != nil {
			return err
		}
		return os.Remove(staged)
	}

	result, err := updater.Update(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.CurrentVersion != currentVersion ||
		result.LatestVersion != latestVersion || result.AssetName != assetName {
		t.Fatalf("result = %#v", result)
	}
	if got := readTestFile(t, executable); got != "new binary" {
		t.Fatalf("executable = %q", got)
	}
	if got := readTestFile(t, executable+".previous"); got != "old binary" {
		t.Fatalf("backup = %q", got)
	}
}

func TestUpdateRejectsChecksumMismatchWithoutReplacing(t *testing.T) {
	t.Parallel()

	const assetName = "telebox-go_v1.2.0_linux_amd64.tar.gz"
	archive := testArchive(t, "package/telebox", []byte("new binary"))
	checksums := []byte(strings.Repeat("0", 64) + "  " + assetName + "\n")

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/latest":
			_ = json.NewEncoder(writer).Encode(releaseMetadata{
				TagName: "v1.2.0",
				Assets: []releaseAsset{
					{Name: assetName, URL: server.URL + "/archive", Size: int64(len(archive))},
					{Name: "SHA256SUMS.txt", URL: server.URL + "/sums", Size: int64(len(checksums))},
				},
			})
		case "/archive":
			_, _ = writer.Write(archive)
		case "/sums":
			_, _ = writer.Write(checksums)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	executable := filepath.Join(t.TempDir(), "telebox")
	if err := os.WriteFile(executable, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	updater := testUpdater(server, "v1.1.0", executable)
	_, err := updater.Update(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("Update() error = %v", err)
	}
	if got := readTestFile(t, executable); got != "old binary" {
		t.Fatalf("executable changed to %q", got)
	}
	if _, err := os.Stat(executable + ".previous"); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup after rejected update: %v", err)
	}
}

func TestUpdateRejectsUnsupportedPlatform(t *testing.T) {
	t.Parallel()

	updater := &Updater{goos: "windows", goarch: "amd64"}
	_, err := updater.Update(context.Background(), false)
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestChecksumFor(t *testing.T) {
	t.Parallel()

	want := strings.Repeat("a", 64)
	got, err := checksumFor([]byte(
		"invalid line\n"+want+" *telebox.tar.gz\n",
	), "telebox.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("checksum = %q", got)
	}
}

func TestValidateDownloadURL(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"https://github.com/Acacia415/TeleBox-Go/releases/download/v1/a",
		"https://release-assets.githubusercontent.com/file",
	} {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateDownloadURL(parsed, false); err != nil {
			t.Fatalf("validateDownloadURL(%q) error = %v", raw, err)
		}
	}
	parsed, err := url.Parse("https://example.com/update")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDownloadURL(parsed, false); err == nil {
		t.Fatal("untrusted update host was accepted")
	}
}

func testUpdater(server *httptest.Server, currentVersion, executable string) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		releaseAPIURL:  server.URL + "/latest",
		executablePath: executable,
		goos:           "linux",
		goarch:         "amd64",
		client:         server.Client(),
		allowHTTP:      true,
	}
}

func testArchive(t *testing.T, name string, content []byte) []byte {
	t.Helper()

	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{
		Name: name,
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
	return buffer.Bytes()
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
