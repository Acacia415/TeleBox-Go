package corebackup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fakeStorage struct {
	content string
}

func (f fakeStorage) Backup(_ context.Context, destination string) error {
	return os.WriteFile(destination, []byte(f.content), 0o600)
}

func TestCreateValidateStageAndApply(t *testing.T) {
	root := t.TempDir()
	paths := Paths{
		Config:       filepath.Join(root, "config.json"),
		Storage:      filepath.Join(root, "data", "telebox.db"),
		Assets:       filepath.Join(root, "data", "assets"),
		LegacyAssets: filepath.Join(root, "data", "legacy-assets"),
		Plugins:      filepath.Join(root, "data", "plugins"),
	}
	writeTestFile(t, paths.Config, "backup-config")
	writeTestFile(t, filepath.Join(paths.Assets, "alias", "alias.db"), "backup-alias")
	writeTestFile(t, filepath.Join(paths.LegacyAssets, "old", "cache.db"), "backup-legacy")
	writeTestFile(t, filepath.Join(paths.Plugins, "ids", "plugin.json"), "backup-plugin")
	writeTestFile(t, paths.Storage, "current-storage")

	archive := filepath.Join(root, "backup.tar.gz")
	manifest, err := Create(
		context.Background(),
		fakeStorage{content: "backup-storage"},
		paths,
		true,
		archive,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Full || len(manifest.Files) != 5 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if _, err := Validate(archive); err != nil {
		t.Fatal(err)
	}

	writeTestFile(t, paths.Config, "new-config")
	writeTestFile(t, paths.Storage, "new-storage")
	writeTestFile(t, filepath.Join(paths.Assets, "alias", "alias.db"), "new-alias")
	writeTestFile(t, filepath.Join(paths.LegacyAssets, "old", "cache.db"), "new-legacy")
	if _, _, err := Stage(archive, paths); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyPending(paths)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || !result.Full || result.RollbackDir == "" {
		t.Fatalf("unexpected apply result: %#v", result)
	}
	assertTestFile(t, paths.Config, "backup-config")
	assertTestFile(t, paths.Storage, "backup-storage")
	assertTestFile(t, filepath.Join(paths.Assets, "alias", "alias.db"), "backup-alias")
	assertTestFile(t, filepath.Join(paths.LegacyAssets, "old", "cache.db"), "backup-legacy")
	assertTestFile(t, filepath.Join(result.RollbackDir, "config.json"), "new-config")
	assertTestFile(t, filepath.Join(result.RollbackDir, "telebox.db"), "new-storage")
	assertTestFile(t, filepath.Join(result.RollbackDir, "legacy-assets", "old", "cache.db"), "new-legacy")
}

func TestValidateRejectsPathTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "unsafe.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	data := []byte("unsafe")
	if err := tarWriter.WriteHeader(&tar.Header{
		Name: "../../outside",
		Mode: 0o600,
		Size: int64(len(data)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(archive); err == nil {
		t.Fatal("expected unsafe archive to be rejected")
	}
}

func writeTestFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertTestFile(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != expected {
		t.Fatalf("%s = %q, want %q", path, data, expected)
	}
}
