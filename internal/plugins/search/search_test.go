package search

import (
	"testing"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestSearchArgs(t *testing.T) {
	query, random, spoiler := searchArgs([]string{"The", "Movie", "-S", "-r"})
	if query != "The Movie" || !random || !spoiler {
		t.Fatalf("searchArgs = %q, %v, %v", query, random, spoiler)
	}
}

func TestFuzzyMatch(t *testing.T) {
	for _, test := range []struct {
		text  string
		query string
		want  bool
	}{
		{"The-Movie.S02E03.mp4", "movie s02e03", true},
		{"中文_资源_第3集", "中文 第3集", true},
		{"unrelated", "movie", false},
	} {
		if got := fuzzyMatch(test.text, test.query); got != test.want {
			t.Fatalf("fuzzyMatch(%q, %q) = %v", test.text, test.query, got)
		}
	}
}

func TestUsableRandomVideo(t *testing.T) {
	message := telegram.Message{
		Text: "clean",
		Media: &telegram.Media{
			Kind:     telegram.MediaVideo,
			FileName: "clip.mp4",
			Duration: time.Minute,
		},
	}
	if !isUsableVideo(message, "", defaultAdFilters, true) {
		t.Fatal("one minute clean video was rejected")
	}
	message.Text = "推广频道"
	if isUsableVideo(message, "", defaultAdFilters, true) {
		t.Fatal("advertisement was accepted")
	}
}

func TestOrderedChannelsDefaultFirst(t *testing.T) {
	got := orderedChannels(config{
		Default: "@second",
		Channels: []channel{
			{Handle: "@first"},
			{Handle: "@second"},
		},
	})
	if got[0].Handle != "@second" || got[1].Handle != "@first" {
		t.Fatalf("ordered channels = %+v", got)
	}
}

func TestSplitTargets(t *testing.T) {
	got := splitTargets("@one\\ @two\n@one")
	if len(got) != 2 || got[0] != "@one" || got[1] != "@two" {
		t.Fatalf("targets = %#v", got)
	}
}
