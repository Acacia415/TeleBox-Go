package inkstone

import (
	"context"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (p *Plugin) resolveCustomEmojiEntities(
	ctx context.Context,
	message telegram.Message,
) telegram.Message {
	seen := make(map[int64]struct{})
	ids := make([]int64, 0, len(message.Entities))
	for _, entity := range message.Entities {
		if !strings.EqualFold(entity.Type, "custom_emoji") ||
			entity.DocumentID <= 0 || entity.Emoji != "" {
			continue
		}
		if _, exists := seen[entity.DocumentID]; exists {
			continue
		}
		seen[entity.DocumentID] = struct{}{}
		ids = append(ids, entity.DocumentID)
	}
	if len(ids) == 0 || p.services.Telegram == nil {
		return message
	}

	values, err := telegram.ResolveCustomEmoji(ctx, p.services.Telegram, ids)
	if err != nil {
		if p.services.Logger != nil {
			p.services.Logger.Warn(
				"Inkstone custom emoji lookup failed",
				"count", len(ids),
				"error", err,
			)
		}
		return message
	}
	fallbacks := make(map[int64]string, len(values))
	for _, value := range values {
		fallbacks[value.DocumentID] = value.Emoji
	}
	for index := range message.Entities {
		entity := &message.Entities[index]
		if strings.EqualFold(entity.Type, "custom_emoji") {
			entity.Emoji = fallbacks[entity.DocumentID]
		}
	}
	return message
}
