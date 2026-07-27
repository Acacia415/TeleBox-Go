package gif

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

const (
	maxFileSize         = 50 << 20
	maxOriginalDuration = 10 * time.Second
	targetDuration      = 3 * time.Second
	targetStickerSize   = 250 << 10
)

var crfLevels = []int{30, 35, 40, 45, 51, 57, 63}

type Plugin struct {
	services service.Container
	workDir  string
	mu       sync.Mutex
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services: services,
		workDir:  filepath.Join(os.TempDir(), "telebox-go-gif"),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "gif",
		Version:     "0.2.0",
		Description: "将 GIF 或短视频自适应压缩为 Telegram 视频贴纸",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "gif",
		Description: "将回复的 GIF 或视频转换为贴纸",
		Usage: []string{
			"gif（回复 GIF 或视频）",
			"gif clear",
		},
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error {
	return os.MkdirAll(p.workDir, 0o700)
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if len(request.Args) > 0 {
		switch strings.ToLower(request.Args[0]) {
		case "help", "h":
			return p.respond(ctx, request, helpText(request.Prefix))
		case "clear", "c":
			return p.clear(ctx, request)
		}
	}
	if request.Message.ReplyToID == 0 {
		return p.respond(ctx, request,
			"❌ 请回复 GIF 或视频后使用此命令\n\n"+helpText(request.Prefix))
	}
	return p.convert(ctx, request)
}

func (p *Plugin) convert(ctx context.Context, request command.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := p.services.Tools.LookPath("ffmpeg"); err != nil {
		return p.respond(ctx, request, "❌ 未找到 FFmpeg，请先安装并加入 PATH")
	}
	messages, err := p.services.Telegram.GetMessages(
		ctx, request.Message.ChatID, []int{request.Message.ReplyToID},
	)
	if err != nil || len(messages) == 0 || messages[0].Media == nil {
		return p.respond(ctx, request, "❌ 无法读取回复的媒体")
	}
	source := messages[0]
	if !supported(source.Media) {
		return p.respond(ctx, request, "❌ 仅支持 GIF、动画和视频文件")
	}
	if source.Media.Size > maxFileSize {
		return p.respond(ctx, request, fmt.Sprintf(
			"❌ 文件为 %.1f MiB，超过 50 MiB 限制",
			float64(source.Media.Size)/(1<<20),
		))
	}
	if source.Media.Duration > maxOriginalDuration {
		return p.respond(ctx, request, fmt.Sprintf(
			"❌ 原始时长 %.1f 秒，超过 10 秒限制",
			source.Media.Duration.Seconds(),
		))
	}
	jobDir, err := os.MkdirTemp(p.workDir, "job-*")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建临时目录失败："+err.Error())
	}
	defer func() {
		if cleanupErr := os.RemoveAll(jobDir); cleanupErr != nil {
			p.services.Logger.Warn("remove gif job", "path", jobDir, "error", cleanupErr)
		}
	}()
	inputPath := filepath.Join(jobDir, "input"+safeExtension(source.Media.FileName))
	outputPath := filepath.Join(jobDir, "sticker.webm")
	if err := p.respond(ctx, request, "⏳ 下载媒体…"); err != nil {
		return err
	}
	input, err := os.OpenFile(inputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return p.respond(ctx, request, "❌ 创建临时文件失败："+err.Error())
	}
	downloadedMedia, downloadErr := p.services.Telegram.DownloadMedia(
		ctx, request.Message.ChatID, source.ID, input,
	)
	closeErr := input.Close()
	if downloadErr != nil {
		return p.respond(ctx, request, "❌ 下载媒体失败："+downloadErr.Error())
	}
	if closeErr != nil {
		return p.respond(ctx, request, "❌ 保存媒体失败："+closeErr.Error())
	}
	duration := source.Media.Duration
	if duration <= 0 {
		duration = downloadedMedia.Duration
	}
	if duration <= 0 {
		duration = p.probeDuration(ctx, inputPath)
	}
	if duration <= 0 {
		return p.respond(ctx, request, "❌ 无法识别媒体时长")
	}
	if duration > maxOriginalDuration {
		return p.respond(ctx, request, fmt.Sprintf(
			"❌ 原始时长 %.1f 秒，超过 10 秒限制",
			duration.Seconds(),
		))
	}
	if err := p.compress(ctx, request, inputPath, outputPath, duration); err != nil {
		return p.respond(ctx, request, "❌ 转换失败："+err.Error())
	}
	if err := p.respond(ctx, request, "⏳ 发送视频贴纸…"); err != nil {
		return err
	}
	_, err = p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:         outputPath,
		FileName:     "sticker.webm",
		MIMEType:     "video/webm",
		ReplyToID:    request.Message.ID,
		Kind:         telegram.MediaSticker,
		Duration:     min(duration, targetDuration),
		StickerEmoji: randomEmoji(),
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 发送贴纸失败："+err.Error())
	}
	if request.Message.Outgoing {
		return p.services.Telegram.DeleteMessages(
			ctx, request.Message.ChatID, []int{request.Message.ID},
		)
	}
	return nil
}

func (p *Plugin) compress(
	ctx context.Context,
	request command.Request,
	inputPath string,
	outputPath string,
	duration time.Duration,
) error {
	filter := videoFilter(duration)
	var lastErr error
	for index, crf := range crfLevels {
		if err := p.respond(ctx, request, fmt.Sprintf(
			"⏳ 自适应压缩（%d/%d，CRF %d）…",
			index+1, len(crfLevels), crf,
		)); err != nil {
			return err
		}
		result, err := p.services.Tools.Run(ctx, toolrunner.Command{
			Name: "ffmpeg",
			Args: []string{
				"-hide_banner", "-loglevel", "error",
				"-i", inputPath,
				"-an",
				"-c:v", "libvpx-vp9",
				"-pix_fmt", "yuva420p",
				"-auto-alt-ref", "0",
				"-b:v", "0",
				"-crf", strconv.Itoa(crf),
				"-vf", filter,
				"-t", formatSeconds(min(duration, targetDuration)),
				"-y", outputPath,
			},
			Timeout:   3 * time.Minute,
			MaxOutput: 64 << 10,
		})
		if err != nil {
			lastErr = errors.New(shortDetail(result.Stderr, err))
			continue
		}
		info, statErr := os.Stat(outputPath)
		if statErr == nil && info.Size() > 0 && info.Size() <= targetStickerSize {
			return nil
		}
		if statErr == nil {
			lastErr = fmt.Errorf("CRF %d 输出仍为 %.1f KiB",
				crf, float64(info.Size())/1024)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("最低质量下仍无法压缩到 250 KiB")
	}
	return lastErr
}

func (p *Plugin) probeDuration(ctx context.Context, path string) time.Duration {
	if _, err := p.services.Tools.LookPath("ffprobe"); err != nil {
		return 0
	}
	result, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name: "ffprobe",
		Args: []string{
			"-v", "error",
			"-show_entries", "format=duration",
			"-of", "default=noprint_wrappers=1:nokey=1",
			path,
		},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return 0
	}
	seconds, _ := strconv.ParseFloat(strings.TrimSpace(result.Stdout), 64)
	return time.Duration(seconds * float64(time.Second))
}

func (p *Plugin) clear(ctx context.Context, request command.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	entries, _ := os.ReadDir(p.workDir)
	if err := os.RemoveAll(p.workDir); err != nil {
		return p.respond(ctx, request, "❌ 清理临时文件失败："+err.Error())
	}
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return p.respond(ctx, request, "❌ 重建临时目录失败："+err.Error())
	}
	return p.respond(ctx, request, fmt.Sprintf("✅ 已清理 %d 个临时任务", len(entries)))
}

func (p *Plugin) respond(ctx context.Context, request command.Request, text string) error {
	if request.Message.Outgoing {
		_, err := p.services.Telegram.EditText(
			ctx, request.Message.ChatID, request.Message.ID, text,
		)
		return err
	}
	_, err := p.services.Telegram.ReplyText(
		ctx, request.Message.ChatID, request.Message.ID, text,
	)
	return err
}

func supported(media *telegram.Media) bool {
	if media == nil {
		return false
	}
	switch media.Kind {
	case telegram.MediaVideo, telegram.MediaAnimation:
		return true
	case telegram.MediaDocument:
		mime := strings.ToLower(media.MIMEType)
		extension := strings.ToLower(filepath.Ext(media.FileName))
		return strings.HasPrefix(mime, "video/") || mime == "image/gif" ||
			slicesContains([]string{".gif", ".mp4", ".webm", ".mov", ".avi", ".mkv"}, extension)
	default:
		return false
	}
}

func videoFilter(duration time.Duration) string {
	parts := make([]string, 0, 3)
	if duration > targetDuration {
		parts = append(parts, fmt.Sprintf(
			"setpts=%.8f*PTS",
			targetDuration.Seconds()/duration.Seconds(),
		))
	}
	parts = append(parts,
		"scale=512:512:force_original_aspect_ratio=decrease",
		"fps=30",
	)
	return strings.Join(parts, ",")
}

func safeExtension(fileName string) string {
	extension := strings.ToLower(filepath.Ext(fileName))
	for _, allowed := range []string{".gif", ".mp4", ".webm", ".mov", ".avi", ".mkv"} {
		if extension == allowed {
			return extension
		}
	}
	return ".bin"
}

func slicesContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func formatSeconds(duration time.Duration) string {
	return strconv.FormatFloat(duration.Seconds(), 'f', 3, 64)
}

func shortDetail(stderr string, err error) string {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = err.Error()
	}
	runes := []rune(detail)
	if len(runes) > 400 {
		return string(runes[:400]) + "…"
	}
	return detail
}

func randomEmoji() string {
	return stickerEmojis[rand.IntN(len(stickerEmojis))]
}

func helpText(prefix string) string {
	return "🎭 GIF / 视频转贴纸\n\n" +
		"回复 GIF 或视频后发送 " + prefix + "gif\n\n" +
		"限制：原文件 ≤ 50 MiB、原时长 ≤ 10 秒；输出会加速至最多 3 秒，" +
		"自动迭代压缩至 ≤ 250 KiB、最长边 ≤ 512px、30 FPS。\n\n" +
		prefix + "gif clear  清理临时文件"
}

var stickerEmojis = []string{
	"😀", "😁", "😂", "😎", "😍", "🤔", "😐", "🙄", "😢", "😭",
	"😱", "🤯", "😴", "🥳", "🥰", "🤩", "🐶", "🐱", "🐼", "🦊",
	"❤️", "💛", "💚", "💙", "💜", "🔥", "✨", "💫", "💥", "👉",
}
