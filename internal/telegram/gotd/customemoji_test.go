package gotd

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestCustomEmojiFallbacks(t *testing.T) {
	t.Parallel()

	documents := []tg.DocumentClass{
		&tg.Document{
			ID: 42,
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: "emoji.tgs"},
				&tg.DocumentAttributeCustomEmoji{Alt: " ✅ "},
			},
		},
		&tg.DocumentEmpty{ID: 99},
	}
	got := customEmojiFallbacks(documents)
	if got[42] != "✅" {
		t.Fatalf("customEmojiFallbacks()[42] = %q, want ✅", got[42])
	}
	if _, exists := got[99]; exists {
		t.Fatal("customEmojiFallbacks() included an empty document")
	}
}

func TestUniquePositiveIDs(t *testing.T) {
	t.Parallel()

	got := uniquePositiveIDs([]int64{0, 3, 2, 3, -1, 2})
	if len(got) != 2 || got[0] != 3 || got[1] != 2 {
		t.Fatalf("uniquePositiveIDs() = %#v", got)
	}
}
