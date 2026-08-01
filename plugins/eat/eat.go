package eat

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/HugoSmits86/nativewebp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	defaultConfigURL  = "https://raw.githubusercontent.com/TeleBoxOrg/TeleBox_Plugins/refs/heads/main/eat/config.json"
	maxImageBytes     = 8 << 20
	stickerFileName   = "output.webp"
	stickerMIMEType   = "image/webp"
	stickerTrimAlpha  = 8
	dominantAlphaRate = 0.75
	diagnosticName    = "eat2-diagnostic.zip"
)

type role struct {
	X          int     `json:"x"`
	Y          int     `json:"y"`
	Mask       string  `json:"mask"`
	Brightness float64 `json:"brightness"`
	Rotate     float64 `json:"rotate"`
	Flip       bool    `json:"flip"`
}

type stamp struct {
	Size    *int     `json:"size"`
	Scale   *float64 `json:"scale"`
	Rotate  *float64 `json:"rotate"`
	Opacity *float64 `json:"opacity"`
}

type entry struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Me    *role  `json:"me"`
	You   *role  `json:"you"`
	Stamp *stamp `json:"stamp"`
}

type configDocument struct {
	Resources map[string]entry `json:"resources"`
}

type loadedConfig struct {
	URL       string
	AssetBase string
	Entries   map[string]entry
}

type Plugin struct {
	services service.Container
	assetDir string
	workDir  string

	mu         sync.Mutex
	cfg        loadedConfig
	assetCache map[string][]byte
}

func New(services service.Container) *Plugin {
	assetDir := filepath.Join(services.AssetsDir, "eat")
	if services.AssetsDir == "" {
		assetDir = filepath.Join(os.TempDir(), "telebox-go-eat-assets")
	}
	return &Plugin{
		services:   services,
		assetDir:   assetDir,
		workDir:    filepath.Join(os.TempDir(), "telebox-go-eat"),
		assetCache: make(map[string][]byte),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "eat",
		Version:     "0.3.7",
		Description: "使用头像或回复图片制作静态表情",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{
		{
			Name:        "eat",
			Description: "把回复用户的头像套入表情模板",
			Usage: []string{
				"eat [模板名]（回复用户）",
				"eat set [配置地址]",
				"eat clear",
			},
			HelpHTML:  eatGuideHTML,
			OwnerOnly: true,
			Handler: func(ctx context.Context, request command.Request) error {
				return p.handle(ctx, request, false)
			},
		},
		{
			Name:        "eat2",
			Description: "把回复消息中的图片套入表情模板",
			Usage: []string{
				"eat2 [模板名]（回复图片）",
				"eat2 debug <模板名>（回复图片）",
				"eat2 set [配置地址]",
				"eat2 clear",
			},
			HelpHTML:  eatGuideHTML,
			OwnerOnly: true,
			Handler: func(ctx context.Context, request command.Request) error {
				return p.handle(ctx, request, true)
			},
		},
	}
}

func (p *Plugin) Start(context.Context) error {
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return err
	}
	return os.MkdirAll(p.assetDir, 0o700)
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(
	ctx context.Context,
	request command.Request,
	useMedia bool,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(request.Args) > 0 && strings.EqualFold(request.Args[0], "set") {
		configURL := defaultConfigURL
		if len(request.Args) > 1 {
			configURL = strings.TrimSpace(request.Args[1])
		}
		return p.refresh(ctx, request, configURL)
	}
	if len(request.Args) > 0 && (strings.EqualFold(request.Args[0], "clear") ||
		strings.EqualFold(request.Args[0], "c")) {
		p.cfg = loadedConfig{}
		clear(p.assetCache)
		return p.respond(ctx, request, "✅ 表情配置和临时缓存已清理")
	}
	if err := p.ensureConfig(ctx); err != nil {
		return p.respond(ctx, request, "❌ 加载表情配置失败："+err.Error())
	}
	if useMedia && len(request.Args) > 0 && strings.EqualFold(request.Args[0], "debug") {
		if request.Message.ReplyToID == 0 || len(request.Args) < 2 {
			return p.respond(ctx, request,
				"❌ 请回复图片或贴纸后使用 "+request.Prefix+"eat2 debug <模板名>")
		}
		key := strings.ToLower(strings.TrimSpace(request.Args[1]))
		selected, ok := p.cfg.Entries[key]
		if !ok {
			return p.respond(ctx, request, "❌ 找不到表情 "+key)
		}
		return p.diagnose(ctx, request, key, selected)
	}
	if request.Message.ReplyToID == 0 {
		return p.respond(ctx, request, p.listText(request.Prefix))
	}
	key := ""
	if len(request.Args) > 0 {
		key = strings.ToLower(strings.TrimSpace(request.Args[0]))
	}
	if key == "" {
		keys := sortedKeys(p.cfg.Entries)
		key = keys[rand.IntN(len(keys))]
	}
	selected, ok := p.cfg.Entries[key]
	if !ok {
		return p.respond(ctx, request,
			"❌ 找不到表情 "+key+"\n\n"+p.listText(request.Prefix))
	}
	return p.generate(ctx, request, selected, useMedia)
}

type diagnosticSource struct {
	Label string
	Media telegram.Media
	Data  []byte
	Err   error
}

func (p *Plugin) diagnose(
	ctx context.Context,
	request command.Request,
	key string,
	selected entry,
) error {
	if err := p.respond(ctx, request, "⏳ 正在生成 eat2 诊断包…"); err != nil {
		return err
	}
	messages, err := p.services.Telegram.GetMessages(
		ctx, request.Message.ChatID, []int{request.Message.ReplyToID},
	)
	if err != nil || len(messages) == 0 {
		return p.respond(ctx, request, "❌ 无法读取回复消息")
	}
	replied := messages[0]
	if replied.Media == nil {
		return p.respond(ctx, request, "❌ 回复消息不包含图片或贴纸")
	}

	full := diagnosticSource{Label: "full"}
	var fullData bytes.Buffer
	full.Media, full.Err = p.services.Telegram.DownloadMedia(
		ctx,
		request.Message.ChatID,
		replied.ID,
		&boundedWriter{Writer: &fullData, Remaining: maxImageBytes},
	)
	full.Data = append([]byte(nil), fullData.Bytes()...)

	preview := diagnosticSource{Label: "preview"}
	var previewData bytes.Buffer
	preview.Media, preview.Err = p.services.Telegram.DownloadMediaPreview(
		ctx,
		request.Message.ChatID,
		replied.ID,
		&boundedWriter{Writer: &previewData, Remaining: maxImageBytes},
	)
	preview.Data = append([]byte(nil), previewData.Bytes()...)

	if full.Err != nil && preview.Err != nil {
		return p.respond(ctx, request,
			"❌ 完整媒体和预览图均下载失败，请查看服务日志")
	}

	files := make(map[string][]byte)
	var report strings.Builder
	fmt.Fprintf(&report, "eat plugin: 0.3.7\n")
	fmt.Fprintf(&report, "template: %s (%s)\n", key, selected.Name)
	fmt.Fprintf(&report, "message media: kind=%s mime=%s size=%d declared=%dx%d\n",
		replied.Media.Kind,
		replied.Media.MIMEType,
		replied.Media.Size,
		replied.Media.Width,
		replied.Media.Height,
	)
	if selected.You != nil {
		fmt.Fprintf(&report, "you role: x=%d y=%d mask=%s rotate=%.2f flip=%t brightness=%.2f\n",
			selected.You.X,
			selected.You.Y,
			selected.You.Mask,
			selected.You.Rotate,
			selected.You.Flip,
			selected.You.Brightness,
		)
	} else {
		report.WriteString("you role: not configured\n")
	}
	report.WriteString("\n")

	for _, source := range []diagnosticSource{full, preview} {
		if err := p.addDiagnosticSource(ctx, &report, files, source, selected); err != nil {
			fmt.Fprintf(&report, "%s processing error: %v\n\n", source.Label, err)
		}
	}
	files["00-report.txt"] = []byte(report.String())

	jobDir, err := os.MkdirTemp(p.workDir, "diagnostic-*")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建诊断目录失败："+err.Error())
	}
	defer os.RemoveAll(jobDir)
	archivePath := filepath.Join(jobDir, diagnosticName)
	if err := writeDiagnosticArchive(archivePath, files); err != nil {
		return p.respond(ctx, request, "❌ 生成诊断包失败："+err.Error())
	}
	_, err = p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:      archivePath,
		FileName:  diagnosticName,
		MIMEType:  "application/zip",
		ReplyToID: request.Message.ReplyToID,
		Kind:      telegram.MediaDocument,
		Caption:   "eat2 诊断包：" + key,
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 发送诊断包失败："+err.Error())
	}
	if p.services.Logger != nil {
		p.services.Logger.Info(
			"eat2 diagnostic created",
			"template", key,
			"message_kind", replied.Media.Kind,
			"full_error", full.Err,
			"preview_error", preview.Err,
		)
	}
	return p.respond(ctx, request,
		"✅ eat2 诊断包已发送，请保留 ZIP 文件用于分析")
}

func (p *Plugin) addDiagnosticSource(
	ctx context.Context,
	report *strings.Builder,
	files map[string][]byte,
	source diagnosticSource,
	selected entry,
) error {
	fmt.Fprintf(report, "[%s]\n", source.Label)
	if source.Err != nil {
		fmt.Fprintf(report, "download error: %v\n\n", source.Err)
		return nil
	}
	fmt.Fprintf(report, "metadata: kind=%s mime=%s bytes=%d declared=%dx%d\n",
		source.Media.Kind,
		source.Media.MIMEType,
		len(source.Data),
		source.Media.Width,
		source.Media.Height,
	)
	decoded, format, err := image.Decode(bytes.NewReader(source.Data))
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	fmt.Fprintf(report, "decoded: format=%s bounds=%s\n", format, rectangleText(decoded.Bounds()))
	files[source.Label+"-original."+diagnosticExtension(format)] = source.Data
	normalized, err := encodeDiagnosticPNG(decoded)
	if err != nil {
		return err
	}
	files[source.Label+"-decoded.png"] = normalized

	for _, threshold := range []uint8{1, 8, 32, 64, 128} {
		bounds, pixels := significantAlphaBounds(decoded, threshold)
		fmt.Fprintf(report, "alpha >= %d: bounds=%s pixels=%d\n",
			threshold, rectangleText(bounds), pixels)
	}
	globalTrimmed := trimTransparentAt(decoded, stickerTrimAlpha)
	globalTrimmedPNG, err := encodeDiagnosticPNG(globalTrimmed)
	if err != nil {
		return err
	}
	files[source.Label+"-trimmed-alpha8.png"] = globalTrimmedPNG
	dominantBounds, dominantPixels, totalPixels := dominantAlphaComponentBounds(
		decoded,
		stickerTrimAlpha,
	)
	dominantRate := 0.0
	if totalPixels > 0 {
		dominantRate = float64(dominantPixels) / float64(totalPixels)
	}
	fmt.Fprintf(report, "dominant component: bounds=%s pixels=%d/%d rate=%.4f selected=%t\n",
		rectangleText(dominantBounds),
		dominantPixels,
		totalPixels,
		dominantRate,
		dominantRate >= dominantAlphaRate,
	)
	trimmed := trimStickerContent(decoded)
	selectedPNG, err := encodeDiagnosticPNG(trimmed)
	if err != nil {
		return err
	}
	files[source.Label+"-selected.png"] = selectedPNG
	fmt.Fprintf(report, "selected image: bounds=%s\n", rectangleText(trimmed.Bounds()))

	if selected.You != nil {
		maskBytes, err := p.asset(ctx, selected.You.Mask)
		if err != nil {
			return fmt.Errorf("load mask: %w", err)
		}
		mask, _, err := image.Decode(bytes.NewReader(maskBytes))
		if err != nil {
			return fmt.Errorf("decode mask: %w", err)
		}
		fitted := resizeCover(trimmed, mask.Bounds().Dx(), mask.Bounds().Dy())
		fittedPNG, err := encodeDiagnosticPNG(fitted)
		if err != nil {
			return err
		}
		files[source.Label+"-fitted.png"] = fittedPNG
		maskedPNG, err := encodeDiagnosticPNG(maskImage(fitted, mask))
		if err != nil {
			return err
		}
		files[source.Label+"-masked.png"] = maskedPNG
		fmt.Fprintf(report, "mask: %s\n", rectangleText(mask.Bounds()))
	}
	report.WriteString("\n")
	return nil
}

func writeDiagnosticArchive(path string, files map[string][]byte) error {
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(output)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		entry, createErr := archive.CreateHeader(&zip.FileHeader{
			Name:   name,
			Method: zip.Deflate,
		})
		if createErr != nil {
			_ = archive.Close()
			_ = output.Close()
			return createErr
		}
		if _, writeErr := entry.Write(files[name]); writeErr != nil {
			_ = archive.Close()
			_ = output.Close()
			return writeErr
		}
	}
	if err := archive.Close(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func encodeDiagnosticPNG(source image.Image) ([]byte, error) {
	var output bytes.Buffer
	if err := png.Encode(&output, source); err != nil {
		return nil, fmt.Errorf("encode PNG: %w", err)
	}
	return output.Bytes(), nil
}

func diagnosticExtension(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg":
		return "jpg"
	case "png", "gif", "webp":
		return strings.ToLower(strings.TrimSpace(format))
	default:
		return "bin"
	}
}

func rectangleText(bounds image.Rectangle) string {
	return fmt.Sprintf("(%d,%d)-(%d,%d) %dx%d",
		bounds.Min.X,
		bounds.Min.Y,
		bounds.Max.X,
		bounds.Max.Y,
		bounds.Dx(),
		bounds.Dy(),
	)
}

func (p *Plugin) refresh(
	ctx context.Context,
	request command.Request,
	configURL string,
) error {
	if err := p.respond(ctx, request, "⏳ 刷新表情配置…"); err != nil {
		return err
	}
	cfg, err := p.loadConfig(ctx, configURL)
	if err != nil {
		return p.respond(ctx, request, "❌ 刷新配置失败："+err.Error())
	}
	if err := p.services.Storage.Put(
		ctx, "eat", "config_url", []byte(configURL),
	); err != nil {
		return p.respond(ctx, request, "❌ 保存配置地址失败："+err.Error())
	}
	p.cfg = cfg
	clear(p.assetCache)
	return p.respond(ctx, request,
		fmt.Sprintf("✅ 已加载 %d 个静态表情\n\n%s",
			len(cfg.Entries), p.listText(request.Prefix)))
}

func (p *Plugin) ensureConfig(ctx context.Context) error {
	if len(p.cfg.Entries) > 0 {
		return nil
	}
	configURL := defaultConfigURL
	if value, err := p.services.Storage.Get(ctx, "eat", "config_url"); err == nil &&
		strings.TrimSpace(string(value)) != "" {
		configURL = strings.TrimSpace(string(value))
	}
	cfg, err := p.loadConfig(ctx, configURL)
	if err != nil {
		return err
	}
	p.cfg = cfg
	return nil
}

func (p *Plugin) loadConfig(ctx context.Context, configURL string) (loadedConfig, error) {
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{URL: configURL})
	body := response.Body
	if err != nil || response.StatusCode != http.StatusOK {
		local, localErr := os.ReadFile(filepath.Join(p.assetDir, "config.json"))
		if localErr != nil {
			if err != nil {
				return loadedConfig{}, err
			}
			return loadedConfig{}, fmt.Errorf("配置源 HTTP %d", response.StatusCode)
		}
		body = local
		p.services.Logger.Info("using migrated local eat config")
	}
	var document configDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return loadedConfig{}, fmt.Errorf("解析配置：%w", err)
	}
	if len(document.Resources) == 0 {
		return loadedConfig{}, errors.New("配置缺少 resources")
	}
	if err := validateEntries(document.Resources); err != nil {
		return loadedConfig{}, err
	}
	base, err := resourceBase(configURL)
	if err != nil {
		return loadedConfig{}, err
	}
	return loadedConfig{
		URL:       configURL,
		AssetBase: base,
		Entries:   document.Resources,
	}, nil
}

func (p *Plugin) generate(
	ctx context.Context,
	request command.Request,
	selected entry,
	useMedia bool,
) error {
	if err := p.respond(ctx, request, "⏳ 正在制作「"+selected.Name+"」…"); err != nil {
		return err
	}
	messages, err := p.services.Telegram.GetMessages(
		ctx, request.Message.ChatID, []int{request.Message.ReplyToID},
	)
	if err != nil || len(messages) == 0 {
		return p.respond(ctx, request, "❌ 无法读取回复消息")
	}
	replied := messages[0]
	youImage, err := p.youImage(ctx, request, replied, useMedia)
	if err != nil {
		return p.respond(ctx, request, "❌ 获取目标头像失败："+err.Error())
	}
	var meImage image.Image
	if selected.Me != nil {
		userID := request.Message.SenderID
		if userID <= 0 {
			me, resolveErr := p.services.Telegram.ResolveUser(ctx, "me")
			if resolveErr != nil {
				return p.respond(ctx, request, "❌ 获取当前账号失败："+resolveErr.Error())
			}
			userID = me.ID
		}
		meImage, err = p.profileImage(ctx, userID)
		if err != nil {
			return p.respond(ctx, request, "❌ 获取当前账号头像失败："+err.Error())
		}
	}
	baseBytes, err := p.asset(ctx, selected.URL)
	if err != nil {
		return p.respond(ctx, request, "❌ 下载表情模板失败："+err.Error())
	}
	template, _, err := image.Decode(bytes.NewReader(baseBytes))
	if err != nil {
		return p.respond(ctx, request, "❌ 解码表情模板失败："+err.Error())
	}
	var result *image.NRGBA
	if selected.Stamp != nil {
		result = composeStamp(youImage, template, selected.Stamp.settings())
	} else {
		result = cloneNRGBA(template)
		if selected.You != nil {
			if err := p.applyRole(ctx, result, youImage, *selected.You); err != nil {
				return p.respond(ctx, request, "❌ 合成目标头像失败："+err.Error())
			}
		}
		if selected.Me != nil {
			if err := p.applyRole(ctx, result, meImage, *selected.Me); err != nil {
				return p.respond(ctx, request, "❌ 合成当前账号头像失败："+err.Error())
			}
		}
	}
	result = fitWithin(result, 512, 512)

	jobDir, err := os.MkdirTemp(p.workDir, "job-*")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建输出目录失败："+err.Error())
	}
	defer os.RemoveAll(jobDir)
	outputPath := filepath.Join(jobDir, stickerFileName)
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return p.respond(ctx, request, "❌ 创建输出文件失败："+err.Error())
	}
	encodeErr := nativewebp.Encode(output, result, nil)
	closeErr := output.Close()
	if encodeErr != nil {
		return p.respond(ctx, request, "❌ 编码表情失败："+encodeErr.Error())
	}
	if closeErr != nil {
		return p.respond(ctx, request, "❌ 保存表情失败："+closeErr.Error())
	}
	_, err = p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:         outputPath,
		FileName:     stickerFileName,
		MIMEType:     stickerMIMEType,
		ReplyToID:    request.Message.ReplyToID,
		Kind:         telegram.MediaSticker,
		Width:        result.Bounds().Dx(),
		Height:       result.Bounds().Dy(),
		StickerEmoji: fallback(selected.Name, "✨"),
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 发送表情失败："+err.Error())
	}
	if request.Message.Outgoing {
		return p.services.Telegram.DeleteMessages(
			ctx, request.Message.ChatID, []int{request.Message.ID},
		)
	}
	return nil
}

type stampSettings struct {
	Size    int
	Scale   float64
	Rotate  float64
	Opacity float64
}

func (value stamp) settings() stampSettings {
	result := stampSettings{
		Size:    512,
		Scale:   0.9,
		Rotate:  -12,
		Opacity: 0.6,
	}
	if value.Size != nil {
		result.Size = *value.Size
	}
	if value.Scale != nil {
		result.Scale = *value.Scale
	}
	if value.Rotate != nil {
		result.Rotate = *value.Rotate
	}
	if value.Opacity != nil {
		result.Opacity = *value.Opacity
	}
	return result
}

func validateEntries(entries map[string]entry) error {
	for key, item := range entries {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(item.Name) == "" ||
			strings.TrimSpace(item.URL) == "" ||
			(item.Me == nil && item.You == nil && item.Stamp == nil) {
			return fmt.Errorf("表情 %q 配置不完整", key)
		}
		for _, configuredRole := range []*role{item.Me, item.You} {
			if configuredRole != nil && strings.TrimSpace(configuredRole.Mask) == "" {
				return fmt.Errorf("表情 %q 的蒙版为空", key)
			}
		}
		if item.Stamp == nil {
			continue
		}
		settings := item.Stamp.settings()
		if settings.Size < 1 || settings.Size > 2048 ||
			settings.Scale <= 0 || settings.Scale > 4 ||
			math.IsNaN(settings.Scale) || math.IsInf(settings.Scale, 0) ||
			math.IsNaN(settings.Rotate) || math.IsInf(settings.Rotate, 0) ||
			settings.Opacity < 0 || settings.Opacity > 1 ||
			math.IsNaN(settings.Opacity) || math.IsInf(settings.Opacity, 0) {
			return fmt.Errorf("表情 %q 的印章参数无效", key)
		}
	}
	return nil
}

func composeStamp(
	avatar image.Image,
	overlay image.Image,
	settings stampSettings,
) *image.NRGBA {
	base := resizeCover(avatar, settings.Size, settings.Size)
	overlayWidth := max(1, int(math.Round(
		float64(settings.Size)*settings.Scale,
	)))
	prepared := resizeToWidth(overlay, overlayWidth)
	if settings.Rotate != 0 {
		prepared = rotate(prepared, settings.Rotate)
	}
	prepared = resizeInside(prepared, settings.Size, settings.Size)
	prepared = adjustOpacity(prepared, settings.Opacity)
	left := (base.Bounds().Dx() - prepared.Bounds().Dx()) / 2
	top := (base.Bounds().Dy() - prepared.Bounds().Dy()) / 2
	draw.Draw(
		base,
		image.Rect(
			left,
			top,
			left+prepared.Bounds().Dx(),
			top+prepared.Bounds().Dy(),
		),
		prepared,
		prepared.Bounds().Min,
		draw.Over,
	)
	return base
}

func (p *Plugin) youImage(
	ctx context.Context,
	request command.Request,
	replied telegram.Message,
	useMedia bool,
) (image.Image, error) {
	if !useMedia {
		if replied.SenderID <= 0 {
			return nil, errors.New("回复消息没有可识别的用户")
		}
		return p.profileImage(ctx, replied.SenderID)
	}
	if replied.Media == nil {
		return nil, errors.New("回复消息不包含图片或媒体")
	}
	var data bytes.Buffer
	if _, err := p.services.Telegram.DownloadMedia(
		ctx,
		request.Message.ChatID,
		replied.ID,
		&boundedWriter{Writer: &data, Remaining: maxImageBytes},
	); err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(data.Bytes()))
	if err != nil {
		return nil, err
	}
	if replied.Media.Kind == telegram.MediaSticker {
		decoded = trimStickerContent(decoded)
	}
	return decoded, nil
}

func (p *Plugin) profileImage(ctx context.Context, userID int64) (image.Image, error) {
	var data bytes.Buffer
	if err := p.services.Telegram.DownloadProfilePhoto(
		ctx,
		userID,
		&boundedWriter{Writer: &data, Remaining: maxImageBytes},
	); err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(data.Bytes()))
	return decoded, err
}

func (p *Plugin) applyRole(
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
		return fmt.Errorf("解码蒙版：%w", err)
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
		icon = rotate(icon, position.Rotate)
		icon = centerCrop(icon, width, height)
	}
	if position.Brightness > 0 && position.Brightness != 1 {
		icon = adjustBrightness(icon, position.Brightness)
	}
	masked := maskImage(icon, mask)
	draw.Draw(
		canvas,
		image.Rect(position.X, position.Y, position.X+width, position.Y+height),
		masked,
		image.Point{},
		draw.Over,
	)
	return nil
}

func (p *Plugin) asset(ctx context.Context, value string) ([]byte, error) {
	if local := p.localAsset(value); local != "" {
		if data, err := os.ReadFile(local); err == nil {
			return data, nil
		}
	}
	assetURL, err := resolveAssetURL(p.cfg.AssetBase, value)
	if err != nil {
		return nil, err
	}
	if cached, ok := p.assetCache[assetURL]; ok {
		return append([]byte(nil), cached...), nil
	}
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{URL: assetURL})
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("资源 HTTP %d", response.StatusCode)
	}
	if len(response.Body) == 0 {
		return nil, errors.New("资源为空")
	}
	if len(p.assetCache) >= 128 {
		clear(p.assetCache)
	}
	p.assetCache[assetURL] = append([]byte(nil), response.Body...)
	return response.Body, nil
}

func (p *Plugin) localAsset(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	name := filepath.Base(filepath.FromSlash(parsed.Path))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	target := filepath.Join(p.assetDir, name)
	root, err := filepath.Abs(p.assetDir)
	if err != nil {
		return ""
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return ""
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return target
}

func (p *Plugin) listText(prefix string) string {
	keys := sortedKeys(p.cfg.Entries)
	var result strings.Builder
	result.WriteString("🍽️ 当前静态表情\n\n")
	for _, key := range keys {
		fmt.Fprintf(&result, "%s - %s\n", key, p.cfg.Entries[key].Name)
	}
	result.WriteString("\n回复用户后使用 " + prefix + "eat <名称>；")
	result.WriteString("回复图片后使用 " + prefix + "eat2 <名称>；名称留空则随机。")
	return strings.TrimSpace(result.String())
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

func resourceBase(configURL string) (string, error) {
	parsed, err := url.Parse(configURL)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("配置 URL 无效")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("配置 URL 必须使用 HTTP(S)")
	}
	if parsed.User != nil {
		return "", errors.New("配置 URL 不允许包含用户信息")
	}
	if strings.EqualFold(parsed.Hostname(), "raw.githubusercontent.com") {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) >= 5 && parts[2] == "refs" && parts[3] == "heads" {
			parsed.Path = "/" + strings.Join(parts[:5], "/") + "/"
			parsed.RawQuery = ""
			parsed.Fragment = ""
			return parsed.String(), nil
		}
		if len(parts) >= 3 {
			parsed.Path = "/" + strings.Join(parts[:3], "/") + "/"
			parsed.RawQuery = ""
			parsed.Fragment = ""
			return parsed.String(), nil
		}
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, filepath.Base(parsed.Path))
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func resolveAssetURL(baseURL, value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() {
		if (parsed.Scheme != "https" && parsed.Scheme != "http") ||
			parsed.Hostname() == "" || parsed.User != nil {
			return "", errors.New("资源 URL 无效")
		}
		return parsed.String(), nil
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(parsed).String(), nil
}

func sortedKeys(values map[string]entry) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func cloneNRGBA(source image.Image) *image.NRGBA {
	bounds := source.Bounds()
	result := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(result, result.Bounds(), source, bounds.Min, draw.Src)
	return result
}

func resize(source image.Image, width, height int) *image.NRGBA {
	if width <= 0 || height <= 0 || source.Bounds().Dx() <= 0 || source.Bounds().Dy() <= 0 {
		return image.NewNRGBA(image.Rect(0, 0, max(0, width), max(0, height)))
	}
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(
		result,
		result.Bounds(),
		source,
		source.Bounds(),
		xdraw.Src,
		nil,
	)
	return result
}

func trimTransparent(source image.Image) image.Image {
	return trimTransparentAt(source, stickerTrimAlpha)
}

func trimStickerContent(source image.Image) image.Image {
	globalBounds, _ := significantAlphaBounds(source, stickerTrimAlpha)
	if globalBounds.Empty() {
		return source
	}
	selected := globalBounds
	dominantBounds, dominantPixels, totalPixels := dominantAlphaComponentBounds(
		source,
		stickerTrimAlpha,
	)
	if totalPixels > 0 &&
		float64(dominantPixels)/float64(totalPixels) >= dominantAlphaRate {
		selected = dominantBounds
	}
	return cropToBounds(source, selected)
}

func trimTransparentAt(source image.Image, threshold uint8) image.Image {
	bounds, _ := significantAlphaBounds(source, threshold)
	if bounds.Empty() || bounds == source.Bounds() {
		return source
	}
	return cropToBounds(source, bounds)
}

func cropToBounds(source image.Image, bounds image.Rectangle) image.Image {
	if bounds.Empty() || bounds == source.Bounds() {
		return source
	}
	result := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(result, result.Bounds(), source, bounds.Min, draw.Src)
	return result
}

func dominantAlphaComponentBounds(
	source image.Image,
	threshold uint8,
) (image.Rectangle, int, int) {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return image.Rectangle{}, 0, 0
	}
	opaque := make([]bool, width*height)
	total := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := y*width + x
			alpha := color.NRGBAModel.Convert(
				source.At(bounds.Min.X+x, bounds.Min.Y+y),
			).(color.NRGBA).A
			if alpha >= threshold {
				opaque[index] = true
				total++
			}
		}
	}
	if total == 0 {
		return image.Rectangle{}, 0, 0
	}

	visited := make([]bool, len(opaque))
	queue := make([]int, 0, total)
	largestBounds := image.Rectangle{}
	largestPixels := 0
	for start, present := range opaque {
		if !present || visited[start] {
			continue
		}
		queue = queue[:0]
		queue = append(queue, start)
		visited[start] = true
		componentPixels := 0
		minX, minY := width, height
		maxX, maxY := 0, 0
		for head := 0; head < len(queue); head++ {
			index := queue[head]
			x, y := index%width, index/width
			componentPixels++
			minX = min(minX, x)
			minY = min(minY, y)
			maxX = max(maxX, x+1)
			maxY = max(maxY, y+1)
			for offsetY := -1; offsetY <= 1; offsetY++ {
				for offsetX := -1; offsetX <= 1; offsetX++ {
					if offsetX == 0 && offsetY == 0 {
						continue
					}
					nextX, nextY := x+offsetX, y+offsetY
					if nextX < 0 || nextX >= width || nextY < 0 || nextY >= height {
						continue
					}
					next := nextY*width + nextX
					if opaque[next] && !visited[next] {
						visited[next] = true
						queue = append(queue, next)
					}
				}
			}
		}
		if componentPixels > largestPixels {
			largestPixels = componentPixels
			largestBounds = image.Rect(
				bounds.Min.X+minX,
				bounds.Min.Y+minY,
				bounds.Min.X+maxX,
				bounds.Min.Y+maxY,
			)
		}
	}
	return largestBounds, largestPixels, total
}

func significantAlphaBounds(source image.Image, threshold uint8) (image.Rectangle, int) {
	bounds := source.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	pixels := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			// Some WebP stickers retain nearly invisible alpha across the full
			// canvas. Ignore that encoding noise so the visible artwork can be
			// centered inside the template mask.
			if color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA).A < threshold {
				continue
			}
			pixels++
			minX = min(minX, x)
			minY = min(minY, y)
			maxX = max(maxX, x+1)
			maxY = max(maxY, y+1)
		}
	}
	if minX >= maxX || minY >= maxY {
		return image.Rectangle{}, 0
	}
	return image.Rect(minX, minY, maxX, maxY), pixels
}

func resizeCover(source image.Image, width, height int) *image.NRGBA {
	bounds := source.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 || width <= 0 || height <= 0 {
		return image.NewNRGBA(image.Rect(0, 0, max(0, width), max(0, height)))
	}
	scale := math.Max(
		float64(width)/float64(bounds.Dx()),
		float64(height)/float64(bounds.Dy()),
	)
	resized := resize(
		source,
		max(width, int(math.Ceil(float64(bounds.Dx())*scale))),
		max(height, int(math.Ceil(float64(bounds.Dy())*scale))),
	)
	return centerCrop(resized, width, height)
}

func resizeToWidth(source image.Image, width int) *image.NRGBA {
	bounds := source.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 || width <= 0 {
		return image.NewNRGBA(image.Rect(0, 0, max(0, width), 0))
	}
	height := max(1, int(math.Round(
		float64(bounds.Dy())*float64(width)/float64(bounds.Dx()),
	)))
	return resize(source, width, height)
}

func resizeInside(source image.Image, maxWidth, maxHeight int) *image.NRGBA {
	bounds := source.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 || maxWidth <= 0 || maxHeight <= 0 {
		return image.NewNRGBA(image.Rect(0, 0, 0, 0))
	}
	scale := math.Min(
		float64(maxWidth)/float64(bounds.Dx()),
		float64(maxHeight)/float64(bounds.Dy()),
	)
	return resize(
		source,
		max(1, int(math.Round(float64(bounds.Dx())*scale))),
		max(1, int(math.Round(float64(bounds.Dy())*scale))),
	)
}

func adjustOpacity(source image.Image, factor float64) *image.NRGBA {
	bounds := source.Bounds()
	result := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			current := color.NRGBAModel.Convert(
				source.At(bounds.Min.X+x, bounds.Min.Y+y),
			).(color.NRGBA)
			current.A = uint8(math.Round(float64(current.A) * factor))
			result.SetNRGBA(x, y, current)
		}
	}
	return result
}

func rotate(source image.Image, degrees float64) *image.NRGBA {
	radians := degrees * math.Pi / 180
	sine, cosine := math.Sin(radians), math.Cos(radians)
	bounds := source.Bounds()
	width, height := float64(bounds.Dx()), float64(bounds.Dy())
	outputWidth := int(math.Ceil(math.Abs(width*cosine) + math.Abs(height*sine)))
	outputHeight := int(math.Ceil(math.Abs(width*sine) + math.Abs(height*cosine)))
	result := image.NewNRGBA(image.Rect(0, 0, outputWidth, outputHeight))
	sourceCX, sourceCY := (width-1)/2, (height-1)/2
	outputCX, outputCY := float64(outputWidth-1)/2, float64(outputHeight-1)/2
	for y := 0; y < outputHeight; y++ {
		for x := 0; x < outputWidth; x++ {
			dx, dy := float64(x)-outputCX, float64(y)-outputCY
			sourceX := cosine*dx + sine*dy + sourceCX
			sourceY := -sine*dx + cosine*dy + sourceCY
			if sourceX >= 0 && sourceX < width && sourceY >= 0 && sourceY < height {
				result.Set(x, y, source.At(
					bounds.Min.X+int(math.Round(sourceX)),
					bounds.Min.Y+int(math.Round(sourceY)),
				))
			}
		}
	}
	return result
}

func centerCrop(source image.Image, width, height int) *image.NRGBA {
	bounds := source.Bounds()
	startX := bounds.Min.X + max(0, (bounds.Dx()-width)/2)
	startY := bounds.Min.Y + max(0, (bounds.Dy()-height)/2)
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(result, result.Bounds(), source, image.Pt(startX, startY), draw.Src)
	return result
}

func flipHorizontal(source image.Image) *image.NRGBA {
	bounds := source.Bounds()
	result := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			result.Set(bounds.Dx()-1-x, y, source.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return result
}

func adjustBrightness(source image.Image, factor float64) *image.NRGBA {
	bounds := source.Bounds()
	result := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			current := color.NRGBAModel.Convert(
				source.At(bounds.Min.X+x, bounds.Min.Y+y),
			).(color.NRGBA)
			current.R = uint8(min(255, int(float64(current.R)*factor)))
			current.G = uint8(min(255, int(float64(current.G)*factor)))
			current.B = uint8(min(255, int(float64(current.B)*factor)))
			result.SetNRGBA(x, y, current)
		}
	}
	return result
}

func maskImage(source image.Image, mask image.Image) *image.NRGBA {
	width := min(source.Bounds().Dx(), mask.Bounds().Dx())
	height := min(source.Bounds().Dy(), mask.Bounds().Dy())
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			current := color.NRGBAModel.Convert(
				source.At(source.Bounds().Min.X+x, source.Bounds().Min.Y+y),
			).(color.NRGBA)
			_, _, _, maskAlpha := mask.At(
				mask.Bounds().Min.X+x,
				mask.Bounds().Min.Y+y,
			).RGBA()
			current.A = uint8(uint32(current.A) * uint32(maskAlpha>>8) / 255)
			result.SetNRGBA(x, y, current)
		}
	}
	return result
}

func fitWithin(source *image.NRGBA, maxWidth, maxHeight int) *image.NRGBA {
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	if width <= maxWidth && height <= maxHeight {
		return source
	}
	scale := math.Min(float64(maxWidth)/float64(width), float64(maxHeight)/float64(height))
	return resize(source, max(1, int(float64(width)*scale)), max(1, int(float64(height)*scale)))
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return strings.TrimSpace(value)
}

type boundedWriter struct {
	io.Writer
	Remaining int64
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.Remaining {
		return 0, errors.New("图片超过 8 MiB")
	}
	count, err := w.Writer.Write(data)
	w.Remaining -= int64(count)
	return count, err
}

func init() {
	image.RegisterFormat("jpeg", "jpeg", jpeg.Decode, jpeg.DecodeConfig)
}

const eatGuideHTML = `<b>静态表情</b>

<code>eat</code> 和 <code>eat2</code> 使用同一套模板。

<b>使用头像</b>
<code>{{prefix}}eat</code>  查看模板列表
回复用户消息后发送 <code>{{prefix}}eat &lt;模板名&gt;</code>
模板名留空时随机选择

<b>使用图片</b>
<code>{{prefix}}eat2</code>  查看模板列表
回复图片后发送 <code>{{prefix}}eat2 &lt;模板名&gt;</code>
模板名留空时随机选择
位置或裁剪异常时，回复原图发送 <code>{{prefix}}eat2 debug &lt;模板名&gt;</code> 获取一次性诊断包

<b>模板配置</b>
<code>{{prefix}}eat set [配置地址]</code>  更新模板配置
<code>{{prefix}}eat clear</code>  清理配置缓存

上面的设置和清理命令也可以将 <code>eat</code> 换成 <code>eat2</code>。`
