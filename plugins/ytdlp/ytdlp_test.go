package ytdlp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseManual(t *testing.T) {
	title, artist, ok := parseManual("晴天 - 周杰伦")
	if !ok || title != "晴天" || artist != "周杰伦" {
		t.Fatalf("manual metadata = %q, %q, %v", title, artist, ok)
	}
	if _, _, ok := parseManual("AC-DC Thunderstruck"); ok {
		t.Fatal("unspaced hyphen was treated as manual metadata")
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
