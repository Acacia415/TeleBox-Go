package gotd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (c *Client) BlockUser(ctx context.Context, userID int64) error {
	peer, err := c.resolvePrivateUser(ctx, userID)
	if err != nil {
		return err
	}
	if _, err := c.raw.API().ContactsBlock(ctx, &tg.ContactsBlockRequest{
		ID: peer,
	}); err != nil {
		return fmt.Errorf("block user: %w", err)
	}
	return nil
}

func (c *Client) UnblockUser(ctx context.Context, userID int64) error {
	peer, err := c.resolvePrivateUser(ctx, userID)
	if err != nil {
		return err
	}
	if _, err := c.raw.API().ContactsUnblock(ctx, &tg.ContactsUnblockRequest{
		ID: peer,
	}); err != nil {
		return fmt.Errorf("unblock user: %w", err)
	}
	return nil
}

func (c *Client) ReportSpam(ctx context.Context, userID int64) error {
	peer, err := c.resolvePrivateUser(ctx, userID)
	if err != nil {
		return err
	}
	if _, err := c.raw.API().MessagesReportSpam(ctx, peer); err != nil {
		return fmt.Errorf("report spam: %w", err)
	}
	return nil
}

func (c *Client) DeletePrivateHistory(ctx context.Context, userID int64) error {
	peer, err := c.resolvePrivateUser(ctx, userID)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 100; attempt++ {
		result, err := c.raw.API().MessagesDeleteHistory(
			ctx,
			&tg.MessagesDeleteHistoryRequest{
				Peer:  peer,
				MaxID: 0,
			},
		)
		if err != nil {
			return fmt.Errorf("delete private history: %w", err)
		}
		if result.Offset == 0 {
			return nil
		}
	}
	return errors.New("delete private history did not finish")
}

func (c *Client) GetPrivateChatSettings(
	ctx context.Context,
	userID int64,
) (teleboxtelegram.PrivateChatSettings, error) {
	peer, err := c.resolvePrivateUser(ctx, userID)
	if err != nil {
		return teleboxtelegram.PrivateChatSettings{}, err
	}
	result, err := c.raw.API().MessagesGetPeerSettings(ctx, peer)
	if err != nil {
		return teleboxtelegram.PrivateChatSettings{}, fmt.Errorf(
			"get private chat settings: %w",
			err,
		)
	}
	return teleboxtelegram.PrivateChatSettings{
		CanReportSpam: result.Settings.GetReportSpam(),
		AutoArchived:  result.Settings.GetAutoarchived(),
	}, nil
}

func (c *Client) SetPrivateChatQuarantined(
	ctx context.Context,
	userID int64,
	enabled bool,
) error {
	peer, err := c.resolvePrivateUser(ctx, userID)
	if err != nil {
		return err
	}
	folderID := 0
	muteUntil := 0
	if enabled {
		folderID = 1
		muteUntil = math.MaxInt32
	}
	if _, err := c.raw.API().FoldersEditPeerFolders(
		ctx,
		[]tg.InputFolderPeer{{Peer: peer, FolderID: folderID}},
	); err != nil {
		return fmt.Errorf("change private chat folder: %w", err)
	}
	settings := tg.InputPeerNotifySettings{}
	settings.SetMuteUntil(muteUntil)
	if _, err := c.raw.API().AccountUpdateNotifySettings(
		ctx,
		&tg.AccountUpdateNotifySettingsRequest{
			Peer:     &tg.InputNotifyPeer{Peer: peer},
			Settings: settings,
		},
	); err != nil {
		return fmt.Errorf("change private chat notifications: %w", err)
	}
	return nil
}

func (c *Client) GetGlobalAutoArchive(ctx context.Context) (bool, error) {
	settings, err := c.raw.API().AccountGetGlobalPrivacySettings(ctx)
	if err != nil {
		return false, fmt.Errorf("get global privacy settings: %w", err)
	}
	return settings.GetArchiveAndMuteNewNoncontactPeers(), nil
}

func (c *Client) SetGlobalAutoArchive(ctx context.Context, enabled bool) error {
	settings, err := c.raw.API().AccountGetGlobalPrivacySettings(ctx)
	if err != nil {
		return fmt.Errorf("get global privacy settings: %w", err)
	}
	settings.SetArchiveAndMuteNewNoncontactPeers(enabled)
	if _, err := c.raw.API().AccountSetGlobalPrivacySettings(ctx, *settings); err != nil {
		return fmt.Errorf("set global privacy settings: %w", err)
	}
	return nil
}

func (c *Client) UpdateAccountUsername(ctx context.Context, username string) error {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if _, err := c.raw.API().AccountUpdateUsername(ctx, username); err != nil {
		return fmt.Errorf("update account username: %w", err)
	}
	return nil
}

func (c *Client) CreateChannel(
	ctx context.Context,
	title string,
	about string,
) (teleboxtelegram.Chat, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return teleboxtelegram.Chat{}, errors.New("channel title is required")
	}
	updates, err := c.raw.API().ChannelsCreateChannel(
		ctx,
		&tg.ChannelsCreateChannelRequest{
			Broadcast: true,
			Title:     title,
			About:     strings.TrimSpace(about),
		},
	)
	if err != nil {
		return teleboxtelegram.Chat{}, fmt.Errorf("create channel: %w", err)
	}
	for _, class := range chatsFromUpdates(updates) {
		raw, ok := class.(*tg.Channel)
		if !ok {
			continue
		}
		var peerID constant.TDLibPeerID
		peerID.Channel(raw.ID)
		chatID := int64(peerID)
		c.mu.Lock()
		c.peerCache[chatID] = raw.AsInputPeer()
		c.mu.Unlock()
		result := portableChannel(raw, chatID)
		result.Description = strings.TrimSpace(about)
		return result, nil
	}
	return teleboxtelegram.Chat{}, errors.New("create channel returned no channel")
}

func (c *Client) UpdateChatUsername(
	ctx context.Context,
	chatID int64,
	username string,
) error {
	channel, err := c.resolveInputChannel(ctx, chatID)
	if err != nil {
		return err
	}
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if _, err := c.raw.API().ChannelsUpdateUsername(
		ctx,
		&tg.ChannelsUpdateUsernameRequest{
			Channel:  channel,
			Username: username,
		},
	); err != nil {
		return fmt.Errorf("update channel username: %w", err)
	}
	return nil
}

func (c *Client) DeleteChannel(ctx context.Context, chatID int64) error {
	channel, err := c.resolveInputChannel(ctx, chatID)
	if err != nil {
		return err
	}
	if _, err := c.raw.API().ChannelsDeleteChannel(ctx, channel); err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	return nil
}

func (c *Client) resolveInputChannel(
	ctx context.Context,
	chatID int64,
) (tg.InputChannelClass, error) {
	channel, err := c.resolveChannel(ctx, chatID)
	if err == nil {
		return channel.InputChannel(), nil
	}
	c.mu.RLock()
	cached := c.peerCache[chatID]
	c.mu.RUnlock()
	if peer, ok := cached.(*tg.InputPeerChannel); ok {
		return &tg.InputChannel{
			ChannelID:  peer.ChannelID,
			AccessHash: peer.AccessHash,
		}, nil
	}
	return nil, err
}

func (c *Client) resolvePrivateUser(
	ctx context.Context,
	userID int64,
) (tg.InputPeerClass, error) {
	if userID <= 0 {
		return nil, errors.New("user ID must be greater than zero")
	}
	c.mu.RLock()
	cached, ok := c.peerCache[userID]
	c.mu.RUnlock()
	if ok {
		return cached, nil
	}
	peer, err := c.resolveInputPeer(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("resolve private user: %w", err)
	}
	return peer, nil
}

func chatsFromUpdates(updates tg.UpdatesClass) []tg.ChatClass {
	switch value := updates.(type) {
	case *tg.Updates:
		return value.Chats
	case *tg.UpdatesCombined:
		return value.Chats
	default:
		return nil
	}
}
