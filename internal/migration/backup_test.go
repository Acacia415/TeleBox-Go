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
	if inventory.PluginCount != 2 ||
		!reflect.DeepEqual(inventory.Plugins, []string{"bin", "ip"}) {
		t.Fatalf("plugins = %v", inventory.Plugins)
	}
	if inventory.SessionFormat != StringSessionGramJS || inventory.SessionDC != 2 {
		t.Fatalf("session inventory = format:%s dc:%d", inventory.SessionFormat, inventory.SessionDC)
	}
	if inventory.PreservedAssetFiles != 2 || inventory.PreservedAssetBytes != 18 {
		t.Fatalf("preserved asset inventory = %d files/%d bytes",
			inventory.PreservedAssetFiles,
			inventory.PreservedAssetBytes,
		)
	}

	configPath := filepath.Join(dir, "output", "config.json")
	sessionPath := filepath.Join(dir, "output", "data", "session.json")
	assetsPath := filepath.Join(dir, "output", "data", "assets")
	legacyAssetsPath := filepath.Join(dir, "output", "data", "legacy-assets")
	result, err := ConvertBackupWithOptions(context.Background(), archivePath, ConvertOptions{
		ConfigPath:       configPath,
		SessionPath:      sessionPath,
		AssetsPath:       assetsPath,
		LegacyAssetsPath: legacyAssetsPath,
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
	if _, err := os.Stat(filepath.Join(assetsPath, "unsupported", "cache.db")); !os.IsNotExist(err) {
		t.Fatalf("unsupported plugin asset was migrated: %v", err)
	}
	if result.LegacyAssets.Files != 2 || result.LegacyAssets.Bytes != 18 {
		t.Fatalf("legacy asset preservation = %+v", result.LegacyAssets)
	}
	for relative, expected := range map[string]string{
		filepath.Join("ip", "data.db"):           "test",
		filepath.Join("unsupported", "cache.db"): "do not migrate",
	} {
		data, err := os.ReadFile(filepath.Join(legacyAssetsPath, relative))
		if err != nil || string(data) != expected {
			t.Fatalf("preserved legacy asset %q = %q, %v", relative, data, err)
		}
		info, err := os.Stat(filepath.Join(legacyAssetsPath, relative))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 != 0 {
			t.Fatalf("preserved legacy asset %q is executable: %v", relative, info.Mode())
		}
	}
	manifestData, err := os.ReadFile(filepath.Join(legacyAssetsPath, legacyAssetManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest preservedAssetManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SourceSHA256 != inventory.SHA256 ||
		manifest.ExtractedFile != 2 ||
		len(manifest.Files) != 2 {
		t.Fatalf("legacy asset manifest = %#v", manifest)
	}
	for _, file := range manifest.Files {
		if len(file.SHA256) != 64 {
			t.Fatalf("legacy asset checksum for %q = %q", file.Path, file.SHA256)
		}
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var converted struct {
		Plugins struct {
			Enabled []string `json:"enabled"`
		} `json:"plugins"`
		Storage struct {
			LegacyAssetsPath string `json:"legacy_assets_path"`
		} `json:"storage"`
	}
	if err := json.Unmarshal(configData, &converted); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(converted.Plugins.Enabled, []string{"bin", "ip"}) {
		t.Fatalf("enabled plugins = %v", converted.Plugins.Enabled)
	}
	if converted.Storage.LegacyAssetsPath != filepath.Join("data", "legacy-assets") {
		t.Fatalf("legacy assets config path = %q", converted.Storage.LegacyAssetsPath)
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

	for _, value := range []string{
		"../secret",
		"/absolute",
		`..\secret`,
		"telebox/assets/../secret",
	} {
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

func TestConvertRejectsOverlappingAssetPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	assets := filepath.Join(root, "data", "assets")
	_, err := ConvertBackupWithOptions(context.Background(), "unused.tar.gz", ConvertOptions{
		ConfigPath:       filepath.Join(root, "config.json"),
		SessionPath:      filepath.Join(root, "data", "session.json"),
		AssetsPath:       assets,
		LegacyAssetsPath: filepath.Join(assets, "legacy"),
	})
	if err == nil {
		t.Fatal("ConvertBackupWithOptions() accepted overlapping asset paths")
	}
}

func TestPreserveLegacyAssetsRejectsUnsafeEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entries []testArchiveEntry
	}{
		{
			name: "symbolic link",
			entries: []testArchiveEntry{{
				header: tar.Header{
					Name:     "telebox/assets/plugin/link",
					Typeflag: tar.TypeSymlink,
					Linkname: "../../secret",
				},
			}},
		},
		{
			name: "duplicate file",
			entries: []testArchiveEntry{
				{header: tar.Header{Name: "telebox/assets/plugin/data.db"}, data: []byte("one")},
				{header: tar.Header{Name: "telebox/assets/plugin/data.db"}, data: []byte("two")},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			archivePath := filepath.Join(root, "backup.tar.gz")
			writeAssetArchive(t, archivePath, test.entries)
			destination := filepath.Join(root, "legacy-assets")
			if _, err := PreserveLegacyAssets(archivePath, destination, "source"); err == nil {
				t.Fatal("PreserveLegacyAssets() error = nil, want rejection")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("partial legacy asset directory remains: %v", err)
			}
		})
	}
}

func TestPreserveLegacyAssetsDoesNotReplaceOriginalManifestNamedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	archivePath := filepath.Join(root, "backup.tar.gz")
	writeAssetArchive(t, archivePath, []testArchiveEntry{{
		header: tar.Header{Name: "telebox/assets/" + legacyAssetManifestName},
		data:   []byte("original plugin data"),
	}})
	destination := filepath.Join(root, "legacy-assets")
	result, err := PreserveLegacyAssets(archivePath, destination, "source")
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 {
		t.Fatalf("preserved files = %d, want 1", result.Files)
	}
	data, err := os.ReadFile(filepath.Join(destination, legacyAssetManifestName))
	if err != nil || string(data) != "original plugin data" {
		t.Fatalf("original manifest-named asset = %q, %v", data, err)
	}
	generated, err := os.ReadFile(filepath.Join(destination, "_legacy_manifest.1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest preservedAssetManifest
	if err := json.Unmarshal(generated, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Format != "telebox-go-legacy-assets" || len(manifest.Files) != 1 {
		t.Fatalf("generated legacy manifest = %#v", manifest)
	}
}

type testArchiveEntry struct {
	header tar.Header
	data   []byte
}

func writeAssetArchive(t *testing.T, target string, entries []testArchiveEntry) {
	t.Helper()
	file, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		header := entry.header
		if header.Typeflag == 0 {
			header.Typeflag = tar.TypeReg
		}
		header.Mode = 0o700
		header.Size = int64(len(entry.data))
		if err := archive.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if len(entry.data) > 0 {
			if _, err := archive.Write(entry.data); err != nil {
				t.Fatal(err)
			}
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
		"telebox/config.json":                 configData,
		"telebox/plugins/ip.ts":               []byte("export {}"),
		"telebox/plugins/bin.ts":              []byte("export {}"),
		"telebox/plugins/unsupported.ts":      []byte("export {}"),
		"telebox/assets/ip/data.db":           []byte("test"),
		"telebox/assets/unsupported/cache.db": []byte("do not migrate"),
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
