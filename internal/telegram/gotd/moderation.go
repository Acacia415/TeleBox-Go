package gotd

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/constant"
	gotdpeers "github.com/gotd/td/telegram/peers"
	gotdquery "github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (c *Client) GetMyPermissions(
	ctx context.Context,
	chatID int64,
) (teleboxtelegram.ChatPermissions, error) {
	resolved, err := c.peers.ResolveTDLibID(ctx, constant.TDLibPeerID(chatID))
	if err != nil {
		return teleboxtelegram.ChatPermissions{}, fmt.Errorf("resolve chat permissions: %w", err)
	}
	return peerPermissions(resolved), nil
}

func (c *Client) ListManagedChats(
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
		permissions := peerPermissions(resolved)
		if !permissions.BanUsers && !permissions.DeleteMessages {
			continue
		}
		chatID := int64(resolved.TDLibPeerID())
		switch value := resolved.(type) {
		case gotdpeers.Chat:
			result = append(result, portableBasicChat(value.Raw(), chatID))
		case gotdpeers.Channel:
			result = append(result, portableChannel(value.Raw(), chatID))
		}
	}
	if err := iterator.Err(); err != nil {
		return nil, fmt.Errorf("list managed chats: %w", err)
	}
	return result, nil
}

func (c *Client) ModerateUser(
	ctx context.Context,
	request teleboxtelegram.ModerationRequest,
) error {
	if request.ChatID == 0 || request.UserID <= 0 {
		return errors.New("moderation chat and user IDs are required")
	}
	channel, err := c.resolveChannel(ctx, request.ChatID)
	if err != nil {
		return err
	}
	user, err := c.peers.ResolveUserID(ctx, request.UserID)
	if err != nil {
		return fmt.Errorf("resolve moderation user: %w", err)
	}

	switch request.Action {
	case teleboxtelegram.ModerationKick:
		if err := c.editBanned(ctx, channel, user.InputPeer(), bannedRights(request)); err != nil {
			return err
		}
		request.Action = teleboxtelegram.ModerationUnban
		return c.editBanned(ctx, channel, user.InputPeer(), bannedRights(request))
	case teleboxtelegram.ModerationBan, teleboxtelegram.ModerationUnban,
		teleboxtelegram.ModerationMute, teleboxtelegram.ModerationUnmute:
		return c.editBanned(ctx, channel, user.InputPeer(), bannedRights(request))
	default:
		return fmt.Errorf("unsupported moderation action %q", request.Action)
	}
}

func (c *Client) DeleteUserHistory(ctx context.Context, chatID, userID int64) error {
	channel, err := c.resolveChannel(ctx, chatID)
	if err != nil {
		return err
	}
	user, err := c.peers.ResolveUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve history user: %w", err)
	}
	if _, err := c.raw.API().ChannelsDeleteParticipantHistory(
		ctx,
		&tg.ChannelsDeleteParticipantHistoryRequest{
			Channel:     channel.InputChannel(),
			Participant: user.InputPeer(),
		},
	); err != nil {
		return fmt.Errorf("delete participant history: %w", err)
	}
	return nil
}

func (c *Client) resolveChannel(
	ctx context.Context,
	chatID int64,
) (gotdpeers.Channel, error) {
	resolved, err := c.peers.ResolveTDLibID(ctx, constant.TDLibPeerID(chatID))
	if err != nil {
		return gotdpeers.Channel{}, fmt.Errorf("resolve moderation chat: %w", err)
	}
	channel, ok := resolved.(gotdpeers.Channel)
	if !ok {
		return gotdpeers.Channel{}, errors.New(
			"该管理操作只支持超级群组或频道，基础群组请先升级",
		)
	}
	return channel, nil
}

func (c *Client) editBanned(
	ctx context.Context,
	channel gotdpeers.Channel,
	user tg.InputPeerClass,
	rights tg.ChatBannedRights,
) error {
	if _, err := c.raw.API().ChannelsEditBanned(ctx, &tg.ChannelsEditBannedRequest{
		Channel:      channel.InputChannel(),
		Participant:  user,
		BannedRights: rights,
	}); err != nil {
		return fmt.Errorf("edit banned rights: %w", err)
	}
	return nil
}

func peerPermissions(peer gotdpeers.Peer) teleboxtelegram.ChatPermissions {
	switch value := peer.(type) {
	case gotdpeers.User:
		return teleboxtelegram.ChatPermissions{DeleteMessages: true}
	case gotdpeers.Chat:
		raw := value.Raw()
		if raw.Creator {
			return teleboxtelegram.ChatPermissions{DeleteMessages: true, BanUsers: true}
		}
		if rights, ok := raw.GetAdminRights(); ok {
			return teleboxtelegram.ChatPermissions{
				DeleteMessages: rights.DeleteMessages,
				BanUsers:       rights.BanUsers,
			}
		}
	case gotdpeers.Channel:
		raw := value.Raw()
		if raw.Creator {
			return teleboxtelegram.ChatPermissions{DeleteMessages: true, BanUsers: true}
		}
		if rights, ok := raw.GetAdminRights(); ok {
			return teleboxtelegram.ChatPermissions{
				DeleteMessages: rights.DeleteMessages,
				BanUsers:       rights.BanUsers,
			}
		}
	}
	return teleboxtelegram.ChatPermissions{}
}

func bannedRights(request teleboxtelegram.ModerationRequest) tg.ChatBannedRights {
	untilDate := 0
	if !request.Until.IsZero() {
		untilDate = int(request.Until.Unix())
	}
	switch request.Action {
	case teleboxtelegram.ModerationBan, teleboxtelegram.ModerationKick:
		return tg.ChatBannedRights{
			ViewMessages: true,
			SendMessages: true,
			SendMedia:    true,
			SendStickers: true,
			SendGifs:     true,
			SendGames:    true,
			SendInline:   true,
			EmbedLinks:   true,
			UntilDate:    untilDate,
		}
	case teleboxtelegram.ModerationMute:
		return tg.ChatBannedRights{
			SendMessages: true,
			SendMedia:    true,
			SendStickers: true,
			SendGifs:     true,
			SendGames:    true,
			SendInline:   true,
			EmbedLinks:   true,
			UntilDate:    untilDate,
		}
	default:
		return tg.ChatBannedRights{}
	}
}
