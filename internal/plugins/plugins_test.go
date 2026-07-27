package plugins

import (
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/service"
)

func TestBuiltinsMatchBackupInventory(t *testing.T) {
	expected := map[string]struct{}{
		"aban":            {},
		"ai":              {},
		"bin":             {},
		"bulk_delete":     {},
		"cezi":            {},
		"convert":         {},
		"dc":              {},
		"dig":             {},
		"eat":             {},
		"eatgif":          {},
		"gif":             {},
		"ids":             {},
		"ip":              {},
		"isalive":         {},
		"jointime":        {},
		"music_bot":       {},
		"nsticker":        {},
		"rate":            {},
		"re":              {},
		"search":          {},
		"speedlink":       {},
		"speedtest":       {},
		"telegram-backup": {},
		"trace":           {},
		"yt-dlp":          {},
		"yvlu":            {},
		"zhijiao":         {},
	}
	builtins := Builtins(service.Container{})
	if len(builtins) != len(expected) {
		t.Fatalf("builtin count = %d, want %d", len(builtins), len(expected))
	}
	seen := make(map[string]struct{}, len(builtins))
	for _, candidate := range builtins {
		name := candidate.Metadata().Name
		if _, duplicate := seen[name]; duplicate {
			t.Fatalf("duplicate builtin plugin %q", name)
		}
		seen[name] = struct{}{}
		if _, exists := expected[name]; !exists {
			t.Fatalf("unexpected builtin plugin %q", name)
		}
	}
	for name := range expected {
		if _, exists := seen[name]; !exists {
			t.Fatalf("missing builtin plugin %q", name)
		}
	}
}
