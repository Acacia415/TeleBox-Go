package inkstone

import (
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestBuildPlainEntry(t *testing.T) {
	t.Parallel()

	message := telegram.Message{
		Text: "请看这个页面",
		Entities: []telegram.MessageEntity{{
			Type: "text_url",
			URL:  "https://example.com/item",
		}},
		Date: time.Date(2026, time.August, 10, 18, 30, 0, 0, time.Local),
	}
	entry, err := buildPlainEntry(message, &entrySource{
		Message: message,
		Sender:  "小王 (@wang)",
		Chat:    "工作群",
		Link:    "https://t.me/c/123/45",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"请看这个页面\n\n链接：<https://example.com/item>",
		"https://example.com/item",
		"来源：发送者 小王 \\(\\@wang\\)",
		"来自 工作群",
		"原消息：<https://t.me/c/123/45>",
	} {
		if !strings.Contains(entry, want) {
			t.Fatalf("entry %q does not contain %q", entry, want)
		}
	}
	if strings.Contains(entry, "- [ ]") || strings.Contains(entry, "<details") {
		t.Fatalf("entry contains an unwanted Markdown block: %q", entry)
	}
}

func TestBuildPlainEntryPreservesParagraphs(t *testing.T) {
	t.Parallel()

	input := "第一段\n\n第二段\n-inkstone"
	entry, err := buildPlainEntry(telegram.Message{Text: input}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "第一段\n\n第二段  \n\\-inkstone"
	if entry != want {
		t.Fatalf("entry = %q, want %q", entry, want)
	}
}

func TestBuildEntryWithMediaAndNoCaption(t *testing.T) {
	t.Parallel()

	entry, err := buildEntry(
		telegram.Message{},
		nil,
		"![图片](</api/files/01k00000000000000000000000>)",
	)
	if err != nil || !strings.Contains(entry, "![图片]") {
		t.Fatalf("media entry = %q, %v", entry, err)
	}
}

func TestMessageLinksUsesTelegramUTF16Offsets(t *testing.T) {
	t.Parallel()

	text := "😀 点这里"
	label := "点这里"
	offset := len(utf16.Encode([]rune("😀 ")))
	length := len(utf16.Encode([]rune(label)))
	links := messageLinks(telegram.Message{
		Text: text,
		Entities: []telegram.MessageEntity{
			{Type: "text_url", Offset: offset, Length: length, URL: "https://example.com/a"},
		},
	})
	if len(links) != 1 || links[0] != "https://example.com/a" {
		t.Fatalf("links = %#v", links)
	}
}
