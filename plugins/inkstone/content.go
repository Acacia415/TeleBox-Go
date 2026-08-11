package inkstone

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

var (
	urlPattern = regexp.MustCompile(`https?://[^\s<>]+`)
)

type entrySource struct {
	Message telegram.Message
	Sender  string
	Chat    string
	Link    string
}

func buildPlainEntry(message telegram.Message, source *entrySource) (string, error) {
	plainText := cleanMessageText(message.Text)
	if plainText == "" {
		return "", errors.New("回复消息没有可写入的文字")
	}
	if len([]rune(plainText)) > 20_000 {
		return "", errors.New("单次写入内容不能超过 20000 个字符")
	}
	entry := telegramMarkdown(message)
	if entry == "" {
		return "", errors.New("回复消息没有可写入的文字")
	}
	if source == nil {
		return entry, nil
	}

	metadata := make([]string, 0, 4)
	for _, link := range messageLinks(source.Message) {
		if !strings.Contains(entry, link) {
			metadata = append(metadata, "链接：<"+encodeMarkdownDestination(link)+">")
		}
	}
	description := make([]string, 0, 3)
	if source.Sender != "" {
		description = append(description, "发送者 "+escapeMarkdownLiteral(source.Sender))
	}
	if !source.Message.Date.IsZero() {
		description = append(description, source.Message.Date.Local().Format("2006-01-02 15:04"))
	}
	if source.Chat != "" {
		description = append(description, "来自 "+escapeMarkdownLiteral(source.Chat))
	}
	if len(description) > 0 {
		metadata = append(metadata, "来源："+strings.Join(description, " · "))
	}
	if source.Link != "" {
		metadata = append(metadata, "原消息：<"+encodeMarkdownDestination(source.Link)+">")
	}
	if len(metadata) > 0 {
		entry += "\n\n" + strings.Join(metadata, "\n")
	}
	return entry, nil
}

func resolveEntrySource(
	ctx context.Context,
	services service.Container,
	message telegram.Message,
) entrySource {
	result := entrySource{Message: message}
	if services.Telegram == nil {
		return result
	}
	senderID := message.SenderID
	if message.ForwardSenderID != 0 {
		senderID = message.ForwardSenderID
	}
	if senderID != 0 {
		if user, err := services.Telegram.ResolveUser(
			ctx,
			strconv.FormatInt(senderID, 10),
		); err == nil {
			result.Sender = strings.TrimSpace(user.FirstName + " " + user.LastName)
			if result.Sender == "" {
				result.Sender = strings.TrimPrefix(user.Username, "@")
			}
			if user.Username != "" {
				if result.Sender == "" {
					result.Sender = "@" + strings.TrimPrefix(user.Username, "@")
				} else {
					result.Sender += " (@" + strings.TrimPrefix(user.Username, "@") + ")"
				}
			}
		}
	}
	if message.ForwardName != "" {
		result.Sender = message.ForwardName
	}
	if chat, err := services.Telegram.ResolveChat(ctx, message.ChatID); err == nil {
		result.Chat = strings.TrimSpace(chat.Title)
		username := strings.TrimPrefix(strings.TrimSpace(chat.Username), "@")
		if username != "" {
			if result.Chat == "" {
				result.Chat = "@" + username
			}
			if chat.Kind != telegram.ChatPrivate {
				result.Link = fmt.Sprintf("https://t.me/%s/%d", username, message.ID)
			}
		}
	}
	if result.Link == "" && message.ChatID <= -1_000_000_000_000 {
		channelID := -message.ChatID - 1_000_000_000_000
		if channelID > 0 {
			result.Link = fmt.Sprintf("https://t.me/c/%d/%d", channelID, message.ID)
		}
	}
	return result
}

func messageLinks(message telegram.Message) []string {
	seen := make(map[string]struct{})
	var result []string
	add := func(value string) {
		value = trimURL(strings.TrimSpace(value))
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	for _, entity := range message.Entities {
		if entity.URL != "" {
			add(entity.URL)
			continue
		}
		if strings.EqualFold(entity.Type, "url") {
			add(utf16Substring(message.Text, entity.Offset, entity.Length))
		}
	}
	for _, value := range urlPattern.FindAllString(message.Text, -1) {
		add(value)
	}
	sort.Strings(result)
	return result
}

func utf16Substring(value string, offset, length int) string {
	if offset < 0 || length <= 0 {
		return ""
	}
	encoded := utf16.Encode([]rune(value))
	if offset >= len(encoded) {
		return ""
	}
	end := min(len(encoded), offset+length)
	return string(utf16.Decode(encoded[offset:end]))
}

func cleanMessageText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.TrimSpace(strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' || character >= ' ' {
			return character
		}
		return -1
	}, value))
	return value
}

func trimURL(value string) string {
	return strings.TrimRight(value, ".,;:!?，。；：！？)]}）】")
}
