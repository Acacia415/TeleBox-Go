package trace

import (
	"strings"
	"testing"
)

func TestParseReactions(t *testing.T) {
	got := parseReactions("👍👍❤️‍🔥", []int64{42, 42})
	if len(got) != 3 {
		t.Fatalf("reaction count = %d: %+v", len(got), got)
	}
	if got[0].Emoji != "❤️‍🔥" || got[1].Emoji != "👍" || got[2].DocumentID != 42 {
		t.Fatalf("reactions = %+v", got)
	}
}

func TestReactionDisplay(t *testing.T) {
	got := reactionDisplay(parseReactions("🔥", []int64{123}))
	if !strings.Contains(got, "🔥") || !strings.Contains(got, "Premium:123") {
		t.Fatalf("display = %q", got)
	}
}
