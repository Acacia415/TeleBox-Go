package gotd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gotdpeers "github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (c *Client) RequestBotMedia(
	ctx context.Context,
	request teleboxtelegram.BotMediaRequest,
) (teleboxtelegram.Message, error) {
	if strings.TrimSpace(request.Bot) == "" || strings.TrimSpace(request.Query) == "" {
		return teleboxtelegram.Message{}, errors.New("bot and query are required")
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > time.Minute {
		timeout = time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resolved, err := c.peers.Resolve(runCtx, request.Bot)
	if err != nil {
		return teleboxtelegram.Message{}, fmt.Errorf("resolve music bot: %w", err)
	}
	bot, ok := resolved.(gotdpeers.User)
	if !ok || !bot.Raw().Bot {
		return teleboxtelegram.Message{}, errors.New("configured music peer is not a bot")
	}
	botID := int64(bot.TDLibPeerID())
	c.mu.Lock()
	c.peerCache[botID] = bot.InputPeer()
	c.mu.Unlock()
	_, _ = c.raw.API().ContactsUnblock(runCtx, &tg.ContactsUnblockRequest{
		ID: bot.InputPeer(),
	})

	startedAt := time.Now().Add(-time.Second)
	if _, err := c.SendText(runCtx, botID, request.Query); err != nil {
		_, _ = c.SendText(runCtx, botID, "/start")
		select {
		case <-runCtx.Done():
			return teleboxtelegram.Message{}, runCtx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		if _, retryErr := c.SendText(runCtx, botID, request.Query); retryErr != nil {
			return teleboxtelegram.Message{}, fmt.Errorf("send bot query: %w", retryErr)
		}
	}
	buttonMessage, err := c.waitBotButton(runCtx, bot, startedAt)
	if err != nil {
		return teleboxtelegram.Message{}, err
	}
	if err := c.pressFirstButton(runCtx, bot, buttonMessage); err != nil {
		// Some music bots accept a numeric selection even when their markup
		// is a URL or another unsupported button type.
		if _, sendErr := c.SendText(runCtx, botID, "1"); sendErr != nil {
			return teleboxtelegram.Message{}, fmt.Errorf("select first bot result: %w", err)
		}
	}
	return c.waitBotMedia(
		runCtx,
		bot,
		time.Unix(int64(buttonMessage.Date), 0).Add(-time.Second),
	)
}

func (c *Client) waitBotButton(
	ctx context.Context,
	bot gotdpeers.User,
	startedAt time.Time,
) (*tg.Message, error) {
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()
	for {
		message, err := c.latestMatchingBotMessage(ctx, bot, startedAt, func(raw *tg.Message) bool {
			_, ok := raw.GetReplyMarkup()
			return ok
		})
		if err != nil {
			return nil, err
		}
		if message != nil {
			return message, nil
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("music bot did not return selectable results before timeout")
		case <-ticker.C:
		}
	}
}

func (c *Client) waitBotMedia(
	ctx context.Context,
	bot gotdpeers.User,
	startedAt time.Time,
) (teleboxtelegram.Message, error) {
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()
	for {
		message, err := c.latestMatchingBotMessage(ctx, bot, startedAt, func(raw *tg.Message) bool {
			return mediaMetadataFromMessage(raw) != nil
		})
		if err != nil {
			return teleboxtelegram.Message{}, err
		}
		if message != nil {
			c.mu.RLock()
			selfID := c.selfID
			c.mu.RUnlock()
			return stableMessage(message, int64(bot.TDLibPeerID()), selfID), nil
		}
		select {
		case <-ctx.Done():
			return teleboxtelegram.Message{}, errors.New("music bot did not return media before timeout")
		case <-ticker.C:
		}
	}
}

func (c *Client) latestMatchingBotMessage(
	ctx context.Context,
	bot gotdpeers.User,
	startedAt time.Time,
	matches func(*tg.Message) bool,
) (*tg.Message, error) {
	result, err := c.raw.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  bot.InputPeer(),
		Limit: 10,
	})
	if err != nil {
		return nil, fmt.Errorf("read music bot history: %w", err)
	}
	modified, ok := result.AsModified()
	if !ok {
		return nil, nil
	}
	for _, class := range modified.GetMessages() {
		raw, ok := class.(*tg.Message)
		if !ok || raw.Out || time.Unix(int64(raw.Date), 0).Before(startedAt) {
			continue
		}
		if matches(raw) {
			return raw, nil
		}
	}
	return nil, nil
}

func (c *Client) pressFirstButton(
	ctx context.Context,
	bot gotdpeers.User,
	message *tg.Message,
) error {
	markup, ok := message.GetReplyMarkup()
	if !ok {
		return errors.New("bot response has no reply markup")
	}
	var rows []tg.KeyboardButtonRow
	switch value := markup.(type) {
	case *tg.ReplyInlineMarkup:
		rows = value.Rows
	case *tg.ReplyKeyboardMarkup:
		rows = value.Rows
	default:
		return errors.New("bot returned unsupported reply markup")
	}
	if len(rows) == 0 || len(rows[0].Buttons) == 0 {
		return errors.New("bot returned an empty keyboard")
	}
	button := rows[0].Buttons[0]
	if callback, ok := button.(*tg.KeyboardButtonCallback); ok {
		_, err := c.raw.API().MessagesGetBotCallbackAnswer(
			ctx,
			&tg.MessagesGetBotCallbackAnswerRequest{
				Peer:  bot.InputPeer(),
				MsgID: message.ID,
				Data:  append([]byte(nil), callback.Data...),
			},
		)
		return err
	}
	text := strings.TrimSpace(button.GetText())
	if text == "" {
		return errors.New("first bot button has no selectable text")
	}
	_, err := c.SendText(ctx, int64(bot.TDLibPeerID()), text)
	return err
}
