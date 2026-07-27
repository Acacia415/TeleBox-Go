package gif

import (
	"strings"
	"testing"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestSupportedMedia(t *testing.T) {
	for _, media := range []*telegram.Media{
		{Kind: telegram.MediaVideo},
		{Kind: telegram.MediaAnimation},
		{Kind: telegram.MediaDocument, MIMEType: "image/gif"},
		{Kind: telegram.MediaDocument, FileName: "clip.WEBM"},
	} {
		if !supported(media) {
			t.Fatalf("supported media rejected: %+v", media)
		}
	}
	if supported(&telegram.Media{Kind: telegram.MediaPhoto}) {
		t.Fatal("photo was accepted")
	}
}

func TestVideoFilterSpeedsLongInput(t *testing.T) {
	filter := videoFilter(6 * time.Second)
	for _, fragment := range []string{"setpts=0.50000000*PTS", "scale=512:512", "fps=30"} {
		if !strings.Contains(filter, fragment) {
			t.Fatalf("filter %q missing %q", filter, fragment)
		}
	}
}

func TestSafeExtension(t *testing.T) {
	if got := safeExtension("movie.MP4"); got != ".mp4" {
		t.Fatalf("extension = %q", got)
	}
	if got := safeExtension("payload.exe"); got != ".bin" {
		t.Fatalf("unsafe extension = %q", got)
	}
}
