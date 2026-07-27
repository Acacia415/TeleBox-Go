package gotd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (c *Client) GetHistory(
	ctx context.Context,
	request teleboxtelegram.HistoryQuery,
) ([]teleboxtelegram.Message, error) {
	if request.ChatID == 0 {
		return nil, errors.New("history chat ID is required")
	}
	if request.Limit <= 0 {
		request.Limit = 100
	}
	if request.Limit > 100 {
		request.Limit = 100
	}
	peer, err := c.resolveInputPeer(ctx, request.ChatID)
	if err != nil {
		return nil, err
	}

	var result tg.MessagesMessagesClass
	if strings.TrimSpace(request.Search) != "" || request.MediaKind != "" ||
		request.FromUserID != 0 {
		search := &tg.MessagesSearchRequest{
			Peer:     peer,
			Q:        request.Search,
			Filter:   historyFilter(request.MediaKind),
			OffsetID: request.OffsetID,
			Limit:    request.Limit,
			MaxID:    request.MaxID,
			MinID:    request.MinID,
		}
		if request.FromUserID != 0 {
			user, resolveErr := c.peers.ResolveUserID(ctx, request.FromUserID)
			if resolveErr != nil {
				return nil, fmt.Errorf("resolve history sender: %w", resolveErr)
			}
			search.SetFromID(user.InputPeer())
		}
		result, err = c.raw.API().MessagesSearch(ctx, search)
	} else {
		result, err = c.raw.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:      peer,
			OffsetID:  request.OffsetID,
			AddOffset: request.AddOffset,
			Limit:     request.Limit,
			MaxID:     request.MaxID,
			MinID:     request.MinID,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("get history: %w", err)
	}
	modified, ok := result.AsModified()
	if !ok {
		return nil, nil
	}
	return stableHistory(modified.GetMessages(), request.ChatID, c.selfID), nil
}

func stableHistory(
	rawMessages []tg.MessageClass,
	chatID, selfID int64,
) []teleboxtelegram.Message {
	result := make([]teleboxtelegram.Message, 0, len(rawMessages))
	for _, item := range rawMessages {
		raw, ok := item.(*tg.Message)
		if !ok {
			continue
		}
		result = append(result, stableMessage(raw, chatID, selfID))
	}
	return result
}

func historyFilter(kind teleboxtelegram.MediaKind) tg.MessagesFilterClass {
	switch kind {
	case teleboxtelegram.MediaVideo, teleboxtelegram.MediaAnimation,
		teleboxtelegram.MediaVideoNote:
		return &tg.InputMessagesFilterVideo{}
	case teleboxtelegram.MediaPhoto:
		return &tg.InputMessagesFilterPhotos{}
	case teleboxtelegram.MediaAudio:
		return &tg.InputMessagesFilterMusic{}
	case teleboxtelegram.MediaVoice:
		return &tg.InputMessagesFilterVoice{}
	case teleboxtelegram.MediaDocument:
		return &tg.InputMessagesFilterDocument{}
	default:
		return &tg.InputMessagesFilterEmpty{}
	}
}
