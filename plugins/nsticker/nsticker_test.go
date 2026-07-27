package nsticker

import (
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestValidPackName(t *testing.T) {
	for _, value := range []string{"MyStickers", "pack_1", "a"} {
		if !validPackName(value) {
			t.Fatalf("valid name %q rejected", value)
		}
	}
	for _, value := range []string{"1pack", "_pack", "pack-name", "pack__name", ""} {
		if validPackName(value) {
			t.Fatalf("invalid name %q accepted", value)
		}
	}
}

func TestAutoPackNames(t *testing.T) {
	if got := autoPackName("telebox", telegram.Sticker{}, 1); got != "telebox_1" {
		t.Fatalf("static pack = %q", got)
	}
	if got := autoPackName("telebox", telegram.Sticker{Animated: true}, 2); got != "telebox_animated_2" {
		t.Fatalf("animated pack = %q", got)
	}
	if got := autoPackName("telebox", telegram.Sticker{Video: true}, 3); got != "telebox_video_3" {
		t.Fatalf("video pack = %q", got)
	}
}

func TestStickerTitle(t *testing.T) {
	if got := stickerTitle("alice", telegram.Sticker{Video: true}); got != "@alice 的收藏（视频）" {
		t.Fatalf("title = %q", got)
	}
}
