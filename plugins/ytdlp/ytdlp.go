package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/managedtool"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

const (
	defaultGeminiBase  = "https://generativelanguage.googleapis.com"
	defaultGeminiModel = "gemini-3.6-flash"
)

type songInfo struct {
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
	URL      string
}

type Plugin struct {
	services service.Container
	assetDir string
	workDir  string
	mu       sync.Mutex
}

func New(services service.Container) *Plugin {
	assetDir := filepath.Join(services.AssetsDir, "ytdlp")
	if services.AssetsDir == "" {
		assetDir = filepath.Join(os.TempDir(), "telebox-go-ytdlp-assets")
	}
	return &Plugin{
		services: services,
		assetDir: assetDir,
		workDir:  filepath.Join(os.TempDir(), "telebox-go-ytdlp"),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "yt-dlp",
		Version:     "0.3.3",
		Description: "使用 yt-dlp 搜索并下载 YouTube 音乐",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "yt",
		Description: "搜索并下载 YouTube 音乐",
		Usage: []string{
			"yt <关键词>",
			"yt <歌名>-<歌手>",
			"yt <歌名> - <歌手>",
			"yt update",
			"yt apikey <密钥|clear>",
			"yt model <模型名>",
			"yt baseurl <地址|clear>",
			"yt proxy <地址|clear>",
			"yt cookies <文件路径|clear>",
			"yt runtime <Deno路径|auto|none>",
			"yt binary <路径|auto>",
			"yt setup",
			"yt doctor",
			"yt clear",
		},
		HelpHTML:  ytGuideHTML,
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(ctx context.Context) error {
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(p.assetDir, 0o700); err != nil {
		return err
	}
	return p.migrateLegacyConfig(ctx)
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 {
		return p.respondHTML(ctx, request, ytHelpHTML(request.Prefix))
	}
	switch strings.ToLower(request.Args[0]) {
	case "help", "h":
		return p.respondHTML(ctx, request, ytHelpHTML(request.Prefix))
	case "update":
		return p.update(ctx, request)
	case "apikey":
		return p.configure(ctx, request, "api_key", true)
	case "baseurl":
		return p.configureBaseURL(ctx, request)
	case "model":
		return p.configure(ctx, request, "model", false)
	case "binary":
		return p.configureBinary(ctx, request)
	case "proxy":
		return p.configureProxy(ctx, request)
	case "cookies":
		return p.configureCookies(ctx, request)
	case "runtime":
		return p.configureRuntime(ctx, request)
	case "setup":
		return p.setup(ctx, request)
	case "doctor":
		return p.doctor(ctx, request)
	case "clear":
		return p.clear(ctx, request)
	}
	return p.download(ctx, request, strings.TrimSpace(strings.Join(request.Args, " ")))
}

func (p *Plugin) download(
	ctx context.Context,
	request command.Request,
	query string,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	executable, err := p.findExecutable(ctx)
	if err != nil {
		if err := p.respond(ctx, request, "⏳ 首次使用，正在安装 yt-dlp…"); err != nil {
			return err
		}
		executable, err = p.ensureYTDLP(ctx, false)
		if err != nil {
			return p.respond(ctx, request, "❌ 自动安装 yt-dlp 失败："+shortError(err))
		}
	}
	if _, err := p.services.Tools.LookPath("ffmpeg"); err != nil {
		return p.respond(ctx, request, "❌ 未找到 FFmpeg；yt-dlp 音频转换需要 ffmpeg")
	}
	jobDir, err := os.MkdirTemp(p.workDir, "job-*")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建下载目录失败："+err.Error())
	}
	defer func() {
		if cleanupErr := os.RemoveAll(jobDir); cleanupErr != nil {
			p.services.Logger.Warn("remove yt-dlp job", "path", jobDir, "error", cleanupErr)
		}
	}()

	title, artist, manual := parseManual(query)
	searchQuery := query
	if !manual {
		if apiKey, _ := p.read(ctx, "api_key"); apiKey != "" {
			if err := p.respond(ctx, request, "⏳ 识别歌曲信息…"); err != nil {
				return err
			}
			identified, identifyErr := p.identify(ctx, query, apiKey)
			if identifyErr == nil {
				title, artist = identified.Title, identified.Artist
				searchQuery = strings.TrimSpace(artist + " " + title)
			} else {
				p.services.Logger.Warn("yt-dlp Gemini metadata fallback", "error", identifyErr)
			}
		}
	}
	if err := p.respond(ctx, request, "⏳ 搜索歌曲…"); err != nil {
		return err
	}
	video, err := p.videoInfo(ctx, executable, searchQuery)
	if err != nil {
		return p.respond(ctx, request, "❌ 未找到可下载的歌曲："+shortError(err))
	}
	if title == "" || artist == "" {
		parsedTitle, parsedArtist := parseVideoTitle(video.Title, video.Artist)
		if title == "" {
			title = parsedTitle
		}
		if artist == "" {
			artist = parsedArtist
		}
	}
	video.Title = fallback(title, video.Title)
	video.Artist = fallback(artist, video.Artist)

	if err := p.respond(
		ctx,
		request,
		"⏳ 下载："+video.Title+"\n• 艺术家："+fallback(video.Artist, "未知"),
	); err != nil {
		return err
	}
	outputTemplate := filepath.Join(jobDir, "%(id)s.%(ext)s")
	result, runErr := p.services.Tools.Run(ctx, toolrunner.Command{
		Name: executable,
		Args: append(p.ytDLPOptions(ctx),
			video.URL,
			"--no-playlist",
			"--no-warnings",
			"-x",
			"--audio-format", "mp3",
			"--audio-quality", "0",
			"--embed-thumbnail",
			"--convert-thumbnails", "jpg",
			"--embed-metadata",
			"--max-filesize", "512M",
			"-o", outputTemplate,
			"--print", "after_move:filepath",
		),
		Directory: jobDir,
		Timeout:   10 * time.Minute,
		MaxOutput: 256 << 10,
	})
	if runErr != nil {
		detail := shortToolError(result.Stderr, runErr)
		if strings.Contains(strings.ToLower(detail), "403") {
			detail += "\n请先运行 " + request.Prefix + "yt update 更新 yt-dlp"
		}
		return p.respond(ctx, request, "❌ yt-dlp 下载失败："+detail)
	}
	audioPath := findOutputPath(jobDir, result.Stdout)
	if audioPath == "" {
		return p.respond(ctx, request, "❌ yt-dlp 未生成 MP3 文件")
	}
	if err := p.respond(ctx, request, "⏳ 上传音频…"); err != nil {
		return err
	}
	fileName := safeFilename(video.Title) + " - " + safeFilename(video.Artist)
	fileName = strings.Trim(fileName, " -.") + ".mp3"
	if fileName == ".mp3" {
		fileName = "music.mp3"
	}
	_, err = p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:       audioPath,
		FileName:   fileName,
		MIMEType:   "audio/mpeg",
		Caption:    "🎵 " + video.Title + "\n👤 " + fallback(video.Artist, "未知艺术家"),
		ReplyToID:  request.Message.ID,
		Kind:       telegram.MediaAudio,
		Duration:   video.Duration,
		AudioTitle: video.Title,
		Performer:  video.Artist,
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 上传音频失败："+err.Error())
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

func (p *Plugin) videoInfo(
	ctx context.Context,
	executable string,
	query string,
) (songInfo, error) {
	result, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name: executable,
		Args: append(p.ytDLPOptions(ctx),
			"ytsearch1:"+query,
			"--no-playlist",
			"--no-download",
			"--no-warnings",
			"--print", "%(title)s\t%(uploader)s\t%(duration)s\t%(webpage_url)s",
		),
		Timeout:   2 * time.Minute,
		MaxOutput: 128 << 10,
	})
	if err != nil {
		return songInfo{}, errors.New(shortToolError(result.Stderr, err))
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) == 0 {
		return songInfo{}, errors.New("yt-dlp 未返回搜索结果")
	}
	parts := strings.Split(strings.TrimSpace(lines[len(lines)-1]), "\t")
	if len(parts) < 4 || strings.TrimSpace(parts[3]) == "" {
		return songInfo{}, errors.New("yt-dlp 返回的歌曲信息不完整")
	}
	seconds, _ := strconv.ParseFloat(parts[2], 64)
	return songInfo{
		Title:    strings.TrimSpace(parts[0]),
		Artist:   strings.TrimSpace(parts[1]),
		Duration: time.Duration(seconds * float64(time.Second)),
		URL:      strings.TrimSpace(parts[3]),
	}, nil
}

func (p *Plugin) identify(
	ctx context.Context,
	query string,
	apiKey string,
) (songInfo, error) {
	model, _ := p.read(ctx, "model")
	model = fallback(model, defaultGeminiModel)
	baseURL, _ := p.read(ctx, "base_url")
	baseURL = strings.TrimRight(fallback(baseURL, defaultGeminiBase), "/")
	body, err := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{
				"text": "根据输入校正歌曲信息。只返回 JSON，字段为 title、artist、album；未知填“未知”。",
			}},
		},
		"contents": []map[string]any{{
			"role": "user",
			"parts": []map[string]string{{
				"text": "识别并校正这首歌，优先最知名版本：" + query,
			}},
		}},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
			"temperature":      p.readFloat(ctx, "temperature", 0.2),
			"topP":             p.readFloat(ctx, "top_p", 0.8),
			"topK":             p.readInt(ctx, "top_k", 40),
		},
	})
	if err != nil {
		return songInfo{}, err
	}
	endpoint := baseURL + "/v1beta/models/" + url.PathEscape(model) + ":generateContent"
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
		return songInfo{}, fmt.Errorf("解析 Gemini 响应：%w", err)
	}
	if response.StatusCode != http.StatusOK {
		return songInfo{}, errors.New(fallback(payload.Error.Message,
			fmt.Sprintf("Gemini HTTP %d", response.StatusCode)))
	}
	if len(payload.Candidates) == 0 || len(payload.Candidates[0].Content.Parts) == 0 {
		return songInfo{}, errors.New("Gemini 未返回歌曲信息")
	}
	return parseAIInfo(payload.Candidates[0].Content.Parts[0].Text, query)
}

func (p *Plugin) update(ctx context.Context, request command.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	executable, err := p.findExecutable(ctx)
	if err != nil {
		executable, err = p.ensureYTDLP(ctx, false)
		if err != nil {
			return p.respond(ctx, request, "❌ 自动安装 yt-dlp 失败："+shortError(err))
		}
	}
	if err := p.respond(ctx, request, "⏳ 更新 yt-dlp…"); err != nil {
		return err
	}
	result, err := p.services.Tools.Run(ctx, toolrunner.Command{
		Name: executable, Args: []string{"-U"}, Timeout: 3 * time.Minute, MaxOutput: 128 << 10,
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 更新失败："+shortToolError(result.Stderr, err))
	}
	version, versionErr := p.services.Tools.Run(ctx, toolrunner.Command{
		Name: executable, Args: []string{"--version"}, Timeout: 30 * time.Second,
	})
	if versionErr != nil {
		return p.respond(ctx, request, "✅ yt-dlp 更新命令执行完成")
	}
	return p.respond(ctx, request, "✅ yt-dlp 已更新\n当前版本："+strings.TrimSpace(version.Stdout))
}

func (p *Plugin) configure(
	ctx context.Context,
	request command.Request,
	key string,
	secret bool,
) error {
	label := "Gemini 模型"
	defaultValue := defaultGeminiModel
	if key == "api_key" {
		label = "Gemini API Key"
		defaultValue = ""
	}
	if len(request.Args) == 1 {
		value, _ := p.read(ctx, key)
		value = fallback(value, defaultValue)
		if value == "" {
			return p.respond(ctx, request, "❌ 尚未设置 "+label)
		}
		if secret {
			value = maskSecret(value)
		}
		return p.respond(ctx, request, label+"："+value)
	}
	value := strings.TrimSpace(strings.Join(request.Args[1:], " "))
	if strings.EqualFold(value, "clear") {
		if err := p.services.Storage.Delete(ctx, "yt-dlp", key); err != nil {
			return p.respond(ctx, request, "❌ 清除配置失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已清除 "+label)
	}
	if value == "" || (!secret && strings.ContainsAny(value, " \t\r\n")) {
		return p.respond(ctx, request, "❌ 配置值无效")
	}
	if err := p.write(ctx, key, value); err != nil {
		return p.respond(ctx, request, "❌ 保存配置失败："+err.Error())
	}
	if secret {
		value = maskSecret(value)
	}
	return p.respond(ctx, request, "✅ 已保存 "+label+"："+value)
}

func (p *Plugin) configureBaseURL(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		value, _ := p.read(ctx, "base_url")
		return p.respond(ctx, request, "Gemini API 地址："+fallback(value, defaultGeminiBase))
	}
	value := strings.TrimRight(strings.TrimSpace(request.Args[1]), "/")
	if strings.EqualFold(value, "clear") {
		if err := p.services.Storage.Delete(ctx, "yt-dlp", "base_url"); err != nil {
			return p.respond(ctx, request, "❌ 清除地址失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已恢复 Gemini 官方 API 地址")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Hostname() == "" || parsed.User != nil {
		return p.respond(ctx, request, "❌ 请输入有效的 HTTP(S) API 地址")
	}
	if err := p.write(ctx, "base_url", value); err != nil {
		return p.respond(ctx, request, "❌ 保存地址失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ Gemini API 地址已设置："+value)
}

func (p *Plugin) configureBinary(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		executable, err := p.findExecutable(ctx)
		if err != nil {
			return p.respond(ctx, request, installHint(request.Prefix))
		}
		return p.respond(ctx, request, "yt-dlp 路径："+executable)
	}
	value := strings.TrimSpace(strings.Join(request.Args[1:], " "))
	if strings.EqualFold(value, "auto") || strings.EqualFold(value, "clear") {
		if err := p.services.Storage.Delete(ctx, "yt-dlp", "binary"); err != nil {
			return p.respond(ctx, request, "❌ 清除路径失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已恢复自动查找 yt-dlp")
	}
	info, err := os.Stat(value)
	if err != nil || info.IsDir() {
		return p.respond(ctx, request, "❌ yt-dlp 文件不存在")
	}
	if err := p.write(ctx, "binary", value); err != nil {
		return p.respond(ctx, request, "❌ 保存路径失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ yt-dlp 路径已保存")
}

func (p *Plugin) clear(ctx context.Context, request command.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.RemoveAll(p.workDir); err != nil {
		return p.respond(ctx, request, "❌ 清理下载目录失败："+err.Error())
	}
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return p.respond(ctx, request, "❌ 重建下载目录失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ yt-dlp 临时文件已清理")
}

func (p *Plugin) findExecutable(ctx context.Context) (string, error) {
	if configured, _ := p.read(ctx, "binary"); configured != "" {
		if managedtool.Executable(configured) {
			return configured, nil
		}
	}
	names := []string{"yt-dlp"}
	if runtime.GOOS == "windows" {
		names = []string{"yt-dlp.exe", "yt-dlp"}
	}
	for _, name := range names {
		candidates := []string{filepath.Join(p.assetDir, name)}
		if p.services.AssetsDir != "" {
			candidates = append(candidates, filepath.Join(p.services.AssetsDir, name))
		}
		for _, candidate := range uniqueExecutablePaths(candidates) {
			if _, err := os.Stat(candidate); err != nil {
				continue
			}
			if candidate == filepath.Join(p.assetDir, name) &&
				managedtool.Verify(candidate, p.ytDLPReceiptPath()) == nil {
				return candidate, nil
			}
			quarantined, err := managedtool.Quarantine(
				candidate, filepath.Join(p.assetDir, "quarantine"),
			)
			if err != nil {
				return "", fmt.Errorf("隔离旧 yt-dlp 失败: %w", err)
			}
			_ = os.Remove(p.ytDLPReceiptPath())
			if quarantined != "" && p.services.Logger != nil {
				p.services.Logger.Info("quarantined unmanaged yt-dlp", "path", quarantined)
			}
		}
		if executable, err := p.services.Tools.LookPath(name); err == nil {
			return executable, nil
		}
	}
	return "", toolrunner.ErrExecutableNotFound
}

func uniqueExecutablePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, candidate := range paths {
		cleaned := filepath.Clean(candidate)
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

func (p *Plugin) read(ctx context.Context, key string) (string, error) {
	value, err := p.services.Storage.Get(ctx, "yt-dlp", key)
	return string(value), err
}

func (p *Plugin) write(ctx context.Context, key, value string) error {
	return p.services.Storage.Put(ctx, "yt-dlp", key, []byte(value))
}

func (p *Plugin) readFloat(ctx context.Context, key string, defaultValue float64) float64 {
	value, _ := p.read(ctx, key)
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return defaultValue
	}
	return parsed
}

func (p *Plugin) readInt(ctx context.Context, key string, defaultValue int) int {
	value, _ := p.read(ctx, key)
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return parsed
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

func (p *Plugin) respondHTML(ctx context.Context, request command.Request, text string) error {
	if request.Message.Outgoing {
		_, err := telegram.EditHTML(
			ctx,
			p.services.Telegram,
			request.Message.ChatID,
			request.Message.ID,
			text,
		)
		return err
	}
	_, err := telegram.ReplyHTML(
		ctx,
		p.services.Telegram,
		request.Message.ChatID,
		request.Message.ID,
		text,
	)
	return err
}

func parseManual(query string) (title, artist string, ok bool) {
	for _, separator := range []string{" - ", " – ", " — ", " | ", " · "} {
		parts := strings.SplitN(query, separator, 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" &&
			strings.TrimSpace(parts[1]) != "" {
			return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
		}
	}
	if strings.Count(query, "-") == 1 &&
		!strings.ContainsFunc(query, unicode.IsSpace) {
		parts := strings.SplitN(query, "-", 2)
		if strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
			return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
		}
	}
	return "", "", false
}

func parseAIInfo(text, fallbackTitle string) (songInfo, error) {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	var payload struct {
		Title  string `json:"title"`
		Artist string `json:"artist"`
		Album  string `json:"album"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &payload); err != nil {
		return songInfo{}, fmt.Errorf("Gemini JSON 无效：%w", err)
	}
	payload.Title = fallback(payload.Title, fallbackTitle)
	payload.Artist = fallback(payload.Artist, "未知艺术家")
	return songInfo{Title: payload.Title, Artist: payload.Artist, Album: payload.Album}, nil
}

var decorations = regexp.MustCompile(
	`(?i)(\[|\(|【)[^\]】)]*(official|video|mv|hd|4k|lyric|subtitles|官方|正式|歌词|字幕|高清|版本)[^\]】)]*(\]|\)|】)`,
)

func parseVideoTitle(rawTitle, uploader string) (title, artist string) {
	cleanTitle := strings.TrimSpace(decorations.ReplaceAllString(rawTitle, ""))
	for _, separator := range []string{" - ", " – ", " — ", " | ", " · "} {
		parts := strings.SplitN(cleanTitle, separator, 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1]), strings.TrimSpace(parts[0])
		}
	}
	cleanUploader := strings.TrimSpace(
		regexp.MustCompile(`(?i)\s*(official|mv|官方频道)\s*`).ReplaceAllString(uploader, " "),
	)
	return cleanTitle, fallback(cleanUploader, "未知艺术家")
}

func findOutputPath(directory, stdout string) string {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		candidate := strings.Trim(strings.TrimSpace(lines[index]), `"'`)
		if candidate == "" {
			continue
		}
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(directory, candidate)
		}
		candidate, err := filepath.Abs(candidate)
		if err != nil || !isInside(directory, candidate) {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() &&
			strings.EqualFold(filepath.Ext(candidate), ".mp3") {
			return candidate
		}
	}
	matches, _ := filepath.Glob(filepath.Join(directory, "*.mp3"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func isInside(directory, candidate string) bool {
	root, err := filepath.Abs(directory)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func safeFilename(value string) string {
	var result strings.Builder
	for _, char := range strings.TrimSpace(value) {
		if unicode.IsControl(char) || strings.ContainsRune(`\/:*?"<>|`, char) {
			continue
		}
		if result.Len() >= 100 {
			break
		}
		result.WriteRune(char)
	}
	return strings.Trim(result.String(), ". ")
}

func shortToolError(stderr string, err error) string {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = err.Error()
	}
	return shortError(errors.New(detail))
}

func shortError(err error) string {
	text := strings.TrimSpace(err.Error())
	if len([]rune(text)) <= 500 {
		return text
	}
	return string([]rune(text)[:500]) + "…"
}

func maskSecret(value string) string {
	runes := []rune(value)
	if len(runes) <= 8 {
		return "••••"
	}
	return string(runes[:4]) + "…" + string(runes[len(runes)-4:])
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" || strings.EqualFold(strings.TrimSpace(value), "未知") {
		return defaultValue
	}
	return strings.TrimSpace(value)
}

func installHint(prefix string) string {
	return "❌ 未找到 yt-dlp\n\n请通过 yt-dlp 官方方式安装并加入 PATH，" +
		"或使用 " + prefix + "yt binary <可执行文件路径> 指定位置。"
}

func ytHelpHTML(prefix string) string {
	return "<b>🔧 YT · YouTube 音乐下载器</b>\n\n" +
		strings.ReplaceAll(ytGuideHTML, "{{prefix}}", html.EscapeString(prefix))
}

const ytGuideHTML = `<b>功能</b>
• 按歌名搜索并下载最佳音质
• 使用 Gemini 补全歌名、歌手、专辑和封面
• 支持手动指定歌名与歌手
• 自动嵌入封面和歌曲信息
• 更新 yt-dlp 下载核心

<b>搜索与下载</b>
• <code>{{prefix}}yt 稻香</code>
• <code>{{prefix}}yt 晴天-周杰伦</code>
• <code>{{prefix}}yt 晴天 - 周杰伦</code>

配置 API Key 后会先用 Gemini 整理歌曲信息；没有 API Key 时直接使用 yt-dlp 搜索。

<b>Gemini 配置</b>
• <code>{{prefix}}yt apikey &lt;API密钥&gt;</code>  保存密钥
• <code>{{prefix}}yt apikey</code>  查看是否已配置
• <code>{{prefix}}yt apikey clear</code>  清除密钥
• <code>{{prefix}}yt model &lt;模型名&gt;</code>  设置模型
• <code>{{prefix}}yt baseurl &lt;反代地址&gt;</code>  设置 Gemini API 地址
• <code>{{prefix}}yt baseurl</code>  查看当前地址
• <code>{{prefix}}yt baseurl clear</code>  恢复官方地址

<b>Cloudflare Workers 反代</b>
Gemini 官方 API 无法直连时，可部署下面的 Worker，再把 workers.dev 地址交给 <code>{{prefix}}yt baseurl</code>。

<pre>export default {
  async fetch(request) {
    const url = new URL(request.url);
    url.hostname = 'generativelanguage.googleapis.com';
    return fetch(new Request(url.toString(), {
      method: request.method,
      headers: request.headers,
      body: request.body,
    }));
  }
};</pre>

示例：<code>{{prefix}}yt baseurl https://你的项目.workers.dev</code>

该反代只处理 Gemini 元数据接口。YouTube 下载受地区、服务器 IP 或登录验证限制时，请配置下面的代理与 Cookies。

<b>YouTube 下载配置</b>
• <code>{{prefix}}yt setup</code>  安装或重装 yt-dlp
• <code>{{prefix}}yt update</code>  更新 yt-dlp
• <code>{{prefix}}yt doctor</code>  检查 yt-dlp、FFmpeg、Deno、代理和 Cookies
• <code>{{prefix}}yt proxy &lt;地址&gt;</code>  设置 HTTP(S) 或 SOCKS 代理
• <code>{{prefix}}yt proxy clear</code>  清除代理
• <code>{{prefix}}yt cookies &lt;文件路径&gt;</code>  设置 Netscape Cookies 文件
• <code>{{prefix}}yt cookies clear</code>  清除 Cookies
• <code>{{prefix}}yt runtime &lt;Deno路径|auto|none&gt;</code>  设置 JS 运行时
• <code>{{prefix}}yt binary &lt;路径|auto&gt;</code>  指定 yt-dlp
• <code>{{prefix}}yt clear</code>  清理临时文件

出现 <code>Sign in to confirm you’re not a bot</code> 时需要 Cookies；代理不能替代 Cookies。`
