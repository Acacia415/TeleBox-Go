package pluginmarket

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

func TestInstallAndRemove(t *testing.T) {
	t.Parallel()

	archive := testPluginArchive(t, map[string]string{
		"plugin.json": `{
			"schema_version": 1,
			"api_version": 1,
			"name": "example",
			"version": "v1.0.0",
			"description": "Example plugin",
			"executable": "telebox-plugin-example",
			"commands": [{"name": "example"}]
		}`,
		"telebox-plugin-example": "binary",
	})
	sum := sha256.Sum256(archive)
	var catalog []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/catalog.json":
			_, _ = writer.Write(catalog)
		case "/example.tar.gz":
			_, _ = writer.Write(archive)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	catalogValue := pluginapi.Catalog{
		SchemaVersion: pluginapi.CatalogSchemaVersion,
		Plugins: []pluginapi.CatalogPlugin{{
			Name:        "example",
			Description: "Example plugin",
			Releases: []pluginapi.PluginRelease{{
				Version: "v1.0.0",
				Artifacts: []pluginapi.Artifact{{
					OS:     "linux",
					Arch:   "arm64",
					URL:    server.URL + "/example.tar.gz",
					SHA256: hex.EncodeToString(sum[:]),
					Size:   int64(len(archive)),
					Format: "tar.gz",
				}},
			}},
		}},
	}
	var err error
	catalog, err = json.Marshal(catalogValue)
	if err != nil {
		t.Fatal(err)
	}

	directory := filepath.Join(t.TempDir(), "plugins")
	manager, err := New(Config{
		Directory:       directory,
		CatalogURL:      server.URL + "/catalog.json",
		MaxArchiveBytes: 1 << 20,
		Client:          server.Client(),
		GOOS:            "linux",
		GOARCH:          "arm64",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Install(context.Background(), "example", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Installed.Manifest.Version != "v1.0.0" {
		t.Fatalf("installed = %+v", result.Installed)
	}
	if _, err := os.Stat(result.Installed.Executable); err != nil {
		t.Fatal(err)
	}
	removed, err := manager.Remove("example")
	if err != nil {
		t.Fatal(err)
	}
	if removed.Manifest.Name != "example" {
		t.Fatalf("removed = %+v", removed)
	}
	if _, err := os.Stat(filepath.Join(directory, "example")); !os.IsNotExist(err) {
		t.Fatalf("removed directory error = %v", err)
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	t.Parallel()

	archive := testPluginArchive(t, map[string]string{
		`..\outside`: "unsafe",
	})
	err := extractArchive(
		bytes.NewReader(archive),
		int64(len(archive)),
		"tar.gz",
		t.TempDir(),
		1<<20,
	)
	if err == nil {
		t.Fatal("traversal archive was accepted")
	}
}

func TestInstalledKeepsValidPluginsWhenOneIsCorrupt(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	valid := filepath.Join(directory, "valid")
	if err := os.Mkdir(valid, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"schema_version": 1,
		"api_version": 1,
		"name": "valid",
		"version": "1.0.0",
		"description": "Valid plugin",
		"executable": "plugin",
		"commands": [{"name": "valid"}]
	}`
	if err := os.WriteFile(
		filepath.Join(valid, "plugin.json"),
		[]byte(manifest),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(valid, "plugin"),
		[]byte("binary"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "broken"), 0o700); err != nil {
		t.Fatal(err)
	}

	manager, err := New(Config{
		Directory:       directory,
		CatalogURL:      "https://example.com/catalog.json",
		MaxArchiveBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := manager.Installed()
	if err == nil {
		t.Fatal("Installed() error = nil, want corrupt plugin warning")
	}
	if len(installed) != 1 || installed[0].Manifest.Name != "valid" {
		t.Fatalf("Installed() = %+v", installed)
	}
}

func TestInstallAndExportLocalArchive(t *testing.T) {
	t.Parallel()

	archive := testPluginArchive(t, map[string]string{
		"plugin.json": `{
			"schema_version": 1,
			"api_version": 1,
			"name": "local",
			"version": "1.2.3",
			"description": "Local plugin",
			"executable": "telebox-plugin-local",
			"commands": [{"name": "local"}]
		}`,
		"telebox-plugin-local": "binary",
	})
	root := t.TempDir()
	manager, err := New(Config{
		Directory:       filepath.Join(root, "plugins"),
		CatalogURL:      "https://example.com/catalog.json",
		MaxArchiveBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := manager.InspectArchive(archive, "tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "local" {
		t.Fatalf("manifest = %#v", manifest)
	}
	result, err := manager.InstallArchive(
		context.Background(),
		archive,
		"tar.gz",
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Installed.Manifest.Version != "1.2.3" {
		t.Fatalf("result = %#v", result)
	}
	exported := filepath.Join(root, "local.zip")
	if _, err := manager.Export("local", exported); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exported)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(Config{
		Directory:       filepath.Join(root, "plugins-second"),
		CatalogURL:      "https://example.com/catalog.json",
		MaxArchiveBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	reinstalled, err := second.InstallArchive(
		context.Background(),
		data,
		"zip",
	)
	if err != nil {
		t.Fatal(err)
	}
	if reinstalled.Installed.Manifest.Name != "local" {
		t.Fatalf("reinstalled = %#v", reinstalled)
	}
}

func testPluginArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for name, content := range files {
		if err := archive.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o700,
			Size: int64(len(content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
