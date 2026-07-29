package cezi

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/service"
	appstorage "github.com/Acacia415/TeleBox-Go/internal/storage"
)

func TestFirstChinese(t *testing.T) {
	if got := firstChinese("abc缘分"); got != "缘" {
		t.Fatalf("firstChinese = %q", got)
	}
}

func TestRandomMeaningfulChinese(t *testing.T) {
	got, err := randomMeaningfulChinese("你的缘")
	if err != nil {
		t.Fatal(err)
	}
	if got != "缘" {
		t.Fatalf("random character = %q", got)
	}
	if _, err := randomMeaningfulChinese("你我的"); err == nil {
		t.Fatal("excluded-only message succeeded")
	}
}

func TestCleanText(t *testing.T) {
	if got := cleanText("\uFEFF【起卦】\x00妙哉\n"); got != "【起卦】妙哉" {
		t.Fatalf("cleanText = %q", got)
	}
}

func TestLegacyCeziConfigPathsIncludesPreservedAssets(t *testing.T) {
	root := t.TempDir()
	got := legacyCeziConfigPaths(service.Container{
		AssetsDir:       filepath.Join(root, "assets"),
		LegacyAssetsDir: filepath.Join(root, "legacy-assets"),
	})
	want := []string{
		filepath.Join(root, "assets", "cezi_config.db"),
		filepath.Join(root, "legacy-assets", "cezi_config.db"),
	}
	if len(got) != len(want) {
		t.Fatalf("legacy paths = %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("legacy path %d = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestMigrateLegacyCeziConfigFromPreservedAssets(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	legacyRoot := filepath.Join(root, "legacy-assets")
	if err := os.MkdirAll(legacyRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(legacyRoot, "cezi_config.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		INSERT INTO config(key, value) VALUES ('cezi_api_key', 'legacy-test-key');
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := appstorage.Open(ctx, filepath.Join(root, "telebox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p := New(service.Container{
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Storage:         store,
		LegacyAssetsDir: legacyRoot,
	})
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "cezi", apiKeyName)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "legacy-test-key" {
		t.Fatalf("migrated API key = %q", got)
	}
}
