package usererror

import (
	"context"
	"log/slog"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const maxLoggedText = 4096

// Client filters user-facing Telegram output while embedding the original
// client for every non-text operation.
type Client struct {
	telegram.Client
	logger *slog.Logger
}

var (
	_ telegram.Client         = (*Client)(nil)
	_ telegram.RichTextClient = (*Client)(nil)
)

func Wrap(client telegram.Client, logger *slog.Logger) telegram.Client {
	if client == nil {
		return nil
	}
	return &Client{Client: client, logger: logger}
}

func (c *Client) SendText(
	ctx context.Context,
	chatID int64,
	text string,
) (telegram.SentMessage, error) {
	return c.Client.SendText(ctx, chatID, c.sanitize(text))
}

func (c *Client) ReplyText(
	ctx context.Context,
	chatID int64,
	replyToID int,
	text string,
) (telegram.SentMessage, error) {
	return c.Client.ReplyText(ctx, chatID, replyToID, c.sanitize(text))
}

func (c *Client) EditText(
	ctx context.Context,
	chatID int64,
	messageID int,
	text string,
) (telegram.SentMessage, error) {
	return c.Client.EditText(ctx, chatID, messageID, c.sanitize(text))
}

func (c *Client) SendHTML(
	ctx context.Context,
	chatID int64,
	text string,
) (telegram.SentMessage, error) {
	return telegram.SendHTML(ctx, c.Client, chatID, c.sanitize(text))
}

func (c *Client) ReplyHTML(
	ctx context.Context,
	chatID int64,
	replyToID int,
	text string,
) (telegram.SentMessage, error) {
	return telegram.ReplyHTML(
		ctx,
		c.Client,
		chatID,
		replyToID,
		c.sanitize(text),
	)
}

func (c *Client) EditHTML(
	ctx context.Context,
	chatID int64,
	messageID int,
	text string,
) (telegram.SentMessage, error) {
	return telegram.EditHTML(
		ctx,
		c.Client,
		chatID,
		messageID,
		c.sanitize(text),
	)
}

func (c *Client) SendFile(
	ctx context.Context,
	chatID int64,
	upload telegram.Upload,
) (telegram.SentMessage, error) {
	upload.Caption = c.sanitize(upload.Caption)
	return c.Client.SendFile(ctx, chatID, upload)
}

func (c *Client) sanitize(text string) string {
	localized, changed := SanitizeText(text)
	if !changed {
		return text
	}
	if c.logger != nil {
		c.logger.Warn(
			"localized technical error before sending Telegram response",
			"original", truncate(text),
			"localized", localized,
		)
	}
	return localized
}

func truncate(text string) string {
	value := []rune(text)
	if len(value) <= maxLoggedText {
		return text
	}
	return string(value[:maxLoggedText]) + "…"
}
