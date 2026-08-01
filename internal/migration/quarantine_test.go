package migration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestQuarantineUnsafeActiveAssets(t *testing.T) {
	root := t.TempDir()
	assets := filepath.Join(root, "assets")
	legacy := filepath.Join(root, "legacy")
	for _, directory := range []string{
		filepath.Join(assets, "speedlink"),
		filepath.Join(assets, "plugin-runtime", "speedlink"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldTool := filepath.Join(assets, "speedlink", "speedtest")
	if err := os.WriteFile(oldTool, []byte("\x7fELFold"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(assets, "speedlink", "servers.db")
	if err := os.WriteFile(dataPath, []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(assets, "plugin-runtime", "speedlink", "plugin")
	if err := os.WriteFile(runtimePath, []byte("\x7fELFplugin"), 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := QuarantineUnsafeActiveAssets(assets, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if result.QuarantinedFiles != 1 {
		t.Fatalf("quarantine result = %+v", result)
	}
	if _, err := os.Stat(oldTool); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old tool remains active: %v", err)
	}
	if _, err := os.Stat(filepath.Join(
		legacy, "_quarantine", "active-assets", "speedlink", "speedtest",
	)); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{dataPath, runtimePath} {
		if _, err := os.Stat(expected); err != nil {
			t.Fatalf("safe path %q was removed: %v", expected, err)
		}
	}
}
