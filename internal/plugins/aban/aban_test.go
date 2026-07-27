package aban

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	for input, want := range map[string]time.Duration{
		"30s": 30 * time.Second,
		"10m": 10 * time.Minute,
		"2h":  2 * time.Hour,
		"7d":  7 * 24 * time.Hour,
	} {
		got, err := parseDuration(input)
		if err != nil {
			t.Fatalf("parseDuration(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseDuration(%q) = %v, want %v", input, got, want)
		}
	}
	if _, err := parseDuration("forever"); err == nil {
		t.Fatal("invalid duration succeeded")
	}
}
