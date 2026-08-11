package inkstone

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestTelegramMarkdownEscapesUnformattedHTMLAndMarkdown(t *testing.T) {
	t.Parallel()

	got := telegramMarkdown(telegram.Message{
		Text: "说明 <details>\n- [ ] 不是任务",
	})
	want := "说明 \\<details\\>  \n\\- \\[ \\] 不是任务"
	if got != want {
		t.Fatalf("telegramMarkdown() = %q, want %q", got, want)
	}
}

func TestTelegramMarkdownPreservesSupportedFormatting(t *testing.T) {
	t.Parallel()

	text := "粗体 斜体 下划线 删除线 等宽 剧透"
	message := telegram.Message{Text: text}
	for _, item := range []struct {
		value      string
		entityType string
	}{
		{"粗体", "bold"},
		{"斜体", "italic"},
		{"下划线", "underline"},
		{"删除线", "strikethrough"},
		{"等宽", "code"},
		{"剧透", "spoiler"},
	} {
		message.Entities = append(message.Entities, entityFor(text, item.value, item.entityType))
	}
	got := telegramMarkdown(message)
	want := "**粗体** *斜体* ++下划线++ ~~删除线~~ `等宽` ==剧透=="
	if got != want {
		t.Fatalf("telegramMarkdown() = %q, want %q", got, want)
	}
}

func TestTelegramMarkdownTurnsTelegramCodeIntoSafeMarkdown(t *testing.T) {
	t.Parallel()

	inlineText := "普通 <details> 后面"
	inline := telegram.Message{
		Text: inlineText,
		Entities: []telegram.MessageEntity{
			entityFor(inlineText, "<details>", "code"),
		},
	}
	if got, want := telegramMarkdown(inline), "普通 `<details>` 后面"; got != want {
		t.Fatalf("inline code = %q, want %q", got, want)
	}

	blockText := "示例\nfmt.Println(`ok`)\n结束"
	pre := entityFor(blockText, "fmt.Println(`ok`)", "pre")
	pre.Language = "go"
	block := telegramMarkdown(telegram.Message{
		Text:     blockText,
		Entities: []telegram.MessageEntity{pre},
	})
	if !strings.Contains(block, "```go\nfmt.Println(`ok`)\n```") {
		t.Fatalf("code block = %q", block)
	}
}

func TestTelegramMarkdownPreservesLinksQuotesAndNestedEntities(t *testing.T) {
	t.Parallel()

	text := "访问网站\n引用内容\n粗斜体"
	link := entityFor(text, "网站", "text_link")
	link.URL = "https://example.com/a?q=1"
	quote := entityFor(text, "引用内容", "blockquote")
	bold := entityFor(text, "粗斜体", "bold")
	italic := entityFor(text, "斜体", "italic")
	got := telegramMarkdown(telegram.Message{
		Text:     text,
		Entities: []telegram.MessageEntity{link, quote, bold, italic},
	})
	for _, want := range []string{
		"访问[网站](<https://example.com/a?q=1>)",
		"> 引用内容",
		"**粗*斜体***",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted text %q does not contain %q", got, want)
		}
	}
}

func TestSliceMessageEntitiesUsesTelegramUTF16Offsets(t *testing.T) {
	t.Parallel()

	fullText := "-ink hx 😀等宽"
	rawArgs := "hx 😀等宽"
	content := "😀等宽"
	entity := entityFor(fullText, "等宽", "code")
	entities := sliceMessageEntities(fullText, []telegram.MessageEntity{entity}, rawArgs, content)
	if len(entities) != 1 || entities[0].Offset != 2 || entities[0].Length != 2 {
		t.Fatalf("sliced entities = %+v", entities)
	}
	if got := telegramMarkdown(telegram.Message{Text: content, Entities: entities}); got != "😀`等宽`" {
		t.Fatalf("formatted command content = %q", got)
	}
}

func TestTelegramMarkdownResolvesCustomEmojiToUnicode(t *testing.T) {
	t.Parallel()

	message := telegram.Message{
		Text: "□ 通过 □ 失败",
		Entities: []telegram.MessageEntity{
			{Type: "custom_emoji", Offset: 0, Length: 1, DocumentID: 10, Emoji: "✅"},
			{Type: "custom_emoji", Offset: 5, Length: 1, DocumentID: 11, Emoji: "❌"},
		},
	}
	if got, want := telegramMarkdown(message), "✅ 通过 ❌ 失败"; got != want {
		t.Fatalf("telegramMarkdown() = %q, want %q", got, want)
	}
}

func TestTelegramMarkdownDropsOnlyUnresolvedCustomEmojiPlaceholders(t *testing.T) {
	t.Parallel()

	message := telegram.Message{
		Text: "□ 装饰后仍有文字 ✓",
		Entities: []telegram.MessageEntity{
			{Type: "custom_emoji", Offset: 0, Length: 1, DocumentID: 10},
		},
	}
	if got, want := telegramMarkdown(message), "装饰后仍有文字 ✓"; got != want {
		t.Fatalf("telegramMarkdown() = %q, want %q", got, want)
	}
}

func entityFor(text string, value string, entityType string) telegram.MessageEntity {
	byteOffset := strings.Index(text, value)
	if byteOffset < 0 {
		panic("test entity value was not found")
	}
	return telegram.MessageEntity{
		Type:   entityType,
		Offset: len(utf16.Encode([]rune(text[:byteOffset]))),
		Length: len(utf16.Encode([]rune(value))),
	}
}
