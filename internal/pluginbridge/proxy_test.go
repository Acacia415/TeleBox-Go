package pluginbridge

import (
	"os"
	"path/filepath"
	"testing"
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
