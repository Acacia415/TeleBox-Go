package gotd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	gotdpeers "github.com/gotd/td/telegram/peers"
	gotdquery "github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (c *Client) ListChats(
	ctx context.Context,
	limit int,
) ([]teleboxtelegram.Chat, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	iterator := gotdquery.GetDialogs(c.raw.API()).BatchSize(100).Iter()
	result := make([]teleboxtelegram.Chat, 0, limit)
	for len(result) < limit && iterator.Next(ctx) {
		element := iterator.Value()
		resolved, err := c.peers.FromInputPeer(ctx, element.Peer)
		if err != nil {
			continue
		}
		chatID := int64(resolved.TDLibPeerID())
		c.mu.Lock()
		c.peerCache[chatID] = resolved.InputPeer()
		c.mu.Unlock()
		switch value := resolved.(type) {
		case gotdpeers.User:
			raw := value.Raw()
			photoDC := 0
			if photo, ok := raw.GetPhoto(); ok {
				if concrete, ok := photo.(*tg.UserProfilePhoto); ok {
					photoDC = concrete.DCID
				}
			}
			result = append(result, teleboxtelegram.Chat{
				ID:       chatID,
				Title:    value.VisibleName(),
				Username: raw.Username,
				Kind:     teleboxtelegram.ChatPrivate,
				PhotoDC:  photoDC,
				Verified: raw.Verified,
				Scam:     raw.Scam,
				Fake:     raw.Fake,
			})
		case gotdpeers.Chat:
			result = append(result, portableBasicChat(value.Raw(), chatID))
		case gotdpeers.Channel:
			result = append(result, portableChannel(value.Raw(), chatID))
		}
	}
	if err := iterator.Err(); err != nil {
		return nil, fmt.Errorf("list dialogs: %w", err)
	}
	return result, nil
}

func (c *Client) JoinChat(ctx context.Context, target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return errors.New("join target is required")
	}
	if hash := inviteHash(target); hash != "" {
		if _, err := c.raw.API().MessagesImportChatInvite(ctx, hash); err != nil &&
			!strings.Contains(strings.ToUpper(err.Error()), "USER_ALREADY_PARTICIPANT") {
			return fmt.Errorf("import chat invite: %w", err)
		}
		return nil
	}
	resolved, err := c.peers.Resolve(ctx, normalizePublicTarget(target))
	if err != nil {
		return fmt.Errorf("resolve join target: %w", err)
	}
	channel, ok := resolved.(gotdpeers.Channel)
	if !ok {
		return errors.New("join target is not a channel or supergroup")
	}
	if _, err := c.raw.API().ChannelsJoinChannel(
		ctx,
		channel.InputChannel(),
	); err != nil &&
		!strings.Contains(strings.ToUpper(err.Error()), "USER_ALREADY_PARTICIPANT") {
		return fmt.Errorf("join channel: %w", err)
	}
	return nil
}

func inviteHash(target string) string {
	value := strings.TrimSpace(target)
	parsed, err := url.Parse(value)
	if err == nil && (strings.EqualFold(parsed.Hostname(), "t.me") ||
		strings.EqualFold(parsed.Hostname(), "telegram.me")) {
		path := strings.Trim(parsed.EscapedPath(), "/")
		switch {
		case strings.HasPrefix(path, "+"):
			return strings.TrimPrefix(path, "+")
		case strings.HasPrefix(path, "joinchat/"):
			return strings.TrimPrefix(path, "joinchat/")
		}
	}
	if strings.HasPrefix(value, "+") {
		return strings.TrimPrefix(value, "+")
	}
	return ""
}

func normalizePublicTarget(target string) string {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err == nil && (strings.EqualFold(parsed.Hostname(), "t.me") ||
		strings.EqualFold(parsed.Hostname(), "telegram.me")) {
		return "@" + strings.Trim(strings.TrimPrefix(parsed.Path, "/"), "/")
	}
	if !strings.HasPrefix(target, "@") {
		return "@" + target
	}
	return target
}
