package gotd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/session"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/message"
	messagehtml "github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/unpack"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"
	updatehook "github.com/gotd/td/telegram/updates/hook"
	"github.com/gotd/td/tg"

	"github.com/Acacia415/TeleBox-Go/internal/buildinfo"
	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type QRCode struct {
	URL       string
	ExpiresAt time.Time
}

type Config struct {
	APIID       int
	APIHash     string
	SessionFile string
	LoginMode   string
	OnQRCode    func(context.Context, QRCode) error
	PhoneAuth   auth.UserAuthenticator
}

type Client struct {
	config     Config
	raw        *gotdtelegram.Client
	dispatcher tg.UpdateDispatcher
	loggedIn   qrlogin.LoggedIn
	peers      *peers.Manager
	updates    *updates.Manager

	runMu sync.Mutex
	mu    sync.RWMutex

	handler          teleboxtelegram.MessageHandler
	sender           *message.Sender
	selfID           int64
	peerCache        map[int64]tg.InputPeerClass
	customEmojiCache map[int64]string
}

func New(cfg Config) (*Client, error) {
	if cfg.APIID <= 0 {
		return nil, errors.New("api ID must be greater than zero")
	}
	if strings.TrimSpace(cfg.APIHash) == "" {
		return nil, errors.New("api hash is required")
	}
	if strings.TrimSpace(cfg.SessionFile) == "" {
		return nil, errors.New("session file is required")
	}
	switch strings.ToLower(cfg.LoginMode) {
	case "existing", "qr":
	case "phone":
		if cfg.PhoneAuth == nil {
			return nil, errors.New("phone login requested but no phone authenticator is configured")
		}
	default:
		return nil, fmt.Errorf("unsupported login mode %q", cfg.LoginMode)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.SessionFile), 0o700); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}

	dispatcher := tg.NewUpdateDispatcher()
	client := &Client{
		config:           cfg,
		dispatcher:       dispatcher,
		peerCache:        make(map[int64]tg.InputPeerClass),
		customEmojiCache: make(map[int64]string),
	}
	client.loggedIn = qrlogin.OnLoginToken(dispatcher)
	dispatcher.OnNewMessage(client.onNewMessage)
	dispatcher.OnNewChannelMessage(client.onNewChannelMessage)
	dispatcher.OnEditMessage(client.onEditMessage)
	dispatcher.OnEditChannelMessage(client.onEditChannelMessage)

	var updateHandler gotdtelegram.UpdateHandler
	client.updates = updates.New(updates.Config{Handler: dispatcher})
	client.raw = gotdtelegram.NewClient(cfg.APIID, cfg.APIHash, gotdtelegram.Options{
		AllowCDN:       true,
		SessionStorage: &session.FileStorage{Path: cfg.SessionFile},
		UpdateHandler: gotdtelegram.UpdateHandlerFunc(func(ctx context.Context, update tg.UpdatesClass) error {
			if updateHandler == nil {
				return errors.New("Telegram update handler is not initialized")
			}
			return updateHandler.Handle(ctx, update)
		}),
		Middlewares: []gotdtelegram.Middleware{
			updatehook.UpdateHook(func(ctx context.Context, update tg.UpdatesClass) error {
				if updateHandler == nil {
					return errors.New("Telegram update handler is not initialized")
				}
				return updateHandler.Handle(ctx, update)
			}),
			updatehook.AffectedHook(client.updates),
		},
		Device: gotdtelegram.DeviceConfig{
			DeviceModel:    "TeleBox-Go",
			SystemVersion:  "Go",
			AppVersion:     deviceAppVersion(buildinfo.Version),
			SystemLangCode: "en",
			LangCode:       "en",
		},
	})
	client.peers = (peers.Options{}).Build(client.raw.API())
	updateHandler = client.peers.UpdateHook(client.updates)
	return client, nil
}

func deviceAppVersion(version string) string {
	version = strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if version == "" {
		return "dev"
	}
	return version
}

func (c *Client) Run(ctx context.Context, handler teleboxtelegram.MessageHandler) error {
	if handler == nil {
		return errors.New("message handler is required")
	}

	c.runMu.Lock()
	defer c.runMu.Unlock()

	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.handler = nil
		c.sender = nil
		c.selfID = 0
		c.peerCache = make(map[int64]tg.InputPeerClass)
		c.mu.Unlock()
	}()

	return c.raw.Run(ctx, func(runCtx context.Context) error {
		if err := c.ensureAuthorized(runCtx); err != nil {
			return err
		}

		if err := c.peers.Init(runCtx); err != nil {
			return fmt.Errorf("initialize Telegram peers: %w", err)
		}
		self, err := c.raw.Self(runCtx)
		if err != nil {
			return fmt.Errorf("load Telegram self: %w", err)
		}
		var selfPeerID constant.TDLibPeerID
		selfPeerID.User(self.ID)

		c.mu.Lock()
		c.selfID = int64(selfPeerID)
		c.sender = message.NewSender(c.raw.API())
		c.peerCache[int64(selfPeerID)] = &tg.InputPeerSelf{}
		c.mu.Unlock()

		return c.updates.Run(runCtx, c.raw.API(), self.ID, updates.AuthOptions{IsBot: false})
	})
}

// Login performs the configured interactive authorization flow and exits as
// soon as gotd has persisted the session. It does not start update processing.
func (c *Client) Login(ctx context.Context) error {
	c.runMu.Lock()
	defer c.runMu.Unlock()

	return c.raw.Run(ctx, c.ensureAuthorized)
}

func (c *Client) ensureAuthorized(ctx context.Context) error {
	status, err := c.raw.Auth().Status(ctx)
	if err != nil {
		return fmt.Errorf("check Telegram authorization: %w", err)
	}
	if status.Authorized {
		return nil
	}

	switch strings.ToLower(c.config.LoginMode) {
	case "qr":
		return c.authorizeQR(ctx)
	case "phone":
		return c.authorizePhone(ctx)
	default:
		return fmt.Errorf(
			"%w: run telebox with -login and choose qr or phone login",
			teleboxtelegram.ErrNotAuthorized,
		)
	}
}

func (c *Client) authorizeQR(ctx context.Context) error {
	if c.config.OnQRCode == nil {
		return errors.New("QR login requested but no QR callback is configured")
	}
	_, err := c.raw.QR().Auth(ctx, c.loggedIn, func(showCtx context.Context, token qrlogin.Token) error {
		return c.config.OnQRCode(showCtx, QRCode{
			URL:       token.URL(),
			ExpiresAt: token.Expires(),
		})
	})
	if err != nil {
		return fmt.Errorf("Telegram QR login: %w", err)
	}
	return nil
}

func (c *Client) authorizePhone(ctx context.Context) error {
	flow := auth.NewFlow(c.config.PhoneAuth, auth.SendCodeOptions{})
	if err := c.raw.Auth().IfNecessary(ctx, flow); err != nil {
		return fmt.Errorf("Telegram phone login: %w", err)
	}
	return nil
}

func (c *Client) SendText(ctx context.Context, chatID int64, text string) (teleboxtelegram.SentMessage, error) {
	c.mu.RLock()
	sender := c.sender
	c.mu.RUnlock()
	if sender == nil {
		return teleboxtelegram.SentMessage{}, teleboxtelegram.ErrTransportUnavailable
	}

	peer, err := c.resolveInputPeer(ctx, chatID)
	if err != nil {
		return teleboxtelegram.SentMessage{}, err
	}

	sentUpdates, err := sender.To(peer).Text(ctx, text)
	messageID, err := unpack.MessageID(sentUpdates, err)
	if err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("send text: %w", err)
	}
	return teleboxtelegram.SentMessage{ChatID: chatID, MessageID: messageID}, nil
}

func (c *Client) SendHTML(ctx context.Context, chatID int64, text string) (teleboxtelegram.SentMessage, error) {
	c.mu.RLock()
	sender := c.sender
	c.mu.RUnlock()
	if sender == nil {
		return teleboxtelegram.SentMessage{}, teleboxtelegram.ErrTransportUnavailable
	}
	peer, err := c.resolveInputPeer(ctx, chatID)
	if err != nil {
		return teleboxtelegram.SentMessage{}, err
	}
	sentUpdates, err := sender.To(peer).StyledText(ctx, messagehtml.String(nil, text))
	messageID, err := unpack.MessageID(sentUpdates, err)
	if err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("send HTML text: %w", err)
	}
	return teleboxtelegram.SentMessage{ChatID: chatID, MessageID: messageID}, nil
}

func (c *Client) ReplyText(ctx context.Context, chatID int64, replyToID int, text string) (teleboxtelegram.SentMessage, error) {
	c.mu.RLock()
	sender := c.sender
	c.mu.RUnlock()
	if sender == nil {
		return teleboxtelegram.SentMessage{}, teleboxtelegram.ErrTransportUnavailable
	}
	if replyToID <= 0 {
		return teleboxtelegram.SentMessage{}, errors.New("reply message ID must be greater than zero")
	}
	peer, err := c.resolveInputPeer(ctx, chatID)
	if err != nil {
		return teleboxtelegram.SentMessage{}, err
	}
	sentUpdates, err := sender.To(peer).Reply(replyToID).Text(ctx, text)
	messageID, err := unpack.MessageID(sentUpdates, err)
	if err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("reply with text: %w", err)
	}
	return teleboxtelegram.SentMessage{ChatID: chatID, MessageID: messageID}, nil
}

func (c *Client) ReplyHTML(
	ctx context.Context,
	chatID int64,
	replyToID int,
	text string,
) (teleboxtelegram.SentMessage, error) {
	c.mu.RLock()
	sender := c.sender
	c.mu.RUnlock()
	if sender == nil {
		return teleboxtelegram.SentMessage{}, teleboxtelegram.ErrTransportUnavailable
	}
	if replyToID <= 0 {
		return teleboxtelegram.SentMessage{}, errors.New("reply message ID must be greater than zero")
	}
	peer, err := c.resolveInputPeer(ctx, chatID)
	if err != nil {
		return teleboxtelegram.SentMessage{}, err
	}
	sentUpdates, err := sender.To(peer).Reply(replyToID).StyledText(
		ctx,
		messagehtml.String(nil, text),
	)
	messageID, err := unpack.MessageID(sentUpdates, err)
	if err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("reply with HTML text: %w", err)
	}
	return teleboxtelegram.SentMessage{ChatID: chatID, MessageID: messageID}, nil
}

func (c *Client) EditText(ctx context.Context, chatID int64, messageID int, text string) (teleboxtelegram.SentMessage, error) {
	c.mu.RLock()
	sender := c.sender
	c.mu.RUnlock()
	if sender == nil {
		return teleboxtelegram.SentMessage{}, teleboxtelegram.ErrTransportUnavailable
	}
	if messageID <= 0 {
		return teleboxtelegram.SentMessage{}, errors.New("message ID must be greater than zero")
	}
	peer, err := c.resolveInputPeer(ctx, chatID)
	if err != nil {
		return teleboxtelegram.SentMessage{}, err
	}
	if _, err := sender.To(peer).Edit(messageID).Text(ctx, text); err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("edit text: %w", err)
	}
	return teleboxtelegram.SentMessage{ChatID: chatID, MessageID: messageID}, nil
}

func (c *Client) EditHTML(
	ctx context.Context,
	chatID int64,
	messageID int,
	text string,
) (teleboxtelegram.SentMessage, error) {
	c.mu.RLock()
	sender := c.sender
	c.mu.RUnlock()
	if sender == nil {
		return teleboxtelegram.SentMessage{}, teleboxtelegram.ErrTransportUnavailable
	}
	if messageID <= 0 {
		return teleboxtelegram.SentMessage{}, errors.New("message ID must be greater than zero")
	}
	peer, err := c.resolveInputPeer(ctx, chatID)
	if err != nil {
		return teleboxtelegram.SentMessage{}, err
	}
	_, err = sender.To(peer).Edit(messageID).StyledText(
		ctx,
		messagehtml.String(nil, text),
	)
	if err != nil {
		return teleboxtelegram.SentMessage{}, fmt.Errorf("edit HTML text: %w", err)
	}
	return teleboxtelegram.SentMessage{ChatID: chatID, MessageID: messageID}, nil
}

func (c *Client) DeleteMessages(ctx context.Context, chatID int64, messageIDs []int) error {
	c.mu.RLock()
	sender := c.sender
	c.mu.RUnlock()
	if sender == nil {
		return teleboxtelegram.ErrTransportUnavailable
	}
	if len(messageIDs) == 0 {
		return errors.New("at least one message ID is required")
	}
	peer, err := c.resolveInputPeer(ctx, chatID)
	if err != nil {
		return err
	}
	if _, err := sender.To(peer).Revoke().Messages(ctx, messageIDs...); err != nil {
		return fmt.Errorf("delete messages: %w", err)
	}
	return nil
}

func (c *Client) ForwardMessages(ctx context.Context, fromChatID, toChatID int64, messageIDs []int) error {
	return c.forwardMessages(ctx, fromChatID, toChatID, messageIDs, false)
}

func (c *Client) CopyMessages(ctx context.Context, fromChatID, toChatID int64, messageIDs []int) error {
	return c.forwardMessages(ctx, fromChatID, toChatID, messageIDs, true)
}

func (c *Client) forwardMessages(
	ctx context.Context,
	fromChatID, toChatID int64,
	messageIDs []int,
	dropAuthor bool,
) error {
	c.mu.RLock()
	sender := c.sender
	c.mu.RUnlock()
	if sender == nil {
		return teleboxtelegram.ErrTransportUnavailable
	}
	if len(messageIDs) == 0 {
		return errors.New("at least one message ID is required")
	}
	fromPeer, err := c.resolveInputPeer(ctx, fromChatID)
	if err != nil {
		return err
	}
	toPeer, err := c.resolveInputPeer(ctx, toChatID)
	if err != nil {
		return err
	}
	builder := sender.To(toPeer).ForwardIDs(fromPeer, messageIDs[0], messageIDs[1:]...)
	if dropAuthor {
		builder = builder.DropAuthor()
	}
	if _, err := builder.Send(ctx); err != nil {
		operation := "forward"
		if dropAuthor {
			operation = "copy"
		}
		return fmt.Errorf("%s messages: %w", operation, err)
	}
	return nil
}

func (c *Client) resolveInputPeer(ctx context.Context, chatID int64) (tg.InputPeerClass, error) {
	resolved, err := c.peers.ResolveTDLibID(ctx, constant.TDLibPeerID(chatID))
	if err == nil {
		return resolved.InputPeer(), nil
	}

	c.mu.RLock()
	cachedPeer, cached := c.peerCache[chatID]
	c.mu.RUnlock()
	if cached {
		return cachedPeer, nil
	}
	return nil, fmt.Errorf("%w: chat_id=%d: %v", teleboxtelegram.ErrPeerNotResolved, chatID, err)
}

func (c *Client) onNewMessage(ctx context.Context, entities tg.Entities, update *tg.UpdateNewMessage) error {
	return c.handleMessage(ctx, entities, update.Message, false)
}

func (c *Client) onNewChannelMessage(ctx context.Context, entities tg.Entities, update *tg.UpdateNewChannelMessage) error {
	return c.handleMessage(ctx, entities, update.Message, false)
}

func (c *Client) onEditMessage(
	ctx context.Context,
	entities tg.Entities,
	update *tg.UpdateEditMessage,
) error {
	return c.handleMessage(ctx, entities, update.Message, true)
}

func (c *Client) onEditChannelMessage(
	ctx context.Context,
	entities tg.Entities,
	update *tg.UpdateEditChannelMessage,
) error {
	return c.handleMessage(ctx, entities, update.Message, true)
}

func (c *Client) handleMessage(
	ctx context.Context,
	entities tg.Entities,
	messageClass tg.MessageClass,
	edited bool,
) error {
	raw, ok := messageClass.(*tg.Message)
	if !ok {
		return nil
	}

	chatID, ok := peerID(raw.PeerID)
	if !ok {
		return nil
	}
	inputPeer, err := inputPeer(entities, raw.PeerID)
	if err != nil {
		c.mu.RLock()
		cached, ok := c.peerCache[chatID]
		c.mu.RUnlock()
		if !ok {
			return err
		}
		inputPeer = cached
	}

	c.cacheEntities(entities)
	c.mu.Lock()
	c.peerCache[chatID] = inputPeer
	handler := c.handler
	selfID := c.selfID
	c.mu.Unlock()
	if handler == nil {
		return nil
	}

	message := stableMessage(raw, chatID, selfID)
	message.Edited = edited
	return handler(ctx, message)
}

func (c *Client) cacheEntities(entities tg.Entities) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, user := range entities.Users {
		var peerID constant.TDLibPeerID
		peerID.User(id)
		c.peerCache[int64(peerID)] = user.AsInputPeer()
	}
	for id, chat := range entities.Chats {
		var peerID constant.TDLibPeerID
		peerID.Chat(id)
		c.peerCache[int64(peerID)] = chat.AsInputPeer()
	}
	for id, channel := range entities.Channels {
		var peerID constant.TDLibPeerID
		peerID.Channel(id)
		c.peerCache[int64(peerID)] = channel.AsInputPeer()
	}
}

func peerID(peer tg.PeerClass) (int64, bool) {
	var id constant.TDLibPeerID
	switch value := peer.(type) {
	case *tg.PeerUser:
		id.User(value.UserID)
	case *tg.PeerChat:
		id.Chat(value.ChatID)
	case *tg.PeerChannel:
		id.Channel(value.ChannelID)
	default:
		return 0, false
	}
	return int64(id), true
}

func inputPeer(entities tg.Entities, peer tg.PeerClass) (tg.InputPeerClass, error) {
	switch value := peer.(type) {
	case *tg.PeerUser:
		user, ok := entities.Users[value.UserID]
		if !ok {
			return nil, fmt.Errorf("%w: user_id=%d", teleboxtelegram.ErrPeerNotResolved, value.UserID)
		}
		return user.AsInputPeer(), nil
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: value.ChatID}, nil
	case *tg.PeerChannel:
		channel, ok := entities.Channels[value.ChannelID]
		if !ok {
			return nil, fmt.Errorf("%w: channel_id=%d", teleboxtelegram.ErrPeerNotResolved, value.ChannelID)
		}
		return channel.AsInputPeer(), nil
	default:
		return nil, fmt.Errorf("%w: unsupported peer %T", teleboxtelegram.ErrPeerNotResolved, peer)
	}
}
