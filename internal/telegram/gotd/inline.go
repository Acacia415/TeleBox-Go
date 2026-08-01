package gotd

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram/message/unpack"
	gotdpeers "github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (c *Client) SendInlineBotResult(
	ctx context.Context,
	request teleboxtelegram.InlineBotRequest,
) (teleboxtelegram.SentMessage, error) {
	if strings.TrimSpace(request.Bot) == "" {
		return teleboxtelegram.SentMessage{}, errors.New("inline bot is required")
	}
	if request.ChatID == 0 {
		return teleboxtelegram.SentMessage{}, errors.New("inline destination chat is required")
	}
	resolved, err := c.peers.Resolve(ctx, request.Bot)
	if err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("resolve inline bot: %w", err)
	}
	bot, ok := resolved.(gotdpeers.User)
	if !ok || !bot.Raw().Bot {
		return teleboxtelegram.SentMessage{}, errors.New("configured inline peer is not a bot")
	}
	peer, err := c.resolveInputPeer(ctx, request.ChatID)
	if err != nil {
		return teleboxtelegram.SentMessage{}, err
	}
	results, err := c.raw.API().MessagesGetInlineBotResults(
		ctx,
		&tg.MessagesGetInlineBotResultsRequest{
			Bot:   bot.InputUser(),
			Peer:  peer,
			Query: strings.TrimSpace(request.Query),
		},
	)
	if err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("request inline bot result: %w", err)
	}
	if len(results.Results) == 0 {
		return teleboxtelegram.SentMessage{}, errors.New("inline bot returned no results")
	}
	randomID, err := secureRandomID()
	if err != nil {
		return teleboxtelegram.SentMessage{}, err
	}
	send := &tg.MessagesSendInlineBotResultRequest{
		Silent:   request.Silent,
		Peer:     peer,
		RandomID: randomID,
		QueryID:  results.QueryID,
		ID:       results.Results[0].GetID(),
	}
	if request.ReplyToID > 0 {
		send.SetReplyTo(&tg.InputReplyToMessage{
			ReplyToMsgID: request.ReplyToID,
		})
	}
	updates, err := c.raw.API().MessagesSendInlineBotResult(ctx, send)
	messageID, err := unpack.MessageID(updates, err)
	if err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("send inline bot result: %w", err)
	}
	return teleboxtelegram.SentMessage{
		ChatID:    request.ChatID,
		MessageID: messageID,
	}, nil
}

func secureRandomID() (int64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, fmt.Errorf("generate random message ID: %w", err)
	}
	return int64(binary.LittleEndian.Uint64(raw[:])), nil
}
