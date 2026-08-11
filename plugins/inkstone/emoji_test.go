package inkstone

import (
	"context"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type customEmojiTestClient struct {
	telegram.Client
	values []telegram.CustomEmoji
	ids    []int64
}

func (c *customEmojiTestClient) ResolveCustomEmoji(
	_ context.Context,
	ids []int64,
) ([]telegram.CustomEmoji, error) {
	c.ids = append([]int64(nil), ids...)
	return c.values, nil
}

func TestResolveCustomEmojiEntities(t *testing.T) {
	t.Parallel()

	client := &customEmojiTestClient{values: []telegram.CustomEmoji{
		{DocumentID: 10, Emoji: "✅"},
		{DocumentID: 11, Emoji: "❌"},
	}}
	plugin := New(service.Container{Telegram: client})
	message := telegram.Message{Entities: []telegram.MessageEntity{
		{Type: "custom_emoji", DocumentID: 10},
		{Type: "custom_emoji", DocumentID: 10},
		{Type: "bold"},
		{Type: "custom_emoji", DocumentID: 11},
	}}

	got := plugin.resolveCustomEmojiEntities(context.Background(), message)
	if len(client.ids) != 2 || client.ids[0] != 10 || client.ids[1] != 11 {
		t.Fatalf("ResolveCustomEmoji() ids = %#v", client.ids)
	}
	if got.Entities[0].Emoji != "✅" || got.Entities[1].Emoji != "✅" ||
		got.Entities[3].Emoji != "❌" {
		t.Fatalf("resolved entities = %+v", got.Entities)
	}
}
