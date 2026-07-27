package pluginbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/pluginrpc"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

type Host struct {
	services    service.Container
	manifest    pluginapi.Manifest
	workDir     string
	permissions map[string]struct{}
	tools       map[string]struct{}
}

func NewHost(
	services service.Container,
	manifest pluginapi.Manifest,
	workDir string,
) (*Host, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(workDir) == "" {
		return nil, errors.New("plugin work directory is required")
	}
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return nil, fmt.Errorf("create plugin work directory: %w", err)
	}
	host := &Host{
		services:    services,
		manifest:    manifest,
		workDir:     filepath.Clean(workDir),
		permissions: make(map[string]struct{}),
		tools:       make(map[string]struct{}),
	}
	for _, permission := range manifest.Permissions.Telegram {
		host.permissions[strings.ToLower(strings.TrimSpace(permission))] = struct{}{}
	}
	for _, tool := range manifest.Permissions.Tools {
		host.tools[strings.ToLower(strings.TrimSpace(tool))] = struct{}{}
	}
	return host, nil
}

func (h *Host) Handle(
	ctx context.Context,
	method string,
	raw json.RawMessage,
) (any, error) {
	switch method {
	case MethodStorageGet:
		if !h.manifest.Permissions.Storage {
			return nil, denied(method)
		}
		request, err := decode[StorageRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkNamespace(request.Plugin); err != nil {
			return nil, err
		}
		value, err := h.services.Storage.Get(ctx, request.Plugin, request.Key)
		return value, translateError(err)
	case MethodStoragePut:
		if !h.manifest.Permissions.Storage {
			return nil, denied(method)
		}
		request, err := decode[StorageRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkNamespace(request.Plugin); err != nil {
			return nil, err
		}
		return nil, translateError(h.services.Storage.Put(
			ctx,
			request.Plugin,
			request.Key,
			request.Value,
		))
	case MethodStorageDelete:
		if !h.manifest.Permissions.Storage {
			return nil, denied(method)
		}
		request, err := decode[StorageRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkNamespace(request.Plugin); err != nil {
			return nil, err
		}
		return nil, translateError(h.services.Storage.Delete(
			ctx,
			request.Plugin,
			request.Key,
		))
	case MethodHTTPDo:
		if !h.manifest.Permissions.Network {
			return nil, denied(method)
		}
		request, err := decode[HTTPRequest](raw)
		if err != nil {
			return nil, err
		}
		response, err := h.services.HTTP.Do(ctx, request.Request)
		return response, translateError(err)
	case MethodToolLookPath:
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return nil, err
		}
		if err := h.checkTool(name); err != nil {
			return nil, err
		}
		result, err := h.services.Tools.LookPath(name)
		return result, translateError(err)
	case MethodToolRun:
		request, err := decode[ToolRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTool(request.Command.Name); err != nil {
			return nil, err
		}
		result, err := h.services.Tools.Run(ctx, request.Command)
		response := ToolResponse{Result: result}
		if translated := translateError(err); translated != nil {
			var remote *pluginrpc.RemoteError
			if errors.As(translated, &remote) {
				response.Error = &BridgeError{
					Code:    remote.Code,
					Message: remote.Message,
				}
			} else {
				response.Error = &BridgeError{
					Code:    "remote_error",
					Message: translated.Error(),
				}
			}
		}
		return response, nil
	case MethodTelegramSendText:
		request, err := decode[TextRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.SendText(ctx, request.ChatID, request.Text)
		return result, translateError(err)
	case MethodTelegramReplyText:
		request, err := decode[TextRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.ReplyText(
			ctx, request.ChatID, request.MessageID, request.Text,
		)
		return result, translateError(err)
	case MethodTelegramEditText:
		request, err := decode[TextRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.EditText(
			ctx, request.ChatID, request.MessageID, request.Text,
		)
		return result, translateError(err)
	case MethodTelegramSendHTML:
		request, err := decode[TextRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := telegram.SendHTML(
			ctx, h.services.Telegram, request.ChatID, request.Text,
		)
		return result, translateError(err)
	case MethodTelegramReplyHTML:
		request, err := decode[TextRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := telegram.ReplyHTML(
			ctx,
			h.services.Telegram,
			request.ChatID,
			request.MessageID,
			request.Text,
		)
		return result, translateError(err)
	case MethodTelegramEditHTML:
		request, err := decode[TextRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := telegram.EditHTML(
			ctx,
			h.services.Telegram,
			request.ChatID,
			request.MessageID,
			request.Text,
		)
		return result, translateError(err)
	case MethodTelegramDeleteMessages:
		request, err := decode[MessagesRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		return nil, translateError(h.services.Telegram.DeleteMessages(
			ctx, request.ChatID, request.MessageIDs,
		))
	case MethodTelegramForwardMessages, MethodTelegramCopyMessages:
		request, err := decode[TransferRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		if method == MethodTelegramForwardMessages {
			return nil, translateError(h.services.Telegram.ForwardMessages(
				ctx, request.FromChatID, request.ToChatID, request.MessageIDs,
			))
		}
		return nil, translateError(h.services.Telegram.CopyMessages(
			ctx, request.FromChatID, request.ToChatID, request.MessageIDs,
		))
	case MethodTelegramSendFile:
		request, err := decode[SendFileRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		safePath, err := h.safeExistingPath(request.Upload.Path)
		if err != nil {
			return nil, err
		}
		request.Upload.Path = safePath
		result, err := h.services.Telegram.SendFile(ctx, request.ChatID, request.Upload)
		return result, translateError(err)
	case MethodTelegramGetMessages:
		request, err := decode[MessagesRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.GetMessages(
			ctx, request.ChatID, request.MessageIDs,
		)
		return result, translateError(err)
	case MethodTelegramGetHistory:
		request, err := decode[HistoryRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.GetHistory(ctx, request.Query)
		return result, translateError(err)
	case MethodTelegramDownloadMedia:
		request, err := decode[DownloadRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		target, err := h.safeWritablePath(request.Path)
		if err != nil {
			return nil, err
		}
		output, err := os.Create(target)
		if err != nil {
			return nil, err
		}
		result, downloadErr := h.services.Telegram.DownloadMedia(
			ctx, request.ChatID, request.MessageID, output,
		)
		closeErr := output.Close()
		return result, translateError(errors.Join(downloadErr, closeErr))
	case MethodTelegramDownloadProfilePhoto:
		request, err := decode[ProfilePhotoRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		target, err := h.safeWritablePath(request.Path)
		if err != nil {
			return nil, err
		}
		output, err := os.Create(target)
		if err != nil {
			return nil, err
		}
		downloadErr := h.services.Telegram.DownloadProfilePhoto(
			ctx, request.UserID, output,
		)
		closeErr := output.Close()
		return nil, translateError(errors.Join(downloadErr, closeErr))
	case MethodTelegramResolveUser:
		request, err := decode[TargetRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.ResolveUser(ctx, request.Target)
		return result, translateError(err)
	case MethodTelegramResolveChat:
		request, err := decode[ChatRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.ResolveChat(ctx, request.ChatID)
		return result, translateError(err)
	case MethodTelegramResolveChatTarget:
		request, err := decode[TargetRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.ResolveChatTarget(ctx, request.Target)
		return result, translateError(err)
	case MethodTelegramGetMyPermissions:
		request, err := decode[ChatRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.GetMyPermissions(ctx, request.ChatID)
		return result, translateError(err)
	case MethodTelegramGetChatMember:
		request, err := decode[MemberRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.GetChatMember(
			ctx, request.ChatID, request.UserID,
		)
		return result, translateError(err)
	case MethodTelegramFindJoinTime:
		request, err := decode[FindJoinTimeRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		joined, scanned, err := h.services.Telegram.FindJoinTime(
			ctx,
			request.ChatID,
			request.UserID,
			request.Limit,
			nil,
		)
		return FindJoinTimeResult{
			UnixSeconds: joined.Unix(),
			Scanned:     scanned,
		}, translateError(err)
	case MethodTelegramListChats, MethodTelegramListManagedChats:
		request, err := decode[LimitRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		if method == MethodTelegramListChats {
			result, err := h.services.Telegram.ListChats(ctx, request.Limit)
			return result, translateError(err)
		}
		result, err := h.services.Telegram.ListManagedChats(ctx, request.Limit)
		return result, translateError(err)
	case MethodTelegramJoinChat:
		request, err := decode[TargetRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		return nil, translateError(h.services.Telegram.JoinChat(ctx, request.Target))
	case MethodTelegramModerateUser:
		request, err := decode[ModerationRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		return nil, translateError(h.services.Telegram.ModerateUser(
			ctx, request.Request,
		))
	case MethodTelegramDeleteUserHistory:
		request, err := decode[MemberRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		return nil, translateError(h.services.Telegram.DeleteUserHistory(
			ctx, request.ChatID, request.UserID,
		))
	case MethodTelegramSendReaction:
		request, err := decode[ReactionRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		return nil, translateError(h.services.Telegram.SendReaction(
			ctx,
			request.ChatID,
			request.MessageID,
			request.Reactions,
			request.Big,
		))
	case MethodTelegramGetStickerSet:
		request, err := decode[StickerSetRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.GetStickerSet(ctx, request.ShortName)
		return result, translateError(err)
	case MethodTelegramCreateStickerSet:
		request, err := decode[CreateStickerSetRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		return nil, translateError(h.services.Telegram.CreateStickerSet(
			ctx,
			request.UserID,
			request.ShortName,
			request.Title,
			request.Sticker,
		))
	case MethodTelegramAddStickerToSet:
		request, err := decode[AddStickerRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		return nil, translateError(h.services.Telegram.AddStickerToSet(
			ctx,
			request.ShortName,
			request.Sticker,
		))
	case MethodTelegramRequestBotMedia:
		request, err := decode[BotMediaRequest](raw)
		if err != nil {
			return nil, err
		}
		if err := h.checkTelegram(method); err != nil {
			return nil, err
		}
		result, err := h.services.Telegram.RequestBotMedia(ctx, request.Request)
		return result, translateError(err)
	default:
		return nil, &pluginrpc.RemoteError{
			Code:    "method_not_found",
			Message: method,
		}
	}
}

func (h *Host) checkNamespace(namespace string) error {
	if namespace != h.manifest.Name {
		return &pluginrpc.RemoteError{
			Code:    "permission_denied",
			Message: "plugin storage is isolated",
		}
	}
	return nil
}

func (h *Host) checkTool(name string) error {
	if _, all := h.tools["*"]; all {
		return nil
	}
	if _, allowed := h.tools[strings.ToLower(strings.TrimSpace(name))]; !allowed {
		return denied(MethodToolRun)
	}
	return nil
}

func (h *Host) checkTelegram(method string) error {
	name := strings.TrimPrefix(method, "telegram.")
	if _, all := h.permissions["*"]; all {
		return nil
	}
	if _, allowed := h.permissions[name]; !allowed {
		return denied(method)
	}
	return nil
}

func (h *Host) safeExistingPath(value string) (string, error) {
	target, err := h.safePath(value)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("plugin file is not a regular file")
	}
	return target, nil
}

func (h *Host) safeWritablePath(value string) (string, error) {
	target, err := h.safePath(value)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	return target, nil
}

func (h *Host) safePath(value string) (string, error) {
	root, err := filepath.Abs(h.workDir)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", &pluginrpc.RemoteError{
			Code:    "permission_denied",
			Message: "plugin path escapes its work directory",
		}
	}
	return target, nil
}

func denied(method string) error {
	return &pluginrpc.RemoteError{
		Code:    "permission_denied",
		Message: "plugin does not declare permission for " + method,
	}
}

func translateError(err error) error {
	if err == nil {
		return nil
	}
	code := "remote_error"
	switch {
	case errors.Is(err, storage.ErrNotFound):
		code = "storage_not_found"
	case errors.Is(err, telegram.ErrNotAuthorized):
		code = "telegram_not_authorized"
	case errors.Is(err, telegram.ErrPeerNotResolved):
		code = "telegram_peer_not_resolved"
	case errors.Is(err, telegram.ErrMessageNotFound):
		code = "telegram_message_not_found"
	case errors.Is(err, telegram.ErrMediaNotFound):
		code = "telegram_media_not_found"
	case errors.Is(err, telegram.ErrStickerSetNotFound):
		code = "telegram_sticker_set_not_found"
	case errors.Is(err, toolrunner.ErrExecutableNotFound):
		code = "tool_not_found"
	case errors.Is(err, httpclient.ErrResponseTooLarge):
		code = "http_response_too_large"
	}
	return &pluginrpc.RemoteError{Code: code, Message: err.Error()}
}

func decode[T any](raw json.RawMessage) (T, error) {
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("decode plugin RPC request: %w", err)
	}
	return result, nil
}
