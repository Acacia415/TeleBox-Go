package gotd

import (
	"context"
	"strings"

	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

// ResolveCustomEmoji resolves Telegram custom emoji documents to the regular
// Unicode emoji declared in documentAttributeCustomEmoji.alt. Empty fallbacks
// are retained in the cache so malformed or deleted documents are not fetched
// on every plugin write.
func (c *Client) ResolveCustomEmoji(
	ctx context.Context,
	documentIDs []int64,
) ([]teleboxtelegram.CustomEmoji, error) {
	ids := uniquePositiveIDs(documentIDs)
	if len(ids) == 0 {
		return nil, nil
	}

	resolved := make(map[int64]string, len(ids))
	missing := make([]int64, 0, len(ids))
	c.mu.RLock()
	for _, id := range ids {
		if emoji, exists := c.customEmojiCache[id]; exists {
			resolved[id] = emoji
		} else {
			missing = append(missing, id)
		}
	}
	c.mu.RUnlock()

	if len(missing) > 0 {
		documents, err := c.raw.API().MessagesGetCustomEmojiDocuments(ctx, missing)
		if err != nil {
			return nil, err
		}
		fetched := customEmojiFallbacks(documents)
		c.mu.Lock()
		if c.customEmojiCache == nil {
			c.customEmojiCache = make(map[int64]string)
		}
		for _, id := range missing {
			emoji := fetched[id]
			c.customEmojiCache[id] = emoji
			resolved[id] = emoji
		}
		c.mu.Unlock()
	}

	result := make([]teleboxtelegram.CustomEmoji, 0, len(ids))
	for _, id := range ids {
		result = append(result, teleboxtelegram.CustomEmoji{
			DocumentID: id,
			Emoji:      resolved[id],
		})
	}
	return result, nil
}

func customEmojiFallbacks(documents []tg.DocumentClass) map[int64]string {
	result := make(map[int64]string, len(documents))
	for _, class := range documents {
		document, ok := class.(*tg.Document)
		if !ok {
			continue
		}
		for _, attribute := range document.Attributes {
			custom, ok := attribute.(*tg.DocumentAttributeCustomEmoji)
			if !ok {
				continue
			}
			result[document.ID] = strings.TrimSpace(custom.Alt)
			break
		}
	}
	return result
}

func uniquePositiveIDs(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
