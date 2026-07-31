package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/service"
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

func TestLoadLegacyTraceStateFromPreservedAssets(t *testing.T) {
	t.Parallel()

	legacyRoot := filepath.Join(t.TempDir(), "legacy-assets")
	traceRoot := filepath.Join(legacyRoot, "trace")
	if err := os.MkdirAll(traceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(traceRoot, "db.json"),
		[]byte(`{
			"users":{"123":["👍"]},
			"keywords":{"telebox":["🔥"]},
			"config":{"keepLog":true,"big":true}
		}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	p := New(service.Container{LegacyAssetsDir: legacyRoot})
	state, found, err := p.loadLegacyState()
	if err != nil {
		t.Fatal(err)
	}
	if !found ||
		len(state.Users["123"]) != 1 ||
		len(state.Keywords["telebox"]) != 1 ||
		!state.Config.KeepLog ||
		!state.Config.Big {
		t.Fatalf("legacy trace state = %+v, found=%v", state, found)
	}
}
