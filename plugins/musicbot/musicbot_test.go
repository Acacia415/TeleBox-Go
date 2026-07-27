package musicbot

import (
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestBuildQuery(t *testing.T) {
	tests := []struct {
		action  string
		keyword string
		bot     string
		query   string
		display string
		ok      bool
	}{
		{"search", "洛天依 网易云", defaultBot, "/search 洛天依 网易云", "洛天依 网易云", true},
		{"kuwo", "song", defaultBot, "/kuwo song", "song", true},
		{"vk", "song", vkBot, "song", "song", true},
		{"ym", "song", ymBot, "song lyric】", "song", true},
		{"unknown", "song", "", "", "", false},
		{"search", " ", "", "", "", false},
	}
	for _, test := range tests {
		bot, query, display, ok := buildQuery(test.action, test.keyword)
		if bot != test.bot || query != test.query || display != test.display || ok != test.ok {
			t.Fatalf(
				"buildQuery(%q, %q) = (%q, %q, %q, %v)",
				test.action,
				test.keyword,
				bot,
				query,
				display,
				ok,
			)
		}
	}
}

func TestMediaFilename(t *testing.T) {
	if got := mediaFilename(telegram.Media{FileName: `..\bad:name.mp3`}); got != "badname.mp3" {
		t.Fatalf("sanitized filename = %q", got)
	}
	if got := mediaFilename(telegram.Media{Kind: telegram.MediaAudio}); got != "music.mp3" {
		t.Fatalf("audio filename = %q", got)
	}
}
