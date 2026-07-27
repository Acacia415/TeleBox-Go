package telegram

import (
	"context"
	"fmt"
	"io"
	"time"
)

type UnavailableClient struct {
	reason string
}

func NewUnavailableClient(reason string) *UnavailableClient {
	return &UnavailableClient{reason: reason}
}

func (c *UnavailableClient) Run(context.Context, MessageHandler) error {
	return fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) SendText(context.Context, int64, string) (SentMessage, error) {
	return SentMessage{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) ReplyText(context.Context, int64, int, string) (SentMessage, error) {
	return SentMessage{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) EditText(context.Context, int64, int, string) (SentMessage, error) {
	return SentMessage{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) DeleteMessages(context.Context, int64, []int) error {
	return fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) ForwardMessages(context.Context, int64, int64, []int) error {
	return fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) CopyMessages(context.Context, int64, int64, []int) error {
	return fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) SendFile(context.Context, int64, Upload) (SentMessage, error) {
	return SentMessage{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) GetMessages(context.Context, int64, []int) ([]Message, error) {
	return nil, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) GetHistory(context.Context, HistoryQuery) ([]Message, error) {
	return nil, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) DownloadMedia(context.Context, int64, int, io.Writer) (Media, error) {
	return Media{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) DownloadProfilePhoto(context.Context, int64, io.Writer) error {
	return fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) ResolveUser(context.Context, string) (User, error) {
	return User{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) ResolveChat(context.Context, int64) (Chat, error) {
	return Chat{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) ResolveChatTarget(context.Context, string) (Chat, error) {
	return Chat{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) GetMyPermissions(context.Context, int64) (ChatPermissions, error) {
	return ChatPermissions{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) GetChatMember(context.Context, int64, int64) (ChatMember, error) {
	return ChatMember{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) FindJoinTime(
	context.Context,
	int64,
	int64,
	int,
	JoinSearchProgress,
) (time.Time, int, error) {
	return time.Time{}, 0, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) ListManagedChats(context.Context, int) ([]Chat, error) {
	return nil, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) ListChats(context.Context, int) ([]Chat, error) {
	return nil, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) JoinChat(context.Context, string) error {
	return fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) ModerateUser(context.Context, ModerationRequest) error {
	return fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) DeleteUserHistory(context.Context, int64, int64) error {
	return fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) SendReaction(context.Context, int64, int, []Reaction, bool) error {
	return fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) GetStickerSet(context.Context, string) (StickerSet, error) {
	return StickerSet{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) CreateStickerSet(
	context.Context,
	int64,
	string,
	string,
	Sticker,
) error {
	return fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) AddStickerToSet(context.Context, string, Sticker) error {
	return fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}

func (c *UnavailableClient) RequestBotMedia(context.Context, BotMediaRequest) (Message, error) {
	return Message{}, fmt.Errorf("%w: %s", ErrTransportUnavailable, c.reason)
}
