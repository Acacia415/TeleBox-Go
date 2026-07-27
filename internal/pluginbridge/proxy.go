package pluginbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/pluginrpc"
	"github.com/Acacia415/TeleBox-Go/internal/scheduler"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

func NewProxyServices(
	peer *pluginrpc.Peer,
	pluginName string,
	workDir string,
	assetsDir string,
	logger *slog.Logger,
) (service.Container, error) {
	if peer == nil {
		return service.Container{}, errors.New("plugin RPC peer is required")
	}
	if pluginName == "" {
		return service.Container{}, errors.New("plugin name is required")
	}
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return service.Container{}, err
	}
	if assetsDir == "" {
		assetsDir = filepath.Join(workDir, "assets")
	}
	if err := os.MkdirAll(assetsDir, 0o700); err != nil {
		return service.Container{}, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	localScheduler := scheduler.New(logger)
	return service.Container{
		Logger:    logger,
		Telegram:  &telegramProxy{peer: peer, workDir: workDir},
		Storage:   &storageProxy{peer: peer},
		Tools:     &toolProxy{peer: peer},
		Scheduler: localScheduler,
		AssetsDir: assetsDir,
		HTTP:      &httpProxy{peer: peer},
	}, nil
}

type storageProxy struct {
	peer *pluginrpc.Peer
}

func (p *storageProxy) Put(
	ctx context.Context,
	plugin string,
	key string,
	value []byte,
) error {
	return call(ctx, p.peer, MethodStoragePut, StorageRequest{
		Plugin: plugin,
		Key:    key,
		Value:  value,
	}, nil)
}

func (p *storageProxy) Get(
	ctx context.Context,
	plugin string,
	key string,
) ([]byte, error) {
	var value []byte
	err := call(ctx, p.peer, MethodStorageGet, StorageRequest{
		Plugin: plugin,
		Key:    key,
	}, &value)
	return value, err
}

func (p *storageProxy) Delete(
	ctx context.Context,
	plugin string,
	key string,
) error {
	return call(ctx, p.peer, MethodStorageDelete, StorageRequest{
		Plugin: plugin,
		Key:    key,
	}, nil)
}

func (*storageProxy) Backup(context.Context, string) error {
	return errors.New("external plugins cannot back up global storage")
}

func (*storageProxy) SetPluginState(context.Context, storage.PluginState) error {
	return errors.New("external plugins cannot change global plugin state")
}

func (*storageProxy) PluginStates(context.Context) ([]storage.PluginState, error) {
	return nil, errors.New("external plugins cannot list global plugin state")
}

func (*storageProxy) Close() error {
	return nil
}

type httpProxy struct {
	peer *pluginrpc.Peer
}

func (p *httpProxy) Do(
	ctx context.Context,
	request httpclient.Request,
) (httpclient.Response, error) {
	var response httpclient.Response
	err := call(ctx, p.peer, MethodHTTPDo, HTTPRequest{Request: request}, &response)
	return response, err
}

func (p *httpProxy) JSON(
	ctx context.Context,
	request httpclient.Request,
	target any,
) (httpclient.Response, error) {
	response, err := p.Do(ctx, request)
	if err != nil {
		return response, err
	}
	if target == nil {
		return response, errors.New("JSON target is required")
	}
	if err := json.Unmarshal(response.Body, target); err != nil {
		return response, fmt.Errorf("decode HTTP JSON response: %w", err)
	}
	return response, nil
}

func (*httpProxy) Close() {}

type toolProxy struct {
	peer *pluginrpc.Peer
}

func (p *toolProxy) LookPath(name string) (string, error) {
	var result string
	err := call(
		context.Background(),
		p.peer,
		MethodToolLookPath,
		name,
		&result,
	)
	return result, err
}

func (p *toolProxy) Run(
	ctx context.Context,
	request toolrunner.Command,
) (toolrunner.Result, error) {
	var response ToolResponse
	err := call(ctx, p.peer, MethodToolRun, ToolRequest{Command: request}, &response)
	if err != nil {
		return response.Result, err
	}
	if response.Error != nil {
		return response.Result, translateRemoteError(&pluginrpc.RemoteError{
			Code:    response.Error.Code,
			Message: response.Error.Message,
		})
	}
	return response.Result, nil
}

type telegramProxy struct {
	peer    *pluginrpc.Peer
	workDir string
}

func (*telegramProxy) Run(context.Context, telegram.MessageHandler) error {
	return telegram.ErrTransportUnavailable
}

func (p *telegramProxy) SendText(
	ctx context.Context,
	chatID int64,
	text string,
) (telegram.SentMessage, error) {
	var result telegram.SentMessage
	err := call(ctx, p.peer, MethodTelegramSendText, TextRequest{
		ChatID: chatID,
		Text:   text,
	}, &result)
	return result, err
}

func (p *telegramProxy) ReplyText(
	ctx context.Context,
	chatID int64,
	replyToID int,
	text string,
) (telegram.SentMessage, error) {
	var result telegram.SentMessage
	err := call(ctx, p.peer, MethodTelegramReplyText, TextRequest{
		ChatID:    chatID,
		MessageID: replyToID,
		Text:      text,
	}, &result)
	return result, err
}

func (p *telegramProxy) EditText(
	ctx context.Context,
	chatID int64,
	messageID int,
	text string,
) (telegram.SentMessage, error) {
	var result telegram.SentMessage
	err := call(ctx, p.peer, MethodTelegramEditText, TextRequest{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      text,
	}, &result)
	return result, err
}

func (p *telegramProxy) SendHTML(
	ctx context.Context,
	chatID int64,
	text string,
) (telegram.SentMessage, error) {
	var result telegram.SentMessage
	err := call(ctx, p.peer, MethodTelegramSendHTML, TextRequest{
		ChatID: chatID,
		Text:   text,
	}, &result)
	return result, err
}

func (p *telegramProxy) ReplyHTML(
	ctx context.Context,
	chatID int64,
	replyToID int,
	text string,
) (telegram.SentMessage, error) {
	var result telegram.SentMessage
	err := call(ctx, p.peer, MethodTelegramReplyHTML, TextRequest{
		ChatID:    chatID,
		MessageID: replyToID,
		Text:      text,
	}, &result)
	return result, err
}

func (p *telegramProxy) EditHTML(
	ctx context.Context,
	chatID int64,
	messageID int,
	text string,
) (telegram.SentMessage, error) {
	var result telegram.SentMessage
	err := call(ctx, p.peer, MethodTelegramEditHTML, TextRequest{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      text,
	}, &result)
	return result, err
}

func (p *telegramProxy) DeleteMessages(
	ctx context.Context,
	chatID int64,
	messageIDs []int,
) error {
	return call(ctx, p.peer, MethodTelegramDeleteMessages, MessagesRequest{
		ChatID:     chatID,
		MessageIDs: messageIDs,
	}, nil)
}

func (p *telegramProxy) ForwardMessages(
	ctx context.Context,
	fromChatID int64,
	toChatID int64,
	messageIDs []int,
) error {
	return call(ctx, p.peer, MethodTelegramForwardMessages, TransferRequest{
		FromChatID: fromChatID,
		ToChatID:   toChatID,
		MessageIDs: messageIDs,
	}, nil)
}

func (p *telegramProxy) CopyMessages(
	ctx context.Context,
	fromChatID int64,
	toChatID int64,
	messageIDs []int,
) error {
	return call(ctx, p.peer, MethodTelegramCopyMessages, TransferRequest{
		FromChatID: fromChatID,
		ToChatID:   toChatID,
		MessageIDs: messageIDs,
	}, nil)
}

func (p *telegramProxy) SendFile(
	ctx context.Context,
	chatID int64,
	upload telegram.Upload,
) (telegram.SentMessage, error) {
	staged, cleanup, err := p.stageUpload(upload.Path)
	if err != nil {
		return telegram.SentMessage{}, err
	}
	defer cleanup()
	upload.Path = staged
	var result telegram.SentMessage
	err = call(ctx, p.peer, MethodTelegramSendFile, SendFileRequest{
		ChatID: chatID,
		Upload: upload,
	}, &result)
	return result, err
}

func (p *telegramProxy) GetMessages(
	ctx context.Context,
	chatID int64,
	messageIDs []int,
) ([]telegram.Message, error) {
	var result []telegram.Message
	err := call(ctx, p.peer, MethodTelegramGetMessages, MessagesRequest{
		ChatID:     chatID,
		MessageIDs: messageIDs,
	}, &result)
	return result, err
}

func (p *telegramProxy) GetHistory(
	ctx context.Context,
	query telegram.HistoryQuery,
) ([]telegram.Message, error) {
	var result []telegram.Message
	err := call(ctx, p.peer, MethodTelegramGetHistory, HistoryRequest{
		Query: query,
	}, &result)
	return result, err
}

func (p *telegramProxy) DownloadMedia(
	ctx context.Context,
	chatID int64,
	messageID int,
	writer io.Writer,
) (telegram.Media, error) {
	target, cleanup, err := p.downloadTarget("media")
	if err != nil {
		return telegram.Media{}, err
	}
	defer cleanup()
	var media telegram.Media
	if err := call(ctx, p.peer, MethodTelegramDownloadMedia, DownloadRequest{
		ChatID:    chatID,
		MessageID: messageID,
		Path:      target,
	}, &media); err != nil {
		return telegram.Media{}, err
	}
	if err := copyFileToWriter(target, writer); err != nil {
		return telegram.Media{}, err
	}
	return media, nil
}

func (p *telegramProxy) DownloadProfilePhoto(
	ctx context.Context,
	userID int64,
	writer io.Writer,
) error {
	target, cleanup, err := p.downloadTarget("profile")
	if err != nil {
		return err
	}
	defer cleanup()
	if err := call(
		ctx,
		p.peer,
		MethodTelegramDownloadProfilePhoto,
		ProfilePhotoRequest{UserID: userID, Path: target},
		nil,
	); err != nil {
		return err
	}
	return copyFileToWriter(target, writer)
}

func (p *telegramProxy) ResolveUser(
	ctx context.Context,
	target string,
) (telegram.User, error) {
	var result telegram.User
	err := call(ctx, p.peer, MethodTelegramResolveUser, TargetRequest{
		Target: target,
	}, &result)
	return result, err
}

func (p *telegramProxy) ResolveChat(
	ctx context.Context,
	chatID int64,
) (telegram.Chat, error) {
	var result telegram.Chat
	err := call(ctx, p.peer, MethodTelegramResolveChat, ChatRequest{
		ChatID: chatID,
	}, &result)
	return result, err
}

func (p *telegramProxy) ResolveChatTarget(
	ctx context.Context,
	target string,
) (telegram.Chat, error) {
	var result telegram.Chat
	err := call(ctx, p.peer, MethodTelegramResolveChatTarget, TargetRequest{
		Target: target,
	}, &result)
	return result, err
}

func (p *telegramProxy) GetMyPermissions(
	ctx context.Context,
	chatID int64,
) (telegram.ChatPermissions, error) {
	var result telegram.ChatPermissions
	err := call(ctx, p.peer, MethodTelegramGetMyPermissions, ChatRequest{
		ChatID: chatID,
	}, &result)
	return result, err
}

func (p *telegramProxy) GetChatMember(
	ctx context.Context,
	chatID int64,
	userID int64,
) (telegram.ChatMember, error) {
	var result telegram.ChatMember
	err := call(ctx, p.peer, MethodTelegramGetChatMember, MemberRequest{
		ChatID: chatID,
		UserID: userID,
	}, &result)
	return result, err
}

func (p *telegramProxy) FindJoinTime(
	ctx context.Context,
	chatID int64,
	userID int64,
	limit int,
	progress telegram.JoinSearchProgress,
) (time.Time, int, error) {
	var result FindJoinTimeResult
	err := call(ctx, p.peer, MethodTelegramFindJoinTime, FindJoinTimeRequest{
		ChatID: chatID,
		UserID: userID,
		Limit:  limit,
	}, &result)
	if err != nil {
		return time.Time{}, result.Scanned, err
	}
	if progress != nil {
		if err := progress(result.Scanned, 0); err != nil {
			return time.Time{}, result.Scanned, err
		}
	}
	return time.Unix(result.UnixSeconds, 0), result.Scanned, nil
}

func (p *telegramProxy) ListChats(
	ctx context.Context,
	limit int,
) ([]telegram.Chat, error) {
	var result []telegram.Chat
	err := call(ctx, p.peer, MethodTelegramListChats, LimitRequest{
		Limit: limit,
	}, &result)
	return result, err
}

func (p *telegramProxy) ListManagedChats(
	ctx context.Context,
	limit int,
) ([]telegram.Chat, error) {
	var result []telegram.Chat
	err := call(ctx, p.peer, MethodTelegramListManagedChats, LimitRequest{
		Limit: limit,
	}, &result)
	return result, err
}

func (p *telegramProxy) JoinChat(
	ctx context.Context,
	target string,
) error {
	return call(ctx, p.peer, MethodTelegramJoinChat, TargetRequest{
		Target: target,
	}, nil)
}

func (p *telegramProxy) ModerateUser(
	ctx context.Context,
	request telegram.ModerationRequest,
) error {
	return call(ctx, p.peer, MethodTelegramModerateUser, ModerationRequest{
		Request: request,
	}, nil)
}

func (p *telegramProxy) DeleteUserHistory(
	ctx context.Context,
	chatID int64,
	userID int64,
) error {
	return call(ctx, p.peer, MethodTelegramDeleteUserHistory, MemberRequest{
		ChatID: chatID,
		UserID: userID,
	}, nil)
}

func (p *telegramProxy) SendReaction(
	ctx context.Context,
	chatID int64,
	messageID int,
	reactions []telegram.Reaction,
	big bool,
) error {
	return call(ctx, p.peer, MethodTelegramSendReaction, ReactionRequest{
		ChatID:    chatID,
		MessageID: messageID,
		Reactions: reactions,
		Big:       big,
	}, nil)
}

func (p *telegramProxy) GetStickerSet(
	ctx context.Context,
	shortName string,
) (telegram.StickerSet, error) {
	var result telegram.StickerSet
	err := call(ctx, p.peer, MethodTelegramGetStickerSet, StickerSetRequest{
		ShortName: shortName,
	}, &result)
	return result, err
}

func (p *telegramProxy) CreateStickerSet(
	ctx context.Context,
	userID int64,
	shortName string,
	title string,
	sticker telegram.Sticker,
) error {
	return call(ctx, p.peer, MethodTelegramCreateStickerSet, CreateStickerSetRequest{
		UserID:    userID,
		ShortName: shortName,
		Title:     title,
		Sticker:   sticker,
	}, nil)
}

func (p *telegramProxy) AddStickerToSet(
	ctx context.Context,
	shortName string,
	sticker telegram.Sticker,
) error {
	return call(ctx, p.peer, MethodTelegramAddStickerToSet, AddStickerRequest{
		ShortName: shortName,
		Sticker:   sticker,
	}, nil)
}

func (p *telegramProxy) RequestBotMedia(
	ctx context.Context,
	request telegram.BotMediaRequest,
) (telegram.Message, error) {
	var result telegram.Message
	err := call(ctx, p.peer, MethodTelegramRequestBotMedia, BotMediaRequest{
		Request: request,
	}, &result)
	return result, err
}

func (p *telegramProxy) stageUpload(
	source string,
) (string, func(), error) {
	input, err := os.Open(source)
	if err != nil {
		return "", func() {}, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", func() {}, errors.New("upload source is not a regular file")
	}
	directory := filepath.Join(p.workDir, "rpc", "uploads")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", func() {}, err
	}
	output, err := os.CreateTemp(directory, "upload-*")
	if err != nil {
		return "", func() {}, err
	}
	path := output.Name()
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		_ = os.Remove(path)
		return "", func() {}, err
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(path)
		return "", func() {}, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func (p *telegramProxy) downloadTarget(
	prefix string,
) (string, func(), error) {
	directory := filepath.Join(p.workDir, "rpc", "downloads")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", func() {}, err
	}
	file, err := os.CreateTemp(directory, prefix+"-*")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", func() {}, err
	}
	if err := os.Remove(path); err != nil {
		return "", func() {}, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func copyFileToWriter(path string, writer io.Writer) error {
	if writer == nil {
		return errors.New("download writer is required")
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	_, err = io.Copy(writer, input)
	return err
}

func call(
	ctx context.Context,
	peer *pluginrpc.Peer,
	method string,
	request any,
	target any,
) error {
	err := peer.Call(ctx, method, request, target)
	if err == nil {
		return nil
	}
	var remote *pluginrpc.RemoteError
	if !errors.As(err, &remote) {
		return err
	}
	return translateRemoteError(remote)
}

func translateRemoteError(remote *pluginrpc.RemoteError) error {
	var sentinel error
	switch remote.Code {
	case "storage_not_found":
		sentinel = storage.ErrNotFound
	case "telegram_not_authorized":
		sentinel = telegram.ErrNotAuthorized
	case "telegram_peer_not_resolved":
		sentinel = telegram.ErrPeerNotResolved
	case "telegram_message_not_found":
		sentinel = telegram.ErrMessageNotFound
	case "telegram_media_not_found":
		sentinel = telegram.ErrMediaNotFound
	case "telegram_sticker_set_not_found":
		sentinel = telegram.ErrStickerSetNotFound
	case "tool_not_found":
		sentinel = toolrunner.ErrExecutableNotFound
	case "http_response_too_large":
		sentinel = httpclient.ErrResponseTooLarge
	}
	if sentinel != nil {
		return fmt.Errorf("%w: %s", sentinel, remote.Message)
	}
	return remote
}
