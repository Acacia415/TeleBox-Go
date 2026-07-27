package gotd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/constant"
	gotdpeers "github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (c *Client) ResolveUser(ctx context.Context, target string) (teleboxtelegram.User, error) {
	c.mu.RLock()
	ready := c.sender != nil
	c.mu.RUnlock()
	if !ready {
		return teleboxtelegram.User{}, teleboxtelegram.ErrTransportUnavailable
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return teleboxtelegram.User{}, errors.New("user target is required")
	}

	var resolved gotdpeers.User
	if strings.EqualFold(target, "me") || strings.EqualFold(target, "self") {
		var err error
		resolved, err = c.peers.Self(ctx)
		if err != nil {
			return teleboxtelegram.User{}, fmt.Errorf("resolve current user: %w", err)
		}
	} else if userID, err := strconv.ParseInt(target, 10, 64); err == nil {
		if userID <= 0 {
			return teleboxtelegram.User{}, errors.New("user ID must be greater than zero")
		}
		resolved, err = c.peers.ResolveUserID(ctx, userID)
		if err != nil {
			return teleboxtelegram.User{}, fmt.Errorf("resolve user ID %d: %w", userID, err)
		}
	} else {
		peer, err := c.peers.Resolve(ctx, target)
		if err != nil {
			return teleboxtelegram.User{}, fmt.Errorf("resolve user %q: %w", target, err)
		}
		var ok bool
		resolved, ok = peer.(gotdpeers.User)
		if !ok {
			return teleboxtelegram.User{}, fmt.Errorf("resolved peer %q is not a user", target)
		}
	}

	raw := resolved.Raw()
	c.mu.Lock()
	c.peerCache[int64(resolved.TDLibPeerID())] = resolved.InputPeer()
	c.mu.Unlock()
	result := portableUser(raw)
	full, err := c.raw.API().UsersGetFullUser(ctx, resolved.InputUser())
	if err == nil {
		if about, ok := full.FullUser.GetAbout(); ok {
			result.Bio = about
		}
		result.CommonChats = full.FullUser.CommonChatsCount
	}
	return result, nil
}

func (c *Client) ResolveChat(ctx context.Context, chatID int64) (teleboxtelegram.Chat, error) {
	c.mu.RLock()
	ready := c.sender != nil
	c.mu.RUnlock()
	if !ready {
		return teleboxtelegram.Chat{}, teleboxtelegram.ErrTransportUnavailable
	}
	resolved, err := c.peers.ResolveTDLibID(ctx, constant.TDLibPeerID(chatID))
	if err != nil {
		return teleboxtelegram.Chat{}, fmt.Errorf("resolve chat %d: %w", chatID, err)
	}
	c.mu.Lock()
	c.peerCache[chatID] = resolved.InputPeer()
	c.mu.Unlock()

	switch value := resolved.(type) {
	case gotdpeers.Chat:
		return portableBasicChat(value.Raw(), chatID), nil
	case gotdpeers.Channel:
		return portableChannel(value.Raw(), chatID), nil
	default:
		return teleboxtelegram.Chat{}, fmt.Errorf("peer %d is not a group or channel", chatID)
	}
}

func (c *Client) ResolveChatTarget(
	ctx context.Context,
	target string,
) (teleboxtelegram.Chat, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return teleboxtelegram.Chat{}, errors.New("chat target is required")
	}
	if chatID, err := strconv.ParseInt(target, 10, 64); err == nil {
		return c.ResolveChat(ctx, chatID)
	}
	resolved, err := c.peers.Resolve(ctx, target)
	if err != nil {
		return teleboxtelegram.Chat{}, fmt.Errorf("resolve chat %q: %w", target, err)
	}
	chatID := int64(resolved.TDLibPeerID())
	c.mu.Lock()
	c.peerCache[chatID] = resolved.InputPeer()
	c.mu.Unlock()
	switch value := resolved.(type) {
	case gotdpeers.Chat:
		return portableBasicChat(value.Raw(), chatID), nil
	case gotdpeers.Channel:
		return portableChannel(value.Raw(), chatID), nil
	default:
		return teleboxtelegram.Chat{}, fmt.Errorf("peer %q is not a group or channel", target)
	}
}

func portableBasicChat(raw *tg.Chat, chatID int64) teleboxtelegram.Chat {
	result := teleboxtelegram.Chat{
		ID:          chatID,
		Title:       raw.Title,
		Kind:        teleboxtelegram.ChatGroup,
		MemberCount: raw.ParticipantsCount,
		CreatedAt:   time.Unix(int64(raw.Date), 0),
	}
	if photo, ok := raw.Photo.(*tg.ChatPhoto); ok {
		result.PhotoDC = photo.DCID
	}
	return result
}

func portableChannel(raw *tg.Channel, chatID int64) teleboxtelegram.Chat {
	kind := teleboxtelegram.ChatSupergroup
	if raw.Broadcast {
		kind = teleboxtelegram.ChatChannel
	}
	result := teleboxtelegram.Chat{
		ID:        chatID,
		Title:     raw.Title,
		Username:  raw.Username,
		Kind:      kind,
		CreatedAt: time.Unix(int64(raw.Date), 0),
		Verified:  raw.Verified,
		Scam:      raw.Scam,
		Fake:      raw.Fake,
		Forum:     raw.Forum,
	}
	if count, ok := raw.GetParticipantsCount(); ok {
		result.MemberCount = count
	}
	if photo, ok := raw.Photo.(*tg.ChatPhoto); ok {
		result.PhotoDC = photo.DCID
	}
	return result
}

func portableUser(raw *tg.User) teleboxtelegram.User {
	result := teleboxtelegram.User{
		ID:        raw.ID,
		FirstName: raw.FirstName,
		LastName:  raw.LastName,
		Username:  raw.Username,
		Phone:     raw.Phone,
		Deleted:   raw.Deleted,
		Bot:       raw.Bot,
		Premium:   raw.Premium,
		Verified:  raw.Verified,
		Scam:      raw.Scam,
		Fake:      raw.Fake,
		Presence:  teleboxtelegram.PresenceUnknown,
	}
	if photo, ok := raw.Photo.(*tg.UserProfilePhoto); ok {
		result.PhotoDC = photo.DCID
	}
	status, ok := raw.GetStatus()
	if !ok {
		return result
	}
	switch value := status.(type) {
	case *tg.UserStatusOnline:
		result.Presence = teleboxtelegram.PresenceOnline
	case *tg.UserStatusRecently:
		result.Presence = teleboxtelegram.PresenceRecently
	case *tg.UserStatusOffline:
		result.Presence = teleboxtelegram.PresenceOffline
		result.LastSeen = time.Unix(int64(value.WasOnline), 0)
	case *tg.UserStatusLastWeek:
		result.Presence = teleboxtelegram.PresenceLastWeek
	case *tg.UserStatusLastMonth:
		result.Presence = teleboxtelegram.PresenceLastMonth
	}
	return result
}
