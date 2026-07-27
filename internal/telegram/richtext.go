package telegram

import (
	"context"
	"html"
	"regexp"
)

// RichTextClient is an optional extension implemented by transports that can
// convert trusted Telegram HTML into message entities. Plugins use the helper
// functions below so test doubles and future transports can fall back to plain
// text without implementing this interface.
type RichTextClient interface {
	SendHTML(context.Context, int64, string) (SentMessage, error)
	ReplyHTML(context.Context, int64, int, string) (SentMessage, error)
	EditHTML(context.Context, int64, int, string) (SentMessage, error)
}

var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

func SendHTML(
	ctx context.Context,
	client Client,
	chatID int64,
	text string,
) (SentMessage, error) {
	if rich, ok := client.(RichTextClient); ok {
		return rich.SendHTML(ctx, chatID, text)
	}
	return client.SendText(ctx, chatID, plainTextFromHTML(text))
}

func ReplyHTML(
	ctx context.Context,
	client Client,
	chatID int64,
	replyToID int,
	text string,
) (SentMessage, error) {
	if rich, ok := client.(RichTextClient); ok {
		return rich.ReplyHTML(ctx, chatID, replyToID, text)
	}
	return client.ReplyText(ctx, chatID, replyToID, plainTextFromHTML(text))
}

func EditHTML(
	ctx context.Context,
	client Client,
	chatID int64,
	messageID int,
	text string,
) (SentMessage, error) {
	if rich, ok := client.(RichTextClient); ok {
		return rich.EditHTML(ctx, chatID, messageID, text)
	}
	return client.EditText(ctx, chatID, messageID, plainTextFromHTML(text))
}

func plainTextFromHTML(text string) string {
	return html.UnescapeString(htmlTagPattern.ReplaceAllString(text, ""))
}
