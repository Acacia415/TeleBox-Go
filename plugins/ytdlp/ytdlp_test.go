package ytdlp

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/service"
	appstorage "github.com/Acacia415/TeleBox-Go/internal/storage"
)

func TestParseManual(t *testing.T) {
	title, artist, ok := parseManual("晴天 - 周杰伦")
	if !ok || title != "晴天" || artist != "周杰伦" {
		t.Fatalf("manual metadata = %q, %q, %v", title, artist, ok)
	}
	title, artist, ok = parseManual("晴天-周杰伦")
	if !ok || title != "晴天" || artist != "周杰伦" {
		t.Fatalf("compact manual metadata = %q, %q, %v", title, artist, ok)
	}
	if _, _, ok := parseManual("AC-DC Thunderstruck"); ok {
		t.Fatal("unspaced hyphen was treated as manual metadata")
	}
}

func TestYTHelpIncludesReverseProxyAndLoginGuidance(t *testing.T) {
	got := ytHelpHTML("-")
	for _, want := range []string{
		"-yt baseurl",
		"generativelanguage.googleapis.com",
		"Cloudflare Workers",
		"-yt proxy",
		"-yt cookies",
		"Sign in to confirm you’re not a bot",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("help does not contain %q", want)
		}
	}
	if strings.Contains(got, "{{prefix}}") {
		t.Fatal("help contains an unresolved prefix placeholder")
	}
}

func TestParseVideoTitle(t *testing.T) {
	title, artist := parseVideoTitle(
		"周杰伦 - 稻香 (Official MV)",
		"周杰伦 Jay Chou",
	)
	if title != "稻香" || artist != "周杰伦" {
		t.Fatalf("video metadata = %q / %q", title, artist)
	}
}

func TestParseAIInfo(t *testing.T) {
	got, err := parseAIInfo(
		"```json\n{\"title\":\"稻香\",\"artist\":\"周杰伦\",\"album\":\"魔杰座\"}\n```",
		"fallback",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "稻香" || got.Artist != "周杰伦" || got.Album != "魔杰座" {
		t.Fatalf("AI metadata = %+v", got)
	}
}

func TestFindOutputPathRejectsOutsideJob(t *testing.T) {
	job := t.TempDir()
	outside := filepath.Join(filepath.Dir(job), "outside.mp3")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)
	if got := findOutputPath(job, outside); got != "" {
		t.Fatalf("outside output accepted: %q", got)
	}
}

func TestSafeFilename(t *testing.T) {
	if got := safeFilename(`a:/b*?"<>|`); got != "ab" {
		t.Fatalf("safe filename = %q", got)
	}
}

func TestMigrateLegacyYTConfigFromPreservedAssets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	legacyRoot := filepath.Join(root, "legacy-assets")
	if err := os.MkdirAll(legacyRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open(
		"sqlite",
		filepath.Join(legacyRoot, "ytdlp_gemini_config.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE config (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO config(key, value) VALUES
			('ytdlp_gemini_api_key', 'legacy-yt-test-key'),
			('ytdlp_gemini_base_url', 'https://proxy.example.test');
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
	for key, want := range map[string]string{
		"api_key":  "legacy-yt-test-key",
		"base_url": "https://proxy.example.test",
	} {
		got, err := store.Get(ctx, "yt-dlp", key)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("migrated YT %s = %q", key, got)
		}
	}
}
