package gotd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gotd/td/constant"
	gotdpeers "github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func (c *Client) GetChatMember(
	ctx context.Context,
	chatID, userID int64,
) (teleboxtelegram.ChatMember, error) {
	if userID <= 0 {
		return teleboxtelegram.ChatMember{}, errors.New("member user ID must be positive")
	}
	resolved, err := c.peers.ResolveTDLibID(ctx, constant.TDLibPeerID(chatID))
	if err != nil {
		return teleboxtelegram.ChatMember{}, fmt.Errorf("resolve member chat: %w", err)
	}
	user, err := c.peers.ResolveUserID(ctx, userID)
	if err != nil {
		return teleboxtelegram.ChatMember{}, fmt.Errorf("resolve member user: %w", err)
	}

	switch chat := resolved.(type) {
	case gotdpeers.Channel:
		response, err := c.raw.API().ChannelsGetParticipant(
			ctx,
			&tg.ChannelsGetParticipantRequest{
				Channel:     chat.InputChannel(),
				Participant: user.InputPeer(),
			},
		)
		if err != nil {
			return teleboxtelegram.ChatMember{}, fmt.Errorf("get channel participant: %w", err)
		}
		return portableChannelMember(response.Participant, chat.Raw().Date)
	case gotdpeers.Chat:
		response, err := c.raw.API().MessagesGetFullChat(ctx, chat.ID())
		if err != nil {
			return teleboxtelegram.ChatMember{}, fmt.Errorf("get basic chat participants: %w", err)
		}
		full, ok := response.FullChat.(*tg.ChatFull)
		if !ok {
			return teleboxtelegram.ChatMember{}, errors.New("basic chat full info is unavailable")
		}
		participants, ok := full.Participants.(*tg.ChatParticipants)
		if !ok {
			return teleboxtelegram.ChatMember{}, errors.New("basic chat participants are unavailable")
		}
		for _, participant := range participants.Participants {
			member, ok := portableBasicMember(participant, chat.Raw().Date)
			if ok && member.UserID == userID {
				return member, nil
			}
		}
		return teleboxtelegram.ChatMember{}, errors.New("user is not a chat member")
	default:
		return teleboxtelegram.ChatMember{}, errors.New("join time is only available in groups")
	}
}

func (c *Client) FindJoinTime(
	ctx context.Context,
	chatID, userID int64,
	maxMessages int,
	progress teleboxtelegram.JoinSearchProgress,
) (time.Time, int, error) {
	if userID <= 0 {
		return time.Time{}, 0, errors.New("join search user ID must be positive")
	}
	if maxMessages <= 0 || maxMessages > 100000 {
		maxMessages = 100000
	}
	peer, err := c.resolveInputPeer(ctx, chatID)
	if err != nil {
		return time.Time{}, 0, err
	}
	var (
		offsetID int
		scanned  int
		found    int
		earliest time.Time
	)
	for scanned < maxMessages {
		limit := 100
		if remaining := maxMessages - scanned; remaining < limit {
			limit = remaining
		}
		response, err := c.raw.API().MessagesGetHistory(
			ctx,
			&tg.MessagesGetHistoryRequest{
				Peer:     peer,
				OffsetID: offsetID,
				Limit:    limit,
			},
		)
		if err != nil {
			return time.Time{}, found, fmt.Errorf("search join service messages: %w", err)
		}
		modified, ok := response.AsModified()
		if !ok {
			break
		}
		messages := modified.GetMessages()
		if len(messages) == 0 {
			break
		}
		for _, message := range messages {
			if service, ok := message.(*tg.MessageService); ok &&
				isJoinServiceMessage(service, userID) {
				joinedAt := time.Unix(int64(service.Date), 0)
				if earliest.IsZero() || joinedAt.Before(earliest) {
					earliest = joinedAt
				}
				found++
			}
		}
		scanned += len(messages)
		offsetID = messageClassID(messages[len(messages)-1])
		if progress != nil && scanned%1000 == 0 {
			if err := progress(scanned, found); err != nil {
				return time.Time{}, found, err
			}
		}
		if len(messages) < limit || offsetID <= 0 {
			break
		}
		if scanned%2000 == 0 {
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return time.Time{}, found, ctx.Err()
			}
		}
	}
	return earliest, found, nil
}

func portableChannelMember(
	participant tg.ChannelParticipantClass,
	chatDate int,
) (teleboxtelegram.ChatMember, error) {
	switch value := participant.(type) {
	case *tg.ChannelParticipant:
		return teleboxtelegram.ChatMember{
			UserID:   value.UserID,
			Role:     teleboxtelegram.MemberRoleMember,
			JoinedAt: time.Unix(int64(value.Date), 0),
		}, nil
	case *tg.ChannelParticipantAdmin:
		return teleboxtelegram.ChatMember{
			UserID:   value.UserID,
			Role:     teleboxtelegram.MemberRoleAdmin,
			JoinedAt: time.Unix(int64(value.Date), 0),
		}, nil
	case *tg.ChannelParticipantCreator:
		return teleboxtelegram.ChatMember{
			UserID:   value.UserID,
			Role:     teleboxtelegram.MemberRoleCreator,
			JoinedAt: time.Unix(int64(chatDate), 0),
		}, nil
	default:
		return teleboxtelegram.ChatMember{}, errors.New("unsupported channel participant type")
	}
}

func portableBasicMember(
	participant tg.ChatParticipantClass,
	chatDate int,
) (teleboxtelegram.ChatMember, bool) {
	switch value := participant.(type) {
	case *tg.ChatParticipant:
		return teleboxtelegram.ChatMember{
			UserID:   value.UserID,
			Role:     teleboxtelegram.MemberRoleMember,
			JoinedAt: time.Unix(int64(value.Date), 0),
		}, true
	case *tg.ChatParticipantAdmin:
		return teleboxtelegram.ChatMember{
			UserID:   value.UserID,
			Role:     teleboxtelegram.MemberRoleAdmin,
			JoinedAt: time.Unix(int64(value.Date), 0),
		}, true
	case *tg.ChatParticipantCreator:
		return teleboxtelegram.ChatMember{
			UserID:   value.UserID,
			Role:     teleboxtelegram.MemberRoleCreator,
			JoinedAt: time.Unix(int64(chatDate), 0),
		}, true
	default:
		return teleboxtelegram.ChatMember{}, false
	}
}

func isJoinServiceMessage(message *tg.MessageService, userID int64) bool {
	switch action := message.Action.(type) {
	case *tg.MessageActionChatAddUser:
		for _, addedUserID := range action.Users {
			if addedUserID == userID {
				return true
			}
		}
	case *tg.MessageActionChatJoinedByLink, *tg.MessageActionChatJoinedByRequest:
		if from, ok := message.GetFromID(); ok {
			if peer, ok := from.(*tg.PeerUser); ok {
				return peer.UserID == userID
			}
		}
	}
	return false
}

func messageClassID(message tg.MessageClass) int {
	switch value := message.(type) {
	case *tg.Message:
		return value.ID
	case *tg.MessageService:
		return value.ID
	case *tg.MessageEmpty:
		return value.ID
	default:
		return 0
	}
}
