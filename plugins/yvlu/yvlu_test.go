package yvlu

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestParseOptionsMatchesBackupModes(t *testing.T) {
	tests := []struct {
		raw      string
		expected options
		valid    bool
	}{
		{"", options{Count: 1}, true},
		{"3", options{Count: 3}, true},
		{"r 2", options{Count: 2, IncludeReply: true}, true},
		{"u @alice 4", options{Count: 4, FakeSender: "@alice"}, true},
		{"ur 123", options{Count: 1, IncludeReply: true, FakeSender: "123"}, true},
		{"f 这是一条 假消息", options{Count: 1, FakeText: "这是一条 假消息"}, true},
		{"fr 带回复", options{Count: 1, IncludeReply: true, FakeText: "带回复"}, true},
		{"-1", options{Count: 1}, false},
		{"+1", options{Count: 1}, false},
		{"unknown", options{Count: 1}, false},
	}
	for _, test := range tests {
		got, valid := parseOptions(test.raw)
		if valid != test.valid || got != test.expected {
			t.Fatalf(
				"parseOptions(%q) = %#v, %v; want %#v, %v",
				test.raw,
				got,
				valid,
				test.expected,
				test.valid,
			)
		}
	}
}

func TestQuoteRequestMatchesBackupFormat(t *testing.T) {
	document, err := json.Marshal(newQuoteRequest([]quoteMessage{{
		From:     quoteSender{ID: 1, FirstName: "Alice"},
		Text:     "你好",
		Entities: []quoteEntity{},
		Avatar:   true,
	}}))
	if err != nil {
		t.Fatal(err)
	}
	text := string(document)
	for _, want := range []string{
		`"type":"quote"`,
		`"format":"webp"`,
		`"backgroundColor":"#1b1429"`,
		`"width":512`,
		`"height":768`,
		`"scale":2`,
		`"emojiBrand":"apple"`,
		`"first_name":"Alice"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("quote request does not contain %s:\n%s", want, text)
		}
	}
}

func TestFakeMessageEntitiesKeepRichTextOffsets(t *testing.T) {
	message := telegram.Message{
		Text: "-yvlu f 加粗文字",
		Entities: []telegram.MessageEntity{{
			Type:   "bold",
			Offset: 8,
			Length: 4,
		}},
	}
	got := fakeMessageEntities(message, "f 加粗文字", "加粗文字")
	if len(got) != 1 || got[0].Offset != 0 ||
		got[0].Length != 4 || got[0].Type != "bold" {
		t.Fatalf("fake entities = %+v", got)
	}
}

func TestHelpUsesConfiguredPrefix(t *testing.T) {
	got := helpHTML("-")
	if !strings.Contains(got, "-yvlu ur") || strings.Contains(got, "{{prefix}}") {
		t.Fatalf("help = %q", got)
	}
}
