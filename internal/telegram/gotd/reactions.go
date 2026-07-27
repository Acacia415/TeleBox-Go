package gotd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (c *Client) SendReaction(
	ctx context.Context,
	chatID int64,
	messageID int,
	reactions []teleboxtelegram.Reaction,
	big bool,
) error {
	if messageID <= 0 {
		return errors.New("reaction message ID must be positive")
	}
	if len(reactions) == 0 {
		return errors.New("at least one reaction is required")
	}
	peer, err := c.resolveInputPeer(ctx, chatID)
	if err != nil {
		return err
	}
	raw := make([]tg.ReactionClass, 0, len(reactions))
	for _, reaction := range reactions {
		switch {
		case reaction.DocumentID > 0:
			raw = append(raw, &tg.ReactionCustomEmoji{DocumentID: reaction.DocumentID})
		case strings.TrimSpace(reaction.Emoji) != "":
			raw = append(raw, &tg.ReactionEmoji{Emoticon: reaction.Emoji})
		default:
			return errors.New("reaction emoji or document ID is required")
		}
	}
	if _, err := c.raw.API().MessagesSendReaction(ctx, &tg.MessagesSendReactionRequest{
		Big:         big,
		AddToRecent: true,
		Peer:        peer,
		MsgID:       messageID,
		Reaction:    raw,
	}); err != nil {
		return fmt.Errorf("send reaction: %w", err)
	}
	return nil
}
