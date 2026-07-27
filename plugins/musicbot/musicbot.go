package musicbot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const (
	defaultBot = "@music_v1bot"
	vkBot      = "@vkmusic_bot"
	ymBot      = "@ttaudiobot"
)

var commandActions = map[string]string{
	"mbs":  "search",
	"mbkw": "kuwo",
	"mbkg": "kugou",
	"mbqq": "qq",
	"mbne": "netease",
	"mbvk": "vk",
	"mbym": "ym",
}

type Plugin struct {
	services service.Container
	workDir  string
	locks    map[string]*sync.Mutex
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services: services,
		workDir:  filepath.Join(os.TempDir(), "telebox-go-music-bot"),
		locks: map[string]*sync.Mutex{
			defaultBot: {},
			vkBot:      {},
			ymBot:      {},
		},
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "music_bot",
		Version:     "0.1.0",
		Description: "通过 Telegram 音乐机器人搜索并发送多音源音乐",
	}
}

func (p *Plugin) Commands() []command.Definition {
	definitions := []command.Definition{{
		Name:        "music_bot",
		Description: "使用指定音源搜索音乐",
		OwnerOnly:   true,
		Handler:     p.handleMain,
	}}
	for _, name := range []string{"mbs", "mbkw", "mbkg", "mbqq", "mbne", "mbvk", "mbym"} {
		action := commandActions[name]
		definitions = append(definitions, command.Definition{
			Name:        name,
			Description: "快捷搜索音乐（" + action + "）",
			OwnerOnly:   true,
			Handler: func(ctx context.Context, request command.Request) error {
				return p.search(ctx, request, action, request.RawArgs)
			},
		})
	}
	return definitions
}

func (p *Plugin) Start(context.Context) error {
	return os.MkdirAll(p.workDir, 0o700)
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handleMain(ctx context.Context, request command.Request) error {
	if len(request.Args) < 2 {
		return p.respond(ctx, request, helpText(request.Prefix))
	}
	action := strings.ToLower(request.Args[0])
	keyword := strings.TrimSpace(strings.TrimPrefix(request.RawArgs, request.Args[0]))
	return p.search(ctx, request, action, keyword)
}

func (p *Plugin) search(
	ctx context.Context,
	request command.Request,
	action string,
	keyword string,
) error {
	bot, query, display, ok := buildQuery(action, keyword)
	if !ok {
		return p.respond(ctx, request, helpText(request.Prefix))
	}
	if err := p.respond(ctx, request, "🔎 搜索中："+display); err != nil {
		return err
	}

	lock := p.locks[bot]
	lock.Lock()
	defer lock.Unlock()

	mediaMessage, err := p.services.Telegram.RequestBotMedia(
		ctx,
		telegram.BotMediaRequest{
			Bot:     bot,
			Query:   query,
			Timeout: 35 * time.Second,
		},
	)
	if err != nil {
		return p.respond(ctx, request,
			"❌ 音乐机器人未响应："+friendlyError(err, bot))
	}
	if mediaMessage.Media == nil {
		return p.respond(ctx, request, "❌ 音乐机器人没有返回可发送的媒体")
	}

	if err := p.reupload(ctx, request, mediaMessage, action, display); err != nil {
		// A direct Telegram-side copy can still succeed when the media cannot
		// be downloaded locally (for example, an unusual document subtype).
		if copyErr := p.services.Telegram.CopyMessages(
			ctx,
			mediaMessage.ChatID,
			request.Message.ChatID,
			[]int{mediaMessage.ID},
		); copyErr != nil {
			return p.respond(ctx, request,
				"❌ 发送音乐失败："+friendlyError(errors.Join(err, copyErr), bot))
		}
	}
	if request.Message.Outgoing {
		return p.services.Telegram.DeleteMessages(
			ctx,
			request.Message.ChatID,
			[]int{request.Message.ID},
		)
	}
	return nil
}

func (p *Plugin) reupload(
	ctx context.Context,
	request command.Request,
	message telegram.Message,
	action string,
	display string,
) error {
	if message.Media.Size > 512<<20 {
		return fmt.Errorf("音乐文件超过 512 MiB 限制")
	}
	jobDir, err := os.MkdirTemp(p.workDir, "job-*")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(jobDir); cleanupErr != nil {
			p.services.Logger.Warn(
				"remove music bot job",
				"path", jobDir,
				"error", cleanupErr,
			)
		}
	}()

	fileName := mediaFilename(*message.Media)
	path := filepath.Join(jobDir, fileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	downloaded, downloadErr := p.services.Telegram.DownloadMedia(
		ctx,
		message.ChatID,
		message.ID,
		file,
	)
	closeErr := file.Close()
	if downloadErr != nil {
		return downloadErr
	}
	if closeErr != nil {
		return closeErr
	}
	if downloaded.FileName != "" {
		fileName = safeFilename(downloaded.FileName)
	}
	caption := ""
	if action != "ym" {
		caption = "🎵 " + display
	}
	kind := downloaded.Kind
	if kind == "" {
		kind = message.Media.Kind
	}
	_, err = p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:       path,
		FileName:   fileName,
		MIMEType:   firstNonEmpty(downloaded.MIMEType, message.Media.MIMEType),
		Caption:    caption,
		ReplyToID:  request.Message.ReplyToID,
		Kind:       kind,
		Width:      downloaded.Width,
		Height:     downloaded.Height,
		Duration:   downloaded.Duration,
		AudioTitle: display,
	})
	return err
}

func buildQuery(action, keyword string) (bot, query, display string, ok bool) {
	action = strings.ToLower(strings.TrimSpace(action))
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return "", "", "", false
	}
	switch action {
	case "search", "kugou", "kuwo", "qq", "netease":
		return defaultBot, "/" + action + " " + keyword, keyword, true
	case "vk":
		return vkBot, keyword, keyword, true
	case "ym":
		// Keep the query suffix used by the backed-up plugin while presenting
		// the original user input in progress and caption text.
		return ymBot, keyword + " lyric】", keyword, true
	default:
		return "", "", "", false
	}
}

func mediaFilename(media telegram.Media) string {
	if name := safeFilename(media.FileName); name != "" {
		return name
	}
	extension := ".bin"
	switch media.Kind {
	case telegram.MediaAudio:
		extension = ".mp3"
	case telegram.MediaVoice:
		extension = ".ogg"
	case telegram.MediaVideo, telegram.MediaVideoNote:
		extension = ".mp4"
	case telegram.MediaAnimation:
		extension = ".gif"
	case telegram.MediaPhoto:
		extension = ".jpg"
	}
	return "music" + extension
}

func safeFilename(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	var result strings.Builder
	for _, char := range value {
		if unicode.IsControl(char) || strings.ContainsRune(`\/:*?"<>|`, char) {
			continue
		}
		if result.Len() >= 180 {
			break
		}
		result.WriteRune(char)
	}
	return strings.Trim(result.String(), ". ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func friendlyError(err error, bot string) string {
	if errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "等待超时，请先打开 " + bot + " 并点击 Start 后重试"
	}
	return err.Error()
}

func (p *Plugin) respond(ctx context.Context, request command.Request, text string) error {
	if request.Message.Outgoing {
		_, err := p.services.Telegram.EditText(
			ctx,
			request.Message.ChatID,
			request.Message.ID,
			text,
		)
		return err
	}
	_, err := p.services.Telegram.ReplyText(
		ctx,
		request.Message.ChatID,
		request.Message.ID,
		text,
	)
	return err
}

func helpText(prefix string) string {
	return "🎵 多音源音乐搜索\n\n" +
		prefix + "mbs <关键词>  自动选择音源\n" +
		prefix + "mbkg <关键词>  酷狗\n" +
		prefix + "mbkw <关键词>  酷我\n" +
		prefix + "mbqq <关键词>  QQ 音乐\n" +
		prefix + "mbne <关键词>  网易云音乐\n" +
		prefix + "mbvk <关键词>  VK Music\n" +
		prefix + "mbym <关键词>  YouTube Music\n\n" +
		"也可使用 " + prefix +
		"music_bot <search|kugou|kuwo|qq|netease|vk|ym> <关键词>"
}
