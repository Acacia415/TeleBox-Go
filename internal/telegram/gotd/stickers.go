package gotd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (c *Client) GetStickerSet(
	ctx context.Context,
	shortName string,
) (teleboxtelegram.StickerSet, error) {
	shortName = strings.TrimSpace(shortName)
	if shortName == "" {
		return teleboxtelegram.StickerSet{}, errors.New("sticker set name is required")
	}
	result, err := c.raw.API().MessagesGetStickerSet(ctx, &tg.MessagesGetStickerSetRequest{
		Stickerset: &tg.InputStickerSetShortName{ShortName: shortName},
		Hash:       0,
	})
	if err != nil {
		if _, ok := tgerr.AsType(err, "STICKERSET_INVALID"); ok {
			return teleboxtelegram.StickerSet{}, teleboxtelegram.ErrStickerSetNotFound
		}
		return teleboxtelegram.StickerSet{}, fmt.Errorf("get sticker set: %w", err)
	}
	modified, ok := result.AsModified()
	if !ok {
		return teleboxtelegram.StickerSet{}, errors.New("sticker set response was not modified")
	}
	return teleboxtelegram.StickerSet{
		ShortName: modified.Set.ShortName,
		Title:     modified.Set.Title,
		Count:     modified.Set.Count,
	}, nil
}

func (c *Client) CreateStickerSet(
	ctx context.Context,
	userID int64,
	title string,
	shortName string,
	sticker teleboxtelegram.Sticker,
) error {
	if userID <= 0 {
		return errors.New("sticker set owner ID is required")
	}
	if strings.TrimSpace(title) == "" || strings.TrimSpace(shortName) == "" {
		return errors.New("sticker set title and short name are required")
	}
	owner, err := c.peers.ResolveUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve sticker set owner: %w", err)
	}
	_, err = c.raw.API().StickersCreateStickerSet(ctx, &tg.StickersCreateStickerSetRequest{
		UserID:    owner.InputUser(),
		Title:     title,
		ShortName: shortName,
		Stickers:  []tg.InputStickerSetItem{inputSticker(sticker)},
		Software:  "TeleBox-Go",
	})
	if err != nil {
		return fmt.Errorf("create sticker set: %w", err)
	}
	return nil
}

func (c *Client) AddStickerToSet(
	ctx context.Context,
	shortName string,
	sticker teleboxtelegram.Sticker,
) error {
	if strings.TrimSpace(shortName) == "" {
		return errors.New("sticker set name is required")
	}
	_, err := c.raw.API().StickersAddStickerToSet(ctx, &tg.StickersAddStickerToSetRequest{
		Stickerset: &tg.InputStickerSetShortName{ShortName: shortName},
		Sticker:    inputSticker(sticker),
	})
	if err != nil {
		return fmt.Errorf("add sticker to set: %w", err)
	}
	return nil
}

func inputSticker(sticker teleboxtelegram.Sticker) tg.InputStickerSetItem {
	emoji := strings.TrimSpace(sticker.Emoji)
	if emoji == "" {
		emoji = "😀"
	}
	return tg.InputStickerSetItem{
		Document: &tg.InputDocument{
			ID:            sticker.DocumentID,
			AccessHash:    sticker.AccessHash,
			FileReference: append([]byte(nil), sticker.FileReference...),
		},
		Emoji: emoji,
	}
}
