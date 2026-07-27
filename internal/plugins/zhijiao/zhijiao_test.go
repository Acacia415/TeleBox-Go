package zhijiao

import (
	"strings"
	"testing"
)

func TestJiaciCoversAllCombinations(t *testing.T) {
	if len(jiaci) != 27 {
		t.Fatalf("jiaci entries = %d, want 27", len(jiaci))
	}
	for _, first := range tossOptions {
		for _, second := range tossOptions {
			for _, third := range tossOptions {
				key := first + second + third
				if jiaci[key] == "" {
					t.Fatalf("missing jiaci for %s", key)
				}
			}
		}
	}
}

func TestFinalText(t *testing.T) {
	got := finalText([]string{"胜", "胜", "阳"})
	for _, fragment := range []string{"第1投：胜 ☾☽", "胜胜阳", "上上大吉"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("final text missing %q:\n%s", fragment, got)
		}
	}
}
