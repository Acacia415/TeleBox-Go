package search

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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

func TestPrepareVideoThumbnail(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 960, 540))
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x % 256),
				G: uint8(y % 256),
				B: uint8((x + y) % 256),
				A: 255,
			})
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, source); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "thumbnail.jpg")
	if err := prepareVideoThumbnail(input.Bytes(), path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxThumbnailBytes {
		t.Fatalf("thumbnail size = %d", info.Size())
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoded, format, err := image.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" {
		t.Fatalf("thumbnail format = %q", format)
	}
	if decoded.Bounds().Dx() != 320 || decoded.Bounds().Dy() != 180 {
		t.Fatalf("thumbnail dimensions = %v", decoded.Bounds())
	}
}

func TestPrepareVideoThumbnailRejectsInvalidImage(t *testing.T) {
	err := prepareVideoThumbnail(
		[]byte("not an image"),
		filepath.Join(t.TempDir(), "thumbnail.jpg"),
	)
	if err == nil {
		t.Fatal("prepareVideoThumbnail() error = nil")
	}
}
