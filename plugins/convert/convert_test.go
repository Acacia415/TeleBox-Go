package convert

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

func TestParseSongInfo(t *testing.T) {
	got, err := parseSongInfo(`{"title":"稻香","artist":"周杰伦","album":"魔杰座"}`, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "稻香" || got.Artist != "周杰伦" || got.Album != "魔杰座" {
		t.Fatalf("song info = %+v", got)
	}
}

func TestParseSongInfoLegacyText(t *testing.T) {
	got, err := parseSongInfo("歌曲名：晴天\n歌手：周杰伦\n专辑：叶惠美", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "晴天" || got.Artist != "周杰伦" || got.Album != "叶惠美" {
		t.Fatalf("song info = %+v", got)
	}
}

func TestSafeFilename(t *testing.T) {
	if got := safeFilename(` 周杰伦: "稻香"?.mp3 `); got != "周杰伦 稻香.mp3" {
		t.Fatalf("safe filename = %q", got)
	}
	if got := safeExtension("video.MP4"); got != ".mp4" {
		t.Fatalf("safe extension = %q", got)
	}
}

func TestMigrateLegacyConvertConfigFromPreservedAssets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	legacyRoot := filepath.Join(root, "legacy-assets")
	databasePath := filepath.Join(legacyRoot, "convert", "gemini_config.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE config (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO config(key, value)
		VALUES ('convert_gemini_api_key', 'legacy-convert-test-key');
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
	if err := p.migrateLegacyConfig(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "convert", "api_key")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "legacy-convert-test-key" {
		t.Fatalf("migrated convert key = %q", got)
	}
}
