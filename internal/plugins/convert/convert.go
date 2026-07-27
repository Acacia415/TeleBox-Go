package convert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

const (
	defaultGeminiModel = "gemini-3.6-flash"
	maxInputSize       = 2 << 30
)

type songInfo struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
}

type Plugin struct {
	services service.Container
	workDir  string
	mu       sync.Mutex
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services: services,
		workDir:  filepath.Join(os.TempDir(), "telebox-go-convert"),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "convert",
		Version:     "0.1.0",
		Description: "将回复的视频或媒体文件转换为 MP3",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "convert",
		Description: "使用 FFmpeg 转换音轨，可选 Gemini 元数据",
		OwnerOnly:   true,
		Handler:     p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error {
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return fmt.Errorf("create convert work directory: %w", err)
	}
	return nil
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if len(request.Args) > 0 {
		switch strings.ToLower(request.Args[0]) {
		case "help", "h":
			return p.respond(ctx, request, helpText(request.Prefix))
		case "clear":
			return p.clear(ctx, request)
		case "apikey":
			return p.configureAPIKey(ctx, request)
		case "model":
			return p.configureModel(ctx, request)
		}
	}
	if request.Message.ReplyToID <= 0 {
		return p.respond(ctx, request, helpText(request.Prefix))
	}
	return p.convert(ctx, request)
}

func (p *Plugin) convert(ctx context.Context, request command.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	messages, err := p.services.Telegram.GetMessages(
		ctx,
		request.Message.ChatID,
		[]int{request.Message.ReplyToID},
	)
	if err != nil || len(messages) == 0 {
		return p.respond(ctx, request, "❌ 无法读取回复的媒体消息")
	}
	source := messages[0]
	if source.Media == nil {
		return p.respond(ctx, request, "❌ 请回复一个视频或媒体文件")
	}
	switch source.Media.Kind {
	case telegram.MediaVideo, telegram.MediaAnimation, telegram.MediaDocument,
		telegram.MediaAudio:
	default:
		return p.respond(ctx, request, "❌ 回复的媒体类型不支持音频转换")
	}
	if source.Media.Size > maxInputSize {
		return p.respond(ctx, request, "❌ 媒体文件超过 2 GiB 安全上限")
	}
	if _, err := p.services.Tools.LookPath("ffmpeg"); err != nil {
		return p.respond(ctx, request, "❌ 未找到 FFmpeg，请先安装 ffmpeg 并加入 PATH")
	}

	jobDir, err := os.MkdirTemp(p.workDir, "job-")
	if err != nil {
		return fmt.Errorf("create convert job directory: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(jobDir); cleanupErr != nil {
			p.services.Logger.Warn("remove convert job", "path", jobDir, "error", cleanupErr)
		}
	}()
	inputPath := filepath.Join(jobDir, "input"+safeExtension(source.Media.FileName))
	outputPath := filepath.Join(jobDir, "audio.mp3")

	if err := p.respond(ctx, request, "⏳ 下载媒体…"); err != nil {
		return err
	}
	input, err := os.OpenFile(inputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create convert input: %w", err)
	}
	_, downloadErr := p.services.Telegram.DownloadMedia(
		ctx,
		request.Message.ChatID,
		source.ID,
		input,
	)
	closeErr := input.Close()
	if downloadErr != nil {
		return p.respond(ctx, request, "❌ 下载媒体失败："+downloadErr.Error())
	}
	if closeErr != nil {
		return fmt.Errorf("close convert input: %w", closeErr)
	}

	if err := p.respond(ctx, request, "⏳ 转换 MP3…"); err != nil {
		return err
	}
	if err := p.runFFmpeg(ctx, []string{
		"-hide_banner", "-loglevel", "error",
		"-i", inputPath,
		"-vn", "-codec:a", "libmp3lame", "-q:a", "2",
		"-y", outputPath,
	}, 5*time.Minute); err != nil {
		return p.respond(ctx, request, "❌ 视频转换失败："+err.Error())
	}

	useAI := len(request.Args) > 0 && strings.EqualFold(request.Args[0], "u")
	userQuery := strings.TrimSpace(strings.Join(request.Args, " "))
	if useAI {
		userQuery = strings.TrimSpace(strings.Join(request.Args[1:], " "))
	}
	originalBase := strings.TrimSuffix(
		fallback(source.Media.FileName, "audio"),
		filepath.Ext(source.Media.FileName),
	)
	info := songInfo{Title: fallback(userQuery, originalBase), Artist: "Video Converter"}
	finalPath := outputPath
	var coverPath string

	if useAI {
		if userQuery == "" {
			return p.respond(ctx, request, "❌ 识别模式需要歌曲名称")
		}
		if err := p.respond(ctx, request, "⏳ 识别歌曲信息…"); err != nil {
			return err
		}
		info, err = p.lookupSong(ctx, userQuery)
		if err != nil {
			p.services.Logger.Warn("recognize music metadata", "error", err)
			return p.respond(ctx, request, "❌ 未能识别歌曲信息")
		}
		coverPath, _ = p.downloadCover(ctx, jobDir, info)
		taggedPath := filepath.Join(jobDir, "tagged.mp3")
		if err := p.tagAudio(ctx, outputPath, taggedPath, coverPath, info); err != nil {
			p.services.Logger.Warn("tag converted audio; using untagged file", "error", err)
		} else {
			finalPath = taggedPath
		}
	}

	if err := p.respond(ctx, request, "⏳ 发送音频…"); err != nil {
		return err
	}
	duration := p.probeDuration(ctx, inputPath)
	fileName := safeFilename(info.Title)
	if info.Artist != "" && info.Artist != "未知" && info.Artist != "Video Converter" {
		fileName += " - " + safeFilename(info.Artist)
	}
	if fileName == "" {
		fileName = "audio"
	}
	if _, err := p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:      finalPath,
		FileName:  fileName + ".mp3",
		MIMEType:  "audio/mpeg",
		Caption:   formatSongCaption(info),
		ReplyToID: request.Message.ID,
		Kind:      telegram.MediaAudio,
		Duration:  duration,
	}); err != nil {
		return p.respond(ctx, request, "❌ 发送音频失败："+err.Error())
	}
	return p.services.Telegram.DeleteMessages(
		ctx,
		request.Message.ChatID,
		[]int{request.Message.ID},
	)
}

func (p *Plugin) runFFmpeg(ctx context.Context, args []string, timeout time.Duration) error {
	result, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name:      "ffmpeg",
		Args:      args,
		Timeout:   timeout,
		MaxOutput: 64 << 10,
	})
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(result.Stderr)
	if len(detail) > 300 {
		detail = detail[:300] + "…"
	}
	if detail != "" {
		return fmt.Errorf("%w: %s", err, detail)
	}
	return err
}

func (p *Plugin) tagAudio(
	ctx context.Context,
	inputPath, outputPath, coverPath string,
	info songInfo,
) error {
	args := []string{"-hide_banner", "-loglevel", "error", "-i", inputPath}
	if coverPath != "" {
		args = append(args, "-i", coverPath, "-map", "0:a", "-map", "1:v",
			"-c:v", "mjpeg", "-disposition:v:0", "attached_pic")
	}
	args = append(args,
		"-c:a", "copy",
		"-id3v2_version", "3",
		"-metadata", "title="+info.Title,
		"-metadata", "artist="+info.Artist,
		"-metadata", "album="+info.Album,
		"-y", outputPath,
	)
	return p.runFFmpeg(ctx, args, 2*time.Minute)
}

func (p *Plugin) probeDuration(ctx context.Context, inputPath string) time.Duration {
	if _, err := p.services.Tools.LookPath("ffprobe"); err != nil {
		return 0
	}
	result, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name: "ffprobe",
		Args: []string{
			"-v", "error",
			"-show_entries", "format=duration",
			"-of", "default=noprint_wrappers=1:nokey=1",
			inputPath,
		},
		Timeout:   30 * time.Second,
		MaxOutput: 1024,
	})
	if err != nil {
		return 0
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(result.Stdout), 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func (p *Plugin) lookupSong(ctx context.Context, query string) (songInfo, error) {
	apiKey, err := p.readConfig(ctx, "api_key")
	if err != nil || apiKey == "" {
		return songInfo{}, errors.New(
			"未设置 Gemini API Key，请使用 convert apikey <密钥> 设置",
		)
	}
	model, _ := p.readConfig(ctx, "model")
	if model == "" {
		model = defaultGeminiModel
	}
	body, err := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{
				"text": "你是音乐信息专家。只返回 JSON，字段为 title、artist、album；未知填“未知”。",
			}},
		},
		"contents": []map[string]any{{
			"role": "user",
			"parts": []map[string]string{{
				"text": "识别并校正这首歌的信息，优先最知名版本：" + query,
			}},
		}},
		"tools": []map[string]any{{"google_search": map[string]any{}}},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
		},
	})
	if err != nil {
		return songInfo{}, err
	}
	endpoint := "https://generativelanguage.googleapis.com/v1beta/models/" +
		url.PathEscape(model) + ":generateContent"
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		Method: http.MethodPost,
		URL:    endpoint,
		Headers: http.Header{
			"x-goog-api-key": []string{apiKey},
			"Content-Type":   []string{"application/json"},
		},
		Body: body,
	})
	if err != nil {
		return songInfo{}, err
	}
	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		return songInfo{}, fmt.Errorf("解析 Gemini 响应失败：%w", err)
	}
	if response.StatusCode != http.StatusOK {
		if payload.Error.Message != "" {
			return songInfo{}, errors.New(payload.Error.Message)
		}
		return songInfo{}, fmt.Errorf("Gemini HTTP %d", response.StatusCode)
	}
	if len(payload.Candidates) == 0 || len(payload.Candidates[0].Content.Parts) == 0 {
		return songInfo{}, errors.New("Gemini 未返回歌曲信息")
	}
	result, err := parseSongInfo(payload.Candidates[0].Content.Parts[0].Text, query)
	if err != nil {
		return songInfo{}, err
	}
	return result, nil
}

func parseSongInfo(text, fallbackTitle string) (songInfo, error) {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	var result songInfo
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &result); err != nil {
		for _, line := range strings.Split(text, "\n") {
			parts := strings.SplitN(line, "：", 2)
			if len(parts) != 2 {
				parts = strings.SplitN(line, ":", 2)
			}
			if len(parts) != 2 {
				continue
			}
			switch strings.TrimSpace(parts[0]) {
			case "歌曲名", "title":
				result.Title = strings.TrimSpace(parts[1])
			case "歌手", "artist":
				result.Artist = strings.TrimSpace(parts[1])
			case "专辑", "album":
				result.Album = strings.TrimSpace(parts[1])
			}
		}
	}
	result.Title = fallback(result.Title, fallbackTitle)
	result.Artist = fallback(result.Artist, "未知")
	result.Album = fallback(result.Album, "未知")
	if result.Title == "" {
		return songInfo{}, errors.New("歌曲标题为空")
	}
	return result, nil
}

func (p *Plugin) downloadCover(
	ctx context.Context,
	jobDir string,
	info songInfo,
) (string, error) {
	values := url.Values{
		"term":   []string{strings.TrimSpace(info.Title + " " + info.Artist)},
		"entity": []string{"song"},
		"limit":  []string{"1"},
	}
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		URL: "https://itunes.apple.com/search?" + values.Encode(),
	})
	if err != nil || response.StatusCode != http.StatusOK {
		return "", errors.New("iTunes 封面搜索失败")
	}
	var payload struct {
		Results []struct {
			ArtworkURL string `json:"artworkUrl100"`
		} `json:"results"`
	}
	if json.Unmarshal(response.Body, &payload) != nil ||
		len(payload.Results) == 0 || payload.Results[0].ArtworkURL == "" {
		return "", errors.New("未找到封面")
	}
	artworkURL := strings.Replace(payload.Results[0].ArtworkURL, "100x100bb", "600x600bb", 1)
	artwork, err := p.services.HTTP.Do(ctx, httpclient.Request{URL: artworkURL})
	if err != nil || artwork.StatusCode != http.StatusOK {
		return "", errors.New("下载封面失败")
	}
	coverPath := filepath.Join(jobDir, "cover.jpg")
	if err := os.WriteFile(coverPath, artwork.Body, 0o600); err != nil {
		return "", err
	}
	return coverPath, nil
}

func (p *Plugin) configureAPIKey(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		value, _ := p.readConfig(ctx, "api_key")
		if value == "" {
			return p.respond(ctx, request, "❌ 未设置 Gemini API Key")
		}
		suffix := value
		if len(suffix) > 4 {
			suffix = suffix[len(suffix)-4:]
		}
		return p.respond(ctx, request, "🔑 当前 Gemini API Key：…"+suffix)
	}
	value := strings.TrimSpace(strings.Join(request.Args[1:], " "))
	if strings.EqualFold(value, "clear") {
		if err := p.services.Storage.Delete(ctx, "convert", "api_key"); err != nil {
			return p.respond(ctx, request, "❌ 清除 API Key 失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ Gemini API Key 已清除")
	}
	if value == "" {
		return p.respond(ctx, request, "❌ API Key 不能为空")
	}
	if err := p.writeConfig(ctx, "api_key", value); err != nil {
		return p.respond(ctx, request, "❌ 保存 API Key 失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ Gemini API Key 已保存")
}

func (p *Plugin) configureModel(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		model, _ := p.readConfig(ctx, "model")
		return p.respond(ctx, request, "🤖 当前 Gemini 模型："+fallback(model, defaultGeminiModel))
	}
	model := strings.TrimSpace(request.Args[1])
	if model == "" || strings.ContainsAny(model, " \t\r\n") {
		return p.respond(ctx, request, "❌ 模型名称无效")
	}
	if err := p.writeConfig(ctx, "model", model); err != nil {
		return p.respond(ctx, request, "❌ 保存模型失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ Gemini 模型已设置："+model)
}

func (p *Plugin) clear(ctx context.Context, request command.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := os.RemoveAll(p.workDir); err != nil {
		return p.respond(ctx, request, "❌ 清理临时文件失败："+err.Error())
	}
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return p.respond(ctx, request, "❌ 重建临时目录失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ convert 临时文件已清理")
}

func (p *Plugin) readConfig(ctx context.Context, key string) (string, error) {
	value, err := p.services.Storage.Get(ctx, "convert", key)
	return string(value), err
}

func (p *Plugin) writeConfig(ctx context.Context, key, value string) error {
	return p.services.Storage.Put(ctx, "convert", key, []byte(value))
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

func safeFilename(value string) string {
	var builder strings.Builder
	space := false
	count := 0
	for _, character := range strings.TrimSpace(value) {
		if count >= 80 {
			break
		}
		switch {
		case unicode.IsControl(character) || strings.ContainsRune(`\/:*?"<>|`, character):
			continue
		case unicode.IsSpace(character):
			space = builder.Len() > 0
			continue
		default:
			if space {
				builder.WriteByte(' ')
				space = false
			}
			builder.WriteRune(character)
			count++
		}
	}
	return strings.Trim(builder.String(), ". ")
}

func safeExtension(fileName string) string {
	extension := strings.ToLower(filepath.Ext(fileName))
	if len(extension) > 10 || extension == "" {
		return ".bin"
	}
	for _, character := range extension[1:] {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return ".bin"
		}
	}
	return extension
}

func formatSongCaption(info songInfo) string {
	if info.Artist == "" || info.Artist == "未知" || info.Artist == "Video Converter" {
		return ""
	}
	return "🎵 " + info.Title + "\n👤 " + info.Artist
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func helpText(prefix string) string {
	return "🎬 视频转音频\n\n用法：回复视频或媒体文件后发送：\n" +
		prefix + "convert [输出文件名]\n" +
		prefix + "convert u <歌曲名>  使用 Gemini 识别元数据和封面\n\n配置：\n" +
		prefix + "convert apikey <Gemini API Key>\n" +
		prefix + "convert apikey clear\n" +
		prefix + "convert model <模型名>\n" +
		prefix + "convert clear\n\n系统依赖：ffmpeg（建议同时安装 ffprobe）"
}
