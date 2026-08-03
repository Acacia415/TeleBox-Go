package pluginbridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestStageUploadPreservesMediaExtension(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "speedtest.png")
	if err := os.WriteFile(source, []byte("png data"), 0o600); err != nil {
		t.Fatal(err)
	}
	proxy := &telegramProxy{workDir: filepath.Join(root, "work")}
	staged, cleanup, err := proxy.stageUpload(source, "result.png")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if filepath.Ext(staged) != ".png" {
		t.Fatalf("staged extension = %q", filepath.Ext(staged))
	}
	data, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "png data" {
		t.Fatalf("staged data = %q", data)
	}
}

func TestSafeUploadExtensionRejectsUnsafeSuffix(t *testing.T) {
	for _, value := range []string{"photo.PNG", "song.mp3", "archive.tar.gz"} {
		if safeUploadExtension(value) == "" {
			t.Fatalf("safeUploadExtension(%q) rejected", value)
		}
	}
	for _, value := range []string{"photo", "file.bad-name", "file.abcdefghijkl"} {
		if got := safeUploadExtension(value); got != "" {
			t.Fatalf("safeUploadExtension(%q) = %q", value, got)
		}
	}
}

func TestStageTelegramUploadStagesMediaAndThumbnail(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "video.mp4")
	thumbnailPath := filepath.Join(root, "thumbnail.jpg")
	if err := os.WriteFile(mediaPath, []byte("video data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(thumbnailPath, []byte("thumbnail data"), 0o600); err != nil {
		t.Fatal(err)
	}
	proxy := &telegramProxy{workDir: filepath.Join(root, "work")}
	staged, cleanup, err := proxy.stageTelegramUpload(telegram.Upload{
		Path:          mediaPath,
		ThumbnailPath: thumbnailPath,
		FileName:      "result.mp4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if staged.Path == mediaPath || staged.ThumbnailPath == thumbnailPath {
		t.Fatalf("upload paths were not staged: %+v", staged)
	}
	if filepath.Ext(staged.Path) != ".mp4" || filepath.Ext(staged.ThumbnailPath) != ".jpg" {
		t.Fatalf("staged extensions = %q, %q", filepath.Ext(staged.Path), filepath.Ext(staged.ThumbnailPath))
	}
	for path, want := range map[string]string{
		staged.Path:          "video data",
		staged.ThumbnailPath: "thumbnail data",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("staged data for %q = %q", path, data)
		}
	}
	cleanup()
	for _, path := range []string{staged.Path, staged.ThumbnailPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("staged path %q was not removed: %v", path, err)
		}
	}
}
