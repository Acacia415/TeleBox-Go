package telegram

import (
	"context"
	"fmt"
)

// CustomEmoji contains the portable fallback text for a Telegram custom emoji.
// Emoji is the regular Unicode emoji declared by Telegram for DocumentID.
type CustomEmoji struct {
	DocumentID int64  `json:"document_id"`
	Emoji      string `json:"emoji"`
}

// CustomEmojiResolver is an optional transport extension. Keeping it separate
// from Client avoids forcing test doubles and transports without MTProto custom
// emoji support to implement it.
type CustomEmojiResolver interface {
	ResolveCustomEmoji(context.Context, []int64) ([]CustomEmoji, error)
}

// ResolveCustomEmoji returns portable Unicode fallbacks when the active
// transport supports custom emoji lookup.
func ResolveCustomEmoji(
	ctx context.Context,
	client Client,
	documentIDs []int64,
) ([]CustomEmoji, error) {
	resolver, ok := client.(CustomEmojiResolver)
	if !ok {
		return nil, fmt.Errorf("%w: custom emoji lookup is unsupported", ErrTransportUnavailable)
	}
	return resolver.ResolveCustomEmoji(ctx, documentIDs)
}
