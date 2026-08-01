package telegram

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrTransportUnavailable = errors.New("telegram transport unavailable")

var (
	ErrNotAuthorized      = errors.New("telegram session is not authorized")
	ErrPeerNotResolved    = errors.New("telegram peer is not resolved")
	ErrMessageNotFound    = errors.New("telegram message was not found")
	ErrMediaNotFound      = errors.New("telegram message has no downloadable media")
	ErrStickerSetNotFound = errors.New("telegram sticker set was not found")
)

type MediaKind string

const (
	MediaDocument  MediaKind = "document"
	MediaPhoto     MediaKind = "photo"
	MediaAudio     MediaKind = "audio"
	MediaVoice     MediaKind = "voice"
	MediaVideo     MediaKind = "video"
	MediaVideoNote MediaKind = "video_note"
	MediaAnimation MediaKind = "animation"
	MediaSticker   MediaKind = "sticker"
)

// Media contains the portable metadata plugins need when deciding how to
// process an attachment. Telegram TL objects remain private to the adapter.
type Media struct {
	Kind     MediaKind
	FileName string
	MIMEType string
	Size     int64
	Width    int
	Height   int
	Duration time.Duration
}

type Presence string

const (
	PresenceUnknown   Presence = "unknown"
	PresenceOnline    Presence = "online"
	PresenceRecently  Presence = "recently"
	PresenceOffline   Presence = "offline"
	PresenceLastWeek  Presence = "last_week"
	PresenceLastMonth Presence = "last_month"
)

type User struct {
	ID            int64
	FirstName     string
	LastName      string
	Username      string
	EmojiStatus   int64
	Phone         string
	Bio           string
	LanguageCode  string
	CommonChats   int
	Contact       bool
	MutualContact bool
	Deleted       bool
	Bot           bool
	Premium       bool
	Verified      bool
	Scam          bool
	Fake          bool
	PhotoDC       int
	Presence      Presence
	LastSeen      time.Time
}

type ChatKind string

const (
	ChatPrivate    ChatKind = "private"
	ChatGroup      ChatKind = "group"
	ChatSupergroup ChatKind = "supergroup"
	ChatChannel    ChatKind = "channel"
)

type Chat struct {
	ID           int64
	Title        string
	Username     string
	Kind         ChatKind
	PhotoDC      int
	MemberCount  int
	LinkedChatID int64
	InviteLink   string
	Description  string
	CreatedAt    time.Time
	Verified     bool
	Scam         bool
	Fake         bool
	Forum        bool
}

type ChatPermissions struct {
	DeleteMessages bool
	BanUsers       bool
}

type PrivateChatSettings struct {
	CanReportSpam bool
	AutoArchived  bool
}

type HistoryQuery struct {
	ChatID     int64
	Limit      int
	OffsetID   int
	AddOffset  int
	MinID      int
	MaxID      int
	Search     string
	FromUserID int64
	MediaKind  MediaKind
	ReplyToID  int
}

type ModerationAction string

const (
	ModerationKick   ModerationAction = "kick"
	ModerationBan    ModerationAction = "ban"
	ModerationUnban  ModerationAction = "unban"
	ModerationMute   ModerationAction = "mute"
	ModerationUnmute ModerationAction = "unmute"
)

type ModerationRequest struct {
	ChatID int64
	UserID int64
	Action ModerationAction
	Until  time.Time
}

type MemberRole string

const (
	MemberRoleMember  MemberRole = "member"
	MemberRoleAdmin   MemberRole = "admin"
	MemberRoleCreator MemberRole = "creator"
)

type ChatMember struct {
	UserID   int64
	Role     MemberRole
	JoinedAt time.Time
}

type JoinSearchProgress func(scanned, found int) error

type Reaction struct {
	Emoji      string
	DocumentID int64
}

type Sticker struct {
	DocumentID    int64
	AccessHash    int64
	FileReference []byte
	Emoji         string
	Animated      bool
	Video         bool
}

type StickerSet struct {
	ShortName string
	Title     string
	Count     int
}

type BotMediaRequest struct {
	Bot     string
	Query   string
	Timeout time.Duration
}

// InlineBotRequest asks an inline bot for results and sends the first result
// into the requested chat. It is intentionally narrower than the raw MTProto
// methods so plugins cannot submit arbitrary inline result identifiers.
type InlineBotRequest struct {
	Bot       string
	Query     string
	ChatID    int64
	ReplyToID int
	Silent    bool
}

type MessageEntity struct {
	Type       string
	Offset     int
	Length     int
	URL        string
	UserID     int64
	DocumentID int64
}

type Message struct {
	ID              int
	ChatID          int64
	SenderID        int64
	ForwardSenderID int64
	ForwardName     string
	ReplyToID       int
	ReplyQuote      string
	ReplyEntities   []MessageEntity
	Text            string
	Entities        []MessageEntity
	Outgoing        bool
	Edited          bool
	ViaBotID        int64
	Date            time.Time
	GroupedID       int64
	Media           *Media
	Sticker         *Sticker
	CustomEmojiIDs  []int64
}

type SentMessage struct {
	ChatID    int64
	MessageID int
}

// Upload describes a local file to send. Path must point to a regular file;
// plugins never pass shell commands or gotd-specific media objects.
type Upload struct {
	Path         string
	FileName     string
	MIMEType     string
	Caption      string
	ReplyToID    int
	Kind         MediaKind
	Width        int
	Height       int
	Duration     time.Duration
	Spoiler      bool
	AudioTitle   string
	Performer    string
	StickerEmoji string
}

type MessageHandler func(context.Context, Message) error

// Client is the stable surface used by the core and plugins. gotd-specific TL
// types must remain behind the adapter implementation.
type Client interface {
	Run(context.Context, MessageHandler) error
	SendText(context.Context, int64, string) (SentMessage, error)
	ReplyText(context.Context, int64, int, string) (SentMessage, error)
	EditText(context.Context, int64, int, string) (SentMessage, error)
	DeleteMessages(context.Context, int64, []int) error
	ForwardMessages(context.Context, int64, int64, []int) error
	CopyMessages(context.Context, int64, int64, []int) error
	SendFile(context.Context, int64, Upload) (SentMessage, error)
	GetMessages(context.Context, int64, []int) ([]Message, error)
	GetHistory(context.Context, HistoryQuery) ([]Message, error)
	DownloadMedia(context.Context, int64, int, io.Writer) (Media, error)
	DownloadMediaPreview(context.Context, int64, int, io.Writer) (Media, error)
	DownloadProfilePhoto(context.Context, int64, io.Writer) error
	ResolveUser(context.Context, string) (User, error)
	ResolveChat(context.Context, int64) (Chat, error)
	ResolveChatTarget(context.Context, string) (Chat, error)
	GetMyPermissions(context.Context, int64) (ChatPermissions, error)
	GetChatMember(context.Context, int64, int64) (ChatMember, error)
	FindJoinTime(context.Context, int64, int64, int, JoinSearchProgress) (time.Time, int, error)
	ListChats(context.Context, int) ([]Chat, error)
	ListManagedChats(context.Context, int) ([]Chat, error)
	JoinChat(context.Context, string) error
	ModerateUser(context.Context, ModerationRequest) error
	DeleteUserHistory(context.Context, int64, int64) error
	BlockUser(context.Context, int64) error
	UnblockUser(context.Context, int64) error
	ReportSpam(context.Context, int64) error
	DeletePrivateHistory(context.Context, int64) error
	GetPrivateChatSettings(context.Context, int64) (PrivateChatSettings, error)
	SetPrivateChatQuarantined(context.Context, int64, bool) error
	GetGlobalAutoArchive(context.Context) (bool, error)
	SetGlobalAutoArchive(context.Context, bool) error
	UpdateAccountUsername(context.Context, string) error
	CreateChannel(context.Context, string, string) (Chat, error)
	UpdateChatUsername(context.Context, int64, string) error
	DeleteChannel(context.Context, int64) error
	SendReaction(context.Context, int64, int, []Reaction, bool) error
	GetStickerSet(context.Context, string) (StickerSet, error)
	CreateStickerSet(context.Context, int64, string, string, Sticker) error
	AddStickerToSet(context.Context, string, Sticker) error
	RequestBotMedia(context.Context, BotMediaRequest) (Message, error)
	SendInlineBotResult(context.Context, InlineBotRequest) (SentMessage, error)
}
