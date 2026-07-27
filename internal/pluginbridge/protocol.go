package pluginbridge

import (
	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

const (
	MethodPluginStart   = "plugin.start"
	MethodPluginStop    = "plugin.stop"
	MethodPluginHandle  = "plugin.handle"
	MethodPluginMessage = "plugin.message"

	MethodStorageGet    = "storage.get"
	MethodStoragePut    = "storage.put"
	MethodStorageDelete = "storage.delete"

	MethodHTTPDo       = "http.do"
	MethodToolLookPath = "tool.look_path"
	MethodToolRun      = "tool.run"

	MethodTelegramSendText             = "telegram.send_text"
	MethodTelegramReplyText            = "telegram.reply_text"
	MethodTelegramEditText             = "telegram.edit_text"
	MethodTelegramSendHTML             = "telegram.send_html"
	MethodTelegramReplyHTML            = "telegram.reply_html"
	MethodTelegramEditHTML             = "telegram.edit_html"
	MethodTelegramDeleteMessages       = "telegram.delete_messages"
	MethodTelegramForwardMessages      = "telegram.forward_messages"
	MethodTelegramCopyMessages         = "telegram.copy_messages"
	MethodTelegramSendFile             = "telegram.send_file"
	MethodTelegramGetMessages          = "telegram.get_messages"
	MethodTelegramGetHistory           = "telegram.get_history"
	MethodTelegramDownloadMedia        = "telegram.download_media"
	MethodTelegramDownloadProfilePhoto = "telegram.download_profile_photo"
	MethodTelegramResolveUser          = "telegram.resolve_user"
	MethodTelegramResolveChat          = "telegram.resolve_chat"
	MethodTelegramResolveChatTarget    = "telegram.resolve_chat_target"
	MethodTelegramGetMyPermissions     = "telegram.get_my_permissions"
	MethodTelegramGetChatMember        = "telegram.get_chat_member"
	MethodTelegramFindJoinTime         = "telegram.find_join_time"
	MethodTelegramListChats            = "telegram.list_chats"
	MethodTelegramListManagedChats     = "telegram.list_managed_chats"
	MethodTelegramJoinChat             = "telegram.join_chat"
	MethodTelegramModerateUser         = "telegram.moderate_user"
	MethodTelegramDeleteUserHistory    = "telegram.delete_user_history"
	MethodTelegramSendReaction         = "telegram.send_reaction"
	MethodTelegramGetStickerSet        = "telegram.get_sticker_set"
	MethodTelegramCreateStickerSet     = "telegram.create_sticker_set"
	MethodTelegramAddStickerToSet      = "telegram.add_sticker_to_set"
	MethodTelegramRequestBotMedia      = "telegram.request_bot_media"
)

type Invocation struct {
	Command string          `json:"command"`
	Request command.Request `json:"request"`
}

type StorageRequest struct {
	Plugin string `json:"plugin"`
	Key    string `json:"key"`
	Value  []byte `json:"value,omitempty"`
}

type PluginStateRequest struct {
	State storage.PluginState `json:"state"`
}

type HTTPRequest struct {
	Request httpclient.Request `json:"request"`
}

type ToolRequest struct {
	Command toolrunner.Command `json:"command"`
}

type TextRequest struct {
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id,omitempty"`
	Text      string `json:"text"`
}

type MessagesRequest struct {
	ChatID     int64 `json:"chat_id"`
	MessageIDs []int `json:"message_ids"`
}

type TransferRequest struct {
	FromChatID int64 `json:"from_chat_id"`
	ToChatID   int64 `json:"to_chat_id"`
	MessageIDs []int `json:"message_ids"`
}

type SendFileRequest struct {
	ChatID int64           `json:"chat_id"`
	Upload telegram.Upload `json:"upload"`
}

type HistoryRequest struct {
	Query telegram.HistoryQuery `json:"query"`
}

type DownloadRequest struct {
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id"`
	Path      string `json:"path"`
}

type ProfilePhotoRequest struct {
	UserID int64  `json:"user_id"`
	Path   string `json:"path"`
}

type TargetRequest struct {
	Target string `json:"target"`
}

type ChatRequest struct {
	ChatID int64 `json:"chat_id"`
}

type MemberRequest struct {
	ChatID int64 `json:"chat_id"`
	UserID int64 `json:"user_id"`
}

type FindJoinTimeRequest struct {
	ChatID int64 `json:"chat_id"`
	UserID int64 `json:"user_id"`
	Limit  int   `json:"limit"`
}

type FindJoinTimeResult struct {
	UnixSeconds int64 `json:"unix_seconds"`
	Scanned     int   `json:"scanned"`
}

type LimitRequest struct {
	Limit int `json:"limit"`
}

type ModerationRequest struct {
	Request telegram.ModerationRequest `json:"request"`
}

type ReactionRequest struct {
	ChatID    int64               `json:"chat_id"`
	MessageID int                 `json:"message_id"`
	Reactions []telegram.Reaction `json:"reactions"`
	Big       bool                `json:"big"`
}

type StickerSetRequest struct {
	ShortName string `json:"short_name"`
}

type CreateStickerSetRequest struct {
	UserID    int64            `json:"user_id"`
	ShortName string           `json:"short_name"`
	Title     string           `json:"title"`
	Sticker   telegram.Sticker `json:"sticker"`
}

type AddStickerRequest struct {
	ShortName string           `json:"short_name"`
	Sticker   telegram.Sticker `json:"sticker"`
}

type BotMediaRequest struct {
	Request telegram.BotMediaRequest `json:"request"`
}
