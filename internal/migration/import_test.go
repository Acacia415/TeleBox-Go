package migration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/config"
)

func TestImportConvertedCopiesMissingFilesAndPreservesExistingData(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "converted")
	archive := filepath.Join(root, "backup.tar.gz")
	writeTestBackup(t, archive)
	_, err := ConvertBackupWithOptions(
		context.Background(),
		archive,
		ConvertOptions{
			ConfigPath:       filepath.Join(source, "config.json"),
			SessionPath:      filepath.Join(source, "data", "session.json"),
			AssetsPath:       filepath.Join(source, "data", "assets"),
			LegacyAssetsPath: filepath.Join(source, "data", "legacy-assets"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "installed")
	existingAsset := filepath.Join(target, "assets", "ip", "data.db")
	if err := os.MkdirAll(filepath.Dir(existingAsset), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existingAsset, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := ImportOptions{
		SourceRoot:       source,
		ConfigPath:       filepath.Join(target, "config", "config.json"),
		SessionPath:      filepath.Join(target, "data", "session.json"),
		AssetsPath:       filepath.Join(target, "assets"),
		LegacyAssetsPath: filepath.Join(target, "legacy-assets"),
	}
	result, err := ImportConverted(options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.CopiedFiles != 1 || result.Session.CopiedFiles != 1 {
		t.Fatalf("config/session result = %+v", result)
	}
	if result.Assets.CopiedFiles != 1 || result.Assets.SkippedFiles != 1 {
		t.Fatalf("asset result = %+v", result.Assets)
	}
	if result.LegacyAssets.CopiedFiles != 3 ||
		result.LegacyAssets.SkippedFiles != 0 {
		t.Fatalf("legacy asset result = %+v", result.LegacyAssets)
	}
	configData, err := os.ReadFile(options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var importedConfig config.Config
	if err := json.Unmarshal(configData, &importedConfig); err != nil {
		t.Fatal(err)
	}
	configRoot := filepath.Dir(options.ConfigPath)
	dataRoot := filepath.Dir(options.SessionPath)
	for name, gotWant := range map[string][2]string{
		"session": {
			importedConfig.Telegram.SessionFile,
			relativeOrAbsolute(configRoot, options.SessionPath),
		},
		"storage": {
			importedConfig.Storage.Path,
			relativeOrAbsolute(configRoot, filepath.Join(dataRoot, "telebox.db")),
		},
		"assets": {
			importedConfig.Storage.AssetsPath,
			relativeOrAbsolute(configRoot, options.AssetsPath),
		},
		"legacy assets": {
			importedConfig.Storage.LegacyAssetsPath,
			relativeOrAbsolute(configRoot, options.LegacyAssetsPath),
		},
		"plugins": {
			importedConfig.Plugins.Directory,
			relativeOrAbsolute(configRoot, filepath.Join(dataRoot, "plugins")),
		},
		"logs": {
			importedConfig.Logging.Path,
			relativeOrAbsolute(
				configRoot,
				filepath.Join(dataRoot, "logs", "telebox.log"),
			),
		},
	} {
		if gotWant[0] != gotWant[1] {
			t.Fatalf("imported %s path = %q, want %q", name, gotWant[0], gotWant[1])
		}
	}
	assertImportedFile(t, existingAsset, "current")
	assertImportedFile(
		t,
		filepath.Join(target, "legacy-assets", "unsupported", "cache.db"),
		"do not migrate",
	)
	receipts, err := filepath.Glob(
		filepath.Join(target, "legacy-assets", "_imports", "*.json"),
	)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("import receipts = %#v, %v", receipts, err)
	}

	repeated, err := ImportConverted(options)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Config.SkippedFiles != 1 ||
		repeated.Session.SkippedFiles != 1 ||
		repeated.Assets.SkippedFiles != 2 ||
		repeated.LegacyAssets.SkippedFiles != 3 {
		t.Fatalf("repeated import = %+v", repeated)
	}
}

func TestImportConvertedCanSkipMigratedSession(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := createConvertedTestRoot(t, root)
	target := filepath.Join(root, "installed")
	result, err := ImportConverted(ImportOptions{
		SourceRoot:       source,
		ConfigPath:       filepath.Join(target, "config.json"),
		SessionPath:      filepath.Join(target, "session.json"),
		AssetsPath:       filepath.Join(target, "assets"),
		LegacyAssetsPath: filepath.Join(target, "legacy-assets"),
		SkipSession:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Session != (ImportStats{}) {
		t.Fatalf("session result = %+v", result.Session)
	}
	if _, err := os.Stat(filepath.Join(target, "session.json")); !os.IsNotExist(err) {
		t.Fatalf("skipped session exists: %v", err)
	}
}

func TestImportConvertedRejectsTamperedLegacyManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := createConvertedTestRoot(t, root)
	manifest := filepath.Join(
		source,
		"data",
		"legacy-assets",
		legacyAssetManifestName,
	)
	if err := os.WriteFile(manifest, []byte(`{"format":"invalid"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ImportConverted(ImportOptions{
		SourceRoot:       source,
		ConfigPath:       filepath.Join(root, "installed", "config.json"),
		SessionPath:      filepath.Join(root, "installed", "session.json"),
		AssetsPath:       filepath.Join(root, "installed", "assets"),
		LegacyAssetsPath: filepath.Join(root, "installed", "legacy-assets"),
	})
	if err == nil {
		t.Fatal("ImportConverted() error = nil")
	}
}

func TestImportConvertedRejectsDestinationInsideSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := createConvertedTestRoot(t, root)
	_, err := ImportConverted(ImportOptions{
		SourceRoot:       source,
		ConfigPath:       filepath.Join(source, "installed", "config.json"),
		SessionPath:      filepath.Join(root, "session.json"),
		AssetsPath:       filepath.Join(root, "assets"),
		LegacyAssetsPath: filepath.Join(root, "legacy-assets"),
	})
	if err == nil {
		t.Fatal("ImportConverted() error = nil")
	}
}

func TestImportConvertedRejectsOverlappingDestinations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := createConvertedTestRoot(t, root)
	target := filepath.Join(root, "installed")
	_, err := ImportConverted(ImportOptions{
		SourceRoot:       source,
		ConfigPath:       filepath.Join(target, "assets", "config.json"),
		SessionPath:      filepath.Join(target, "session.json"),
		AssetsPath:       filepath.Join(target, "assets"),
		LegacyAssetsPath: filepath.Join(target, "legacy-assets"),
	})
	if err == nil {
		t.Fatal("ImportConverted() error = nil")
	}
}

func createConvertedTestRoot(t *testing.T, root string) string {
	t.Helper()
	source := filepath.Join(root, "converted")
	archive := filepath.Join(root, "backup.tar.gz")
	writeTestBackup(t, archive)
	if _, err := ConvertBackupWithOptions(
		context.Background(),
		archive,
		ConvertOptions{
			ConfigPath:       filepath.Join(source, "config.json"),
			SessionPath:      filepath.Join(source, "data", "session.json"),
			AssetsPath:       filepath.Join(source, "data", "assets"),
			LegacyAssetsPath: filepath.Join(source, "data", "legacy-assets"),
		},
	); err != nil {
		t.Fatal(err)
	}
	return source
}

func assertImportedFile(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != expected {
		t.Fatalf("%s = %q, want %q", path, data, expected)
	}
}
