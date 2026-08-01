package eat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

const (
	eatGIFBaseURL    = "https://raw.githubusercontent.com/ClearLuv/TeleBox_Plugins/refs/heads/beta/eatgif/"
	eatGIFTargetSize = 250 << 10
)

var eatGIFCRFLevels = []int{20, 26, 32, 38, 44, 50, 57, 63}

type gifListEntry struct {
	URL  string `json:"url"`
	Desc string `json:"desc"`
}

type gifFrame struct {
	URL   string `json:"url"`
	Delay int    `json:"delay"`
	Me    *role  `json:"me"`
	You   *role  `json:"you"`
}

type gifDetail struct {
	Width  int        `json:"width"`
	Height int        `json:"height"`
	Frames []gifFrame `json:"res"`
}

type GIFPlugin struct {
	services service.Container
	workDir  string
	cacheDir string

	mu   sync.Mutex
	list map[string]gifListEntry
}

func NewGIF(services service.Container) *GIFPlugin {
	cacheDir := filepath.Join(services.AssetsDir, "eatgif")
	if services.AssetsDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "telebox-go-eatgif-cache")
	}
	return &GIFPlugin{
		services: services,
		workDir:  filepath.Join(os.TempDir(), "telebox-go-eatgif"),
		cacheDir: cacheDir,
	}
}

func (p *GIFPlugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "eatgif",
		Version:     "0.3.2",
		Description: "将双方头像逐帧合成到动态表情模板",
	}
}

func (p *GIFPlugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "eatgif",
		Description: "生成头像融合动态贴纸",
		Usage: []string{
			"eatgif list",
			"eatgif <模板名>（回复用户）",
			"eatgif clear",
		},
		HelpHTML:  eatGIFGuideHTML,
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *GIFPlugin) Start(context.Context) error {
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return err
	}
	return os.MkdirAll(p.cacheDir, 0o700)
}

func (p *GIFPlugin) Stop(context.Context) error { return nil }

func (p *GIFPlugin) handle(ctx context.Context, request command.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(request.Args) > 0 {
		switch strings.ToLower(request.Args[0]) {
		case "help", "h":
			return p.respond(ctx, request, gifHelpText(request.Prefix))
		case "clear":
			return p.clear(ctx, request)
		}
	}
	if err := p.ensureList(ctx); err != nil {
		return p.respond(ctx, request, "❌ 加载动图配置失败："+err.Error())
	}
	if len(request.Args) == 0 || strings.EqualFold(request.Args[0], "list") ||
		strings.EqualFold(request.Args[0], "ls") {
		return p.respond(ctx, request, p.listText(request.Prefix))
	}
	key := strings.ToLower(strings.TrimSpace(request.Args[0]))
	selected, ok := p.list[key]
	if !ok {
		return p.respond(ctx, request, "❌ 未找到 "+key+"\n\n"+p.listText(request.Prefix))
	}
	if request.Message.ReplyToID == 0 {
		return p.respond(ctx, request, "❌ 请先回复一个用户的消息")
	}
	if _, err := p.services.Tools.LookPath("ffmpeg"); err != nil {
		return p.respond(ctx, request, "❌ 未找到 FFmpeg，请先安装并加入 PATH")
	}
	return p.generate(ctx, request, selected)
}

func (p *GIFPlugin) ensureList(ctx context.Context) error {
	if len(p.list) > 0 {
		return nil
	}
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		URL: eatGIFBaseURL + "config.json",
	})
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("配置源 HTTP %d", response.StatusCode)
	}
	var list map[string]gifListEntry
	if err := json.Unmarshal(response.Body, &list); err != nil {
		return fmt.Errorf("解析动图列表：%w", err)
	}
	if len(list) == 0 || len(list) > 100 {
		return errors.New("动图列表为空或过大")
	}
	for key, item := range list {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(item.URL) == "" ||
			strings.TrimSpace(item.Desc) == "" {
			return fmt.Errorf("动图 %q 配置不完整", key)
		}
		if _, err := safeRelativePath(item.URL); err != nil {
			return fmt.Errorf("动图 %q 路径无效：%w", key, err)
		}
	}
	p.list = list
	return nil
}

func (p *GIFPlugin) loadDetail(
	ctx context.Context,
	item gifListEntry,
) (gifDetail, error) {
	data, err := p.asset(ctx, item.URL)
	if err != nil {
		return gifDetail{}, err
	}
	var detail gifDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		return gifDetail{}, fmt.Errorf("解析逐帧配置：%w", err)
	}
	if detail.Width <= 0 || detail.Height <= 0 || detail.Width > 1024 ||
		detail.Height > 1024 {
		return gifDetail{}, errors.New("动图画布尺寸无效")
	}
	if len(detail.Frames) == 0 || len(detail.Frames) > 200 {
		return gifDetail{}, errors.New("动图帧数必须为 1–200")
	}
	for index, frame := range detail.Frames {
		if _, err := safeRelativePath(frame.URL); err != nil {
			return gifDetail{}, fmt.Errorf("第 %d 帧路径无效：%w", index+1, err)
		}
		if frame.Me == nil && frame.You == nil {
			return gifDetail{}, fmt.Errorf("第 %d 帧没有头像位置", index+1)
		}
		for _, configuredRole := range []*role{frame.Me, frame.You} {
			if configuredRole == nil {
				continue
			}
			if _, err := safeRelativePath(configuredRole.Mask); err != nil {
				return gifDetail{}, fmt.Errorf("第 %d 帧蒙版无效：%w", index+1, err)
			}
		}
	}
	return detail, nil
}

func (p *GIFPlugin) generate(
	ctx context.Context,
	request command.Request,
	item gifListEntry,
) error {
	if err := p.respond(ctx, request, "⏳ 加载「"+item.Desc+"」模板…"); err != nil {
		return err
	}
	detail, err := p.loadDetail(ctx, item)
	if err != nil {
		return p.respond(ctx, request, "❌ 加载模板失败："+err.Error())
	}
	messages, err := p.services.Telegram.GetMessages(
		ctx, request.Message.ChatID, []int{request.Message.ReplyToID},
	)
	if err != nil || len(messages) == 0 || messages[0].SenderID <= 0 {
		return p.respond(ctx, request, "❌ 回复消息没有可识别的用户")
	}
	you, err := p.profileImage(ctx, messages[0].SenderID)
	if err != nil {
		return p.respond(ctx, request, "❌ 获取目标头像失败："+err.Error())
	}
	meID := request.Message.SenderID
	if meID <= 0 {
		me, resolveErr := p.services.Telegram.ResolveUser(ctx, "me")
		if resolveErr != nil {
			return p.respond(ctx, request, "❌ 获取当前账号失败："+resolveErr.Error())
		}
		meID = me.ID
	}
	me, err := p.profileImage(ctx, meID)
	if err != nil {
		return p.respond(ctx, request, "❌ 获取当前账号头像失败："+err.Error())
	}

	jobDir, err := os.MkdirTemp(p.workDir, "job-*")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建逐帧目录失败："+err.Error())
	}
	defer func() {
		if cleanupErr := os.RemoveAll(jobDir); cleanupErr != nil {
			p.services.Logger.Warn("remove eatgif job", "path", jobDir, "error", cleanupErr)
		}
	}()
	instructions := make([]string, 0, len(detail.Frames)*2+1)
	totalDuration := time.Duration(0)
	for index, frame := range detail.Frames {
		canvas, err := p.composeFrame(ctx, frame, you, me)
		if err != nil {
			return p.respond(ctx, request,
				fmt.Sprintf("❌ 第 %d 帧合成失败：%s", index+1, err))
		}
		canvas = fitWithin(canvas, 512, 512)
		frameName := fmt.Sprintf("frame-%03d.png", index)
		framePath := filepath.Join(jobDir, frameName)
		output, err := os.OpenFile(
			framePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600,
		)
		if err != nil {
			return p.respond(ctx, request, "❌ 创建帧文件失败："+err.Error())
		}
		encodeErr := png.Encode(output, canvas)
		closeErr := output.Close()
		if encodeErr != nil {
			return p.respond(ctx, request, "❌ 编码帧失败："+encodeErr.Error())
		}
		if closeErr != nil {
			return p.respond(ctx, request, "❌ 保存帧失败："+closeErr.Error())
		}
		delay := frame.Delay
		if delay <= 0 {
			delay = 100
		}
		if delay > 5000 {
			return p.respond(ctx, request, "❌ 帧延迟超过 5 秒")
		}
		totalDuration += time.Duration(delay) * time.Millisecond
		instructions = append(instructions,
			"file '"+frameName+"'",
			"duration "+strconv.FormatFloat(float64(delay)/1000, 'f', 3, 64),
		)
	}
	lastFrame := fmt.Sprintf("frame-%03d.png", len(detail.Frames)-1)
	instructions = append(instructions, "file '"+lastFrame+"'")
	instructionPath := filepath.Join(jobDir, "frames.txt")
	if err := os.WriteFile(
		instructionPath, []byte(strings.Join(instructions, "\n")+"\n"), 0o600,
	); err != nil {
		return p.respond(ctx, request, "❌ 写入帧序列失败："+err.Error())
	}
	outputPath := filepath.Join(jobDir, "output.webm")
	if err := p.encode(ctx, jobDir, outputPath, totalDuration); err != nil {
		return p.respond(ctx, request, "❌ 生成动态贴纸失败："+err.Error())
	}
	_, err = p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:         outputPath,
		FileName:     "eatgif.webm",
		MIMEType:     "video/webm",
		ReplyToID:    request.Message.ReplyToID,
		Kind:         telegram.MediaSticker,
		Duration:     min(totalDuration, 3*time.Second),
		StickerEmoji: "✨",
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 发送动态贴纸失败："+err.Error())
	}
	if request.Message.Outgoing {
		return p.services.Telegram.DeleteMessages(
			ctx, request.Message.ChatID, []int{request.Message.ID},
		)
	}
	return nil
}

func (p *GIFPlugin) composeFrame(
	ctx context.Context,
	frame gifFrame,
	you image.Image,
	me image.Image,
) (*image.NRGBA, error) {
	frameBytes, err := p.asset(ctx, frame.URL)
	if err != nil {
		return nil, err
	}
	base, _, err := image.Decode(bytes.NewReader(frameBytes))
	if err != nil {
		return nil, fmt.Errorf("解码背景：%w", err)
	}
	canvas := cloneNRGBA(base)
	if frame.You != nil {
		if err := p.applyRole(ctx, canvas, you, *frame.You); err != nil {
			return nil, err
		}
	}
	if frame.Me != nil {
		if err := p.applyRole(ctx, canvas, me, *frame.Me); err != nil {
			return nil, err
		}
	}
	return canvas, nil
}

func (p *GIFPlugin) applyRole(
	ctx context.Context,
	canvas *image.NRGBA,
	avatar image.Image,
	position role,
) error {
	maskBytes, err := p.asset(ctx, position.Mask)
	if err != nil {
		return err
	}
	mask, _, err := image.Decode(bytes.NewReader(maskBytes))
	if err != nil {
		return err
	}
	width, height := mask.Bounds().Dx(), mask.Bounds().Dy()
	if width <= 0 || height <= 0 || width > 2048 || height > 2048 {
		return errors.New("蒙版尺寸无效")
	}
	icon := resizeCover(avatar, width, height)
	if position.Flip {
		icon = flipHorizontal(icon)
	}
	if position.Rotate != 0 {
		icon = centerCrop(rotate(icon, position.Rotate), width, height)
	}
	if position.Brightness > 0 && position.Brightness != 1 {
		icon = adjustBrightness(icon, position.Brightness)
	}
	masked := maskImage(icon, mask)
	drawOver(canvas, masked, position.X, position.Y)
	return nil
}

func (p *GIFPlugin) encode(
	ctx context.Context,
	jobDir string,
	outputPath string,
	duration time.Duration,
) error {
	filter := eatGIFFilter(duration)
	var lastErr error
	for _, crf := range eatGIFCRFLevels {
		result, err := p.services.Tools.Run(ctx, toolrunner.Command{
			Name: "ffmpeg",
			Args: []string{
				"-hide_banner", "-loglevel", "error", "-y",
				"-f", "concat", "-safe", "0", "-i", "frames.txt",
				"-vf", filter,
				"-c:v", "libvpx-vp9",
				"-pix_fmt", "yuva420p",
				"-auto-alt-ref", "0",
				"-b:v", "0",
				"-crf", strconv.Itoa(crf),
				"-an",
				"-t", eatGIFFormatSeconds(min(duration, 3*time.Second)),
				outputPath,
			},
			Directory: jobDir,
			Timeout:   5 * time.Minute,
			MaxOutput: 128 << 10,
		})
		if err != nil {
			lastErr = errors.New(eatGIFShortDetail(result.Stderr, err))
			continue
		}
		info, statErr := os.Stat(outputPath)
		if statErr == nil && info.Size() > 0 && info.Size() <= eatGIFTargetSize {
			if p.services.Logger != nil {
				p.services.Logger.Info(
					"eatgif encoded",
					"crf", crf,
					"bytes", info.Size(),
				)
			}
			return nil
		}
		if statErr == nil {
			lastErr = fmt.Errorf("输出仍为 %.1f KiB", float64(info.Size())/1024)
		}
	}
	return fallbackError(lastErr, "最低质量下仍无法压缩到 250 KiB")
}

func eatGIFFilter(duration time.Duration) string {
	if duration > 3*time.Second {
		return fmt.Sprintf("setpts=%.8f*PTS", 3/duration.Seconds())
	}
	return "null"
}

func (p *GIFPlugin) asset(ctx context.Context, relative string) ([]byte, error) {
	clean, err := safeRelativePath(relative)
	if err != nil {
		return nil, err
	}
	localPath := filepath.Join(p.cacheDir, clean)
	if data, err := os.ReadFile(localPath); err == nil {
		return data, nil
	}
	base, _ := url.Parse(eatGIFBaseURL)
	remote := base.ResolveReference(&url.URL{Path: strings.ReplaceAll(clean, `\`, "/")})
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{URL: remote.String()})
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("资源 HTTP %d", response.StatusCode)
	}
	if len(response.Body) == 0 {
		return nil, errors.New("资源为空")
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(localPath, response.Body, 0o600); err != nil {
		return nil, err
	}
	return response.Body, nil
}

func (p *GIFPlugin) clear(ctx context.Context, request command.Request) error {
	absolute, err := filepath.Abs(p.cacheDir)
	if err != nil {
		return p.respond(ctx, request, "❌ 无法确认缓存目录："+err.Error())
	}
	assetsRoot, rootErr := filepath.Abs(p.services.AssetsDir)
	if p.services.AssetsDir != "" && (rootErr != nil || !isPathInside(assetsRoot, absolute)) {
		return p.respond(ctx, request, "❌ 缓存目录越界，拒绝清理")
	}
	if err := os.RemoveAll(absolute); err != nil {
		return p.respond(ctx, request, "❌ 清理缓存失败："+err.Error())
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return p.respond(ctx, request, "❌ 重建缓存目录失败："+err.Error())
	}
	p.list = nil
	if err := p.ensureList(ctx); err != nil {
		return p.respond(ctx, request, "⚠️ 缓存已清理，但刷新配置失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ eatgif 资源缓存已清理并刷新")
}

func (p *GIFPlugin) profileImage(ctx context.Context, userID int64) (image.Image, error) {
	var data bytes.Buffer
	if err := p.services.Telegram.DownloadProfilePhoto(
		ctx, userID, &boundedWriter{Writer: &data, Remaining: maxImageBytes},
	); err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(data.Bytes()))
	return decoded, err
}

func (p *GIFPlugin) listText(prefix string) string {
	keys := make([]string, 0, len(p.list))
	for key := range p.list {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var result strings.Builder
	result.WriteString("🧩 可用动态表情\n\n")
	for _, key := range keys {
		fmt.Fprintf(&result, "%s - %s\n", key, p.list[key].Desc)
	}
	result.WriteString("\n回复用户后使用 " + prefix + "eatgif <名称>")
	return strings.TrimSpace(result.String())
}

func (p *GIFPlugin) respond(
	ctx context.Context,
	request command.Request,
	text string,
) error {
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

func safeRelativePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	normalized := strings.ReplaceAll(value, `\`, "/")
	if normalized == "" || path.IsAbs(normalized) {
		return "", errors.New("路径为空或为绝对路径")
	}
	firstSegment := strings.SplitN(normalized, "/", 2)[0]
	if strings.Contains(firstSegment, ":") {
		return "", errors.New("路径为空或为绝对路径")
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("路径越界")
	}
	return filepath.FromSlash(clean), nil
}

func isPathInside(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func drawOver(canvas *image.NRGBA, overlay image.Image, x, y int) {
	draw.Draw(
		canvas,
		image.Rect(x, y, x+overlay.Bounds().Dx(), y+overlay.Bounds().Dy()),
		overlay,
		overlay.Bounds().Min,
		draw.Over,
	)
}

func fallbackError(err error, message string) error {
	if err != nil {
		return err
	}
	return errors.New(message)
}

func eatGIFFormatSeconds(duration time.Duration) string {
	return strconv.FormatFloat(duration.Seconds(), 'f', 3, 64)
}

func eatGIFShortDetail(stderr string, err error) string {
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

func gifHelpText(prefix string) string {
	return "🧩 头像融合动态贴纸\n\n" +
		prefix + "eatgif list\n" +
		"回复用户后发送 " + prefix + "eatgif <名称>\n" +
		prefix + "eatgif clear  清理帧资源缓存\n\n" +
		"模板帧会缓存在 data/assets/eatgif；输出自动压缩至 Telegram 贴纸限制。"
}

const eatGIFGuideHTML = `<b>🧩 头像融合动态贴纸</b>

<code>{{prefix}}eatgif list</code> 查看动态模板列表
回复目标用户的消息后发送 <code>{{prefix}}eatgif &lt;模板名&gt;</code>
<code>{{prefix}}eatgif clear</code> 清理已缓存的模板帧

模板配置和帧资源会按需下载，帧文件保存在 <code>data/assets/eatgif</code>。生成结果会转换并压缩为 Telegram 支持的视频贴纸。`
