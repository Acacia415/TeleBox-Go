package yvlu

import (
	"bytes"
	"testing"

	"golang.org/x/image/font/opentype"
)

func TestParseOptions(t *testing.T) {
	tests := []struct {
		raw      string
		expected options
	}{
		{"", options{Count: 1}},
		{"3", options{Count: 3}},
		{"r 2", options{Count: 2, IncludeReply: true}},
		{"u @alice 4", options{Count: 4, FakeSender: "@alice"}},
		{"ur 123", options{Count: 1, IncludeReply: true, FakeSender: "123"}},
		{"f 这是一条 假消息", options{Count: 1, FakeText: "这是一条 假消息"}},
		{"fr 带回复", options{Count: 1, IncludeReply: true, FakeText: "带回复"}},
	}
	for _, test := range tests {
		got, err := parseOptions(test.raw)
		if err != nil {
			t.Fatalf("parseOptions(%q): %v", test.raw, err)
		}
		if got != test.expected {
			t.Fatalf("parseOptions(%q) = %#v, want %#v", test.raw, got, test.expected)
		}
	}
}

func TestParseOptionsRejectsInvalidCount(t *testing.T) {
	for _, raw := range []string{"0", "6", "r nope", "u @alice 9"} {
		if _, err := parseOptions(raw); err == nil {
			t.Fatalf("parseOptions(%q) succeeded", raw)
		}
	}
}

func TestLocalRendererProducesPNG(t *testing.T) {
	renderer, err := newRenderer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := renderer.render([]quoteItem{{
		Sender: "Alice",
		Text:   "Hello, TeleBox!",
		Reply:  "Bob: hi",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("not PNG: %x", data[:8])
	}
	if len(data) > 512<<10 {
		t.Fatalf("sticker exceeds 512 KiB: %d", len(data))
	}
}

func TestWrapText(t *testing.T) {
	renderer, err := newRenderer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	face, err := opentype.NewFace(renderer.font, &opentype.FaceOptions{Size: 16, DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	lines := wrapText(face, "abcdefghij", 30, 2)
	if len(lines) != 2 {
		t.Fatalf("lines = %#v", lines)
	}
}
