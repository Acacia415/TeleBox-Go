package inkstone

import (
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type entitySpan struct {
	entity   telegram.MessageEntity
	start    int
	end      int
	children []*entitySpan
}

func telegramMarkdown(message telegram.Message) string {
	units := utf16.Encode([]rune(message.Text))
	spans := entityTree(message.Entities, len(units))
	return strings.TrimSpace(renderEntityRange(units, 0, len(units), spans))
}

func entityTree(entities []telegram.MessageEntity, textLength int) []*entitySpan {
	spans := make([]*entitySpan, 0, len(entities))
	for _, entity := range entities {
		start := entity.Offset
		end := entity.Offset + entity.Length
		if start < 0 || end <= start || start >= textLength || end > textLength {
			continue
		}
		spans = append(spans, &entitySpan{entity: entity, start: start, end: end})
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		if spans[i].end != spans[j].end {
			return spans[i].end > spans[j].end
		}
		return entityPriority(spans[i].entity.Type) < entityPriority(spans[j].entity.Type)
	})

	roots := make([]*entitySpan, 0, len(spans))
	stack := make([]*entitySpan, 0, 4)
	for _, span := range spans {
		for len(stack) > 0 && span.start >= stack[len(stack)-1].end {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, span)
			stack = append(stack, span)
			continue
		}
		parent := stack[len(stack)-1]
		if span.end > parent.end {
			// Telegram normally emits disjoint or properly nested entities. Ignore a
			// malformed crossing entity instead of producing broken Markdown.
			continue
		}
		parent.children = append(parent.children, span)
		stack = append(stack, span)
	}
	return roots
}

func entityPriority(entityType string) int {
	switch strings.ToLower(entityType) {
	case "text_link":
		return 0
	case "bold":
		return 1
	case "italic":
		return 2
	case "underline":
		return 3
	case "strikethrough":
		return 4
	case "spoiler":
		return 5
	default:
		return 10
	}
}

func renderEntityRange(
	units []uint16,
	start int,
	end int,
	children []*entitySpan,
) string {
	var output strings.Builder
	cursor := start
	for _, child := range children {
		if child.start < cursor || child.end > end {
			continue
		}
		output.WriteString(escapeMarkdownLiteral(decodeUTF16(units[cursor:child.start])))
		output.WriteString(renderEntity(units, child))
		cursor = child.end
	}
	output.WriteString(escapeMarkdownLiteral(decodeUTF16(units[cursor:end])))
	return output.String()
}

func renderEntity(units []uint16, span *entitySpan) string {
	raw := decodeUTF16(units[span.start:span.end])
	typeName := strings.ToLower(span.entity.Type)
	if typeName == "code" {
		return inlineCode(raw)
	}
	if typeName == "pre" {
		return fencedCode(raw, span.entity.Language)
	}

	inner := renderEntityRange(units, span.start, span.end, span.children)
	switch typeName {
	case "bold":
		return "**" + inner + "**"
	case "italic":
		return "*" + inner + "*"
	case "underline":
		return "++" + inner + "++"
	case "strikethrough", "strike":
		return "~~" + inner + "~~"
	case "spoiler":
		// Inkstone has no spoiler primitive. Highlighting keeps the marked range
		// visible and distinct without introducing another collapsible block.
		return "==" + inner + "=="
	case "blockquote":
		return markdownQuote(inner)
	case "text_link":
		if safeMarkdownURL(span.entity.URL) {
			return markdownLinkWithRenderedLabel(inner, span.entity.URL)
		}
		return inner
	case "url":
		if safeMarkdownURL(raw) {
			return "<" + encodeMarkdownDestination(raw) + ">"
		}
		return escapeMarkdownLiteral(raw)
	case "email":
		if !strings.ContainsAny(raw, "<>\r\n") {
			return "<" + raw + ">"
		}
		return escapeMarkdownLiteral(raw)
	case "custom_emoji":
		return renderCustomEmoji(raw, span.entity.Emoji)
	default:
		// Mentions, hashtags, commands, phone numbers and bank cards fall back to
		// their visible Telegram text. Telegram-only click actions are not
		// meaningful inside Inkstone.
		return inner
	}
}

func renderCustomEmoji(raw string, resolved string) string {
	if fallback := cleanCustomEmojiText(resolved); fallback != "" {
		return escapeMarkdownLiteral(fallback)
	}
	return escapeMarkdownLiteral(cleanCustomEmojiText(raw))
}

func cleanCustomEmojiText(value string) string {
	value = strings.TrimSpace(strings.Map(func(character rune) rune {
		switch character {
		case '\uFFFC', '\uFFFD':
			return -1
		default:
			return character
		}
	}, value))
	// These characters are commonly produced as missing-glyph placeholders for
	// custom emoji. A successfully resolved Telegram alt takes precedence, so
	// only an unresolved standalone placeholder is discarded.
	switch value {
	case "□", "▯":
		return ""
	default:
		return value
	}
}

func escapeMarkdownLiteral(value string) string {
	var output strings.Builder
	for index := 0; index < len(value); {
		character, size := utf8.DecodeRuneInString(value[index:])
		index += size
		switch character {
		case '\r':
			continue
		case '\n':
			run := 1
			for index < len(value) && value[index] == '\n' {
				run++
				index++
			}
			if run == 1 {
				output.WriteString("  \n")
			} else {
				output.WriteString(strings.Repeat("\n", run))
			}
			continue
		}
		if character < ' ' && character != '\t' {
			continue
		}
		if isASCIIPunctuation(character) {
			output.WriteByte('\\')
		}
		output.WriteRune(character)
	}
	return output.String()
}

func isASCIIPunctuation(character rune) bool {
	return character >= '!' && character <= '/' ||
		character >= ':' && character <= '@' ||
		character >= '[' && character <= '`' ||
		character >= '{' && character <= '~'
}

func inlineCode(value string) string {
	content := strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", " "), "\n", " ")
	fence := strings.Repeat("`", max(1, longestRun(content, '`')+1))
	padding := ""
	characters := []rune(content)
	if len(characters) > 0 && (unicode.IsSpace(characters[0]) ||
		unicode.IsSpace(characters[len(characters)-1]) ||
		strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`")) {
		padding = " "
	}
	return fence + padding + content + padding + fence
}

func fencedCode(value string, language string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.TrimSuffix(value, "\n")
	fence := strings.Repeat("`", max(3, longestRun(value, '`')+1))
	language = safeFenceLanguage(language)
	return "\n\n" + fence + language + "\n" + value + "\n" + fence + "\n\n"
}

func safeFenceLanguage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.IndexFunc(value, func(character rune) bool {
		return !(unicode.IsLetter(character) || unicode.IsDigit(character) ||
			strings.ContainsRune("+#._-", character))
	}) >= 0 {
		return ""
	}
	return value
}

func longestRun(value string, character rune) int {
	longest := 0
	current := 0
	for _, candidate := range value {
		if candidate == character {
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	return longest
}

func markdownQuote(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "  \n", "\n"))
	lines := strings.Split(value, "\n")
	for index := range lines {
		lines[index] = "> " + lines[index]
	}
	return "\n\n" + strings.Join(lines, "\n") + "\n\n"
}

func markdownLinkWithRenderedLabel(label string, target string) string {
	return "[" + label + "](<" + encodeMarkdownDestination(target) + ">)"
}

func encodeMarkdownDestination(value string) string {
	return strings.NewReplacer(
		" ", "%20",
		"\t", "%09",
		"\n", "%0A",
		"<", "%3C",
		">", "%3E",
	).Replace(strings.TrimSpace(value))
}

func safeMarkdownURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto", "tel":
		return true
	default:
		return false
	}
}

func decodeUTF16(value []uint16) string {
	return string(utf16.Decode(value))
}

func sliceMessageEntities(
	fullText string,
	entities []telegram.MessageEntity,
	rawArgs string,
	content string,
) []telegram.MessageEntity {
	rawOffset := strings.Index(fullText, rawArgs)
	contentOffset := strings.Index(rawArgs, content)
	if rawOffset < 0 || contentOffset < 0 {
		return nil
	}
	cut := len(utf16.Encode([]rune(fullText[:rawOffset+contentOffset])))
	contentLength := len(utf16.Encode([]rune(content)))
	result := make([]telegram.MessageEntity, 0, len(entities))
	for _, entity := range entities {
		start := entity.Offset
		end := entity.Offset + entity.Length
		if end <= cut || start >= cut+contentLength {
			continue
		}
		start = max(start, cut)
		end = min(end, cut+contentLength)
		entity.Offset = start - cut
		entity.Length = end - start
		result = append(result, entity)
	}
	return result
}
