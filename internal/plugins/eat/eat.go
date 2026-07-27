package eat

import (
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
	_ "golang.org/x/image/webp"
)

const (
	defaultConfigURL = "https://raw.githubusercontent.com/TeleBoxOrg/TeleBox_Plugins/refs/heads/main/eat/config.json"
	maxImageBytes    = 8 << 20
)

type role struct {
	X          int     `json:"x"`
	Y          int     `json:"y"`
	Mask       string  `json:"mask"`
	Brightness float64 `json:"brightness"`
	Rotate     float64 `json:"rotate"`
	Flip       bool    `json:"flip"`
}

type entry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Me   *role  `json:"me"`
	You  *role  `json:"you"`
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
	workDir  string

	mu         sync.Mutex
	cfg        loadedConfig
	assetCache map[string][]byte
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services:   services,
		workDir:    filepath.Join(os.TempDir(), "telebox-go-eat"),
		assetCache: make(map[string][]byte),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "eat",
		Version:     "0.1.0",
		Description: "将用户头像或回复图片合成到静态表情模板",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{
		{
			Name:        "eat",
			Description: "使用回复用户的头像生成表情",
			OwnerOnly:   true,
			Handler: func(ctx context.Context, request command.Request) error {
				return p.handle(ctx, request, false)
			},
		},
		{
			Name:        "eat2",
			Description: "使用回复消息中的图片生成表情",
			OwnerOnly:   true,
			Handler: func(ctx context.Context, request command.Request) error {
				return p.handle(ctx, request, true)
			},
		},
	}
}

func (p *Plugin) Start(context.Context) error {
	return os.MkdirAll(p.workDir, 0o700)
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
		return p.respond(ctx, request, "✅ eat 内存缓存已清理")
	}
	if err := p.ensureConfig(ctx); err != nil {
		return p.respond(ctx, request, "❌ 加载表情配置失败："+err.Error())
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
	if err != nil {
		return loadedConfig{}, err
	}
	if response.StatusCode != http.StatusOK {
		return loadedConfig{}, fmt.Errorf("配置源 HTTP %d", response.StatusCode)
	}
	var document configDocument
	if err := json.Unmarshal(response.Body, &document); err != nil {
		return loadedConfig{}, fmt.Errorf("解析配置：%w", err)
	}
	if len(document.Resources) == 0 {
		return loadedConfig{}, errors.New("配置缺少 resources")
	}
	for key, item := range document.Resources {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(item.Name) == "" ||
			strings.TrimSpace(item.URL) == "" || (item.Me == nil && item.You == nil) {
			return loadedConfig{}, fmt.Errorf("表情 %q 配置不完整", key)
		}
		for _, configuredRole := range []*role{item.Me, item.You} {
			if configuredRole != nil && strings.TrimSpace(configuredRole.Mask) == "" {
				return loadedConfig{}, fmt.Errorf("表情 %q 的蒙版为空", key)
			}
		}
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
	if err := p.respond(ctx, request, "⏳ 生成「"+selected.Name+"」…"); err != nil {
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
	canvas, _, err := image.Decode(bytes.NewReader(baseBytes))
	if err != nil {
		return p.respond(ctx, request, "❌ 解码表情模板失败："+err.Error())
	}
	result := cloneNRGBA(canvas)
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
	result = fitWithin(result, 512, 512)

	jobDir, err := os.MkdirTemp(p.workDir, "job-*")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建输出目录失败："+err.Error())
	}
	defer os.RemoveAll(jobDir)
	outputPath := filepath.Join(jobDir, "output.png")
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return p.respond(ctx, request, "❌ 创建输出文件失败："+err.Error())
	}
	encodeErr := png.Encode(output, result)
	closeErr := output.Close()
	if encodeErr != nil {
		return p.respond(ctx, request, "❌ 编码表情失败："+encodeErr.Error())
	}
	if closeErr != nil {
		return p.respond(ctx, request, "❌ 保存表情失败："+closeErr.Error())
	}
	_, err = p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:         outputPath,
		FileName:     "output.png",
		MIMEType:     "image/png",
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
	return decoded, err
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
	icon := resize(avatar, width, height)
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
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	bounds := source.Bounds()
	for y := 0; y < height; y++ {
		sourceY := bounds.Min.Y + y*bounds.Dy()/height
		for x := 0; x < width; x++ {
			sourceX := bounds.Min.X + x*bounds.Dx()/width
			result.Set(x, y, source.At(sourceX, sourceY))
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
