package ai

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const maxVisionBytes = 20 << 20

type Plugin struct {
	services service.Container
	workDir  string
	configMu sync.Mutex
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services: services,
		workDir:  filepath.Join(os.TempDir(), "telebox-go-ai"),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "ai",
		Version:     "0.2.0",
		Description: "调用 Gemini、OpenAI、Claude、DeepSeek、Grok 与第三方模型",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "ai",
		Description: "模型对话、搜索、图片、语音与服务商管理",
		Usage: []string{
			"ai <问题>（可回复文本、图片、音频或文档）",
			"ai search <问题>",
			"ai image <提示词>",
			"ai tts <文本>",
			"ai audio <问题>",
			"ai searchaudio <问题>",
			"ai settings|status",
			"ai select <gemini|openai|claude|deepseek|grok|thirdparty>",
			"ai apikey <服务商> <密钥|clear>",
			"ai baseurl <服务商> <URL|clear>",
			"ai thirdparty compat <兼容协议>",
			"ai model list|set <用途> <模型>|auto",
			"ai chatmodel|searchmodel|imagemodel|ttsmodel [模型]",
			"ai ttsvoice [音色]",
			"ai maxtokens [数量]",
			"ai context <on|off|show|clear>",
			"ai prompt <add|del|list|set|show> ...",
			"ai collapse [on|off]",
			"ai telegraph <on|off|limit <字符数>|list|del <ID|all>>",
			"ai config default",
		},
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(ctx context.Context) error {
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return err
	}
	if err := p.migrateLegacy(ctx); err != nil {
		p.services.Logger.Warn("migrate legacy AI config", "error", err)
	}
	return nil
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 {
		reply, _ := p.repliedMessage(ctx, request)
		if reply.Text == "" && reply.Media == nil && request.Message.Media == nil {
			return p.respond(ctx, request, helpText(request.Prefix))
		}
		return p.handleChat(ctx, request, "", false, false)
	}

	subcommand := strings.ToLower(request.Args[0])
	rest := strings.TrimSpace(strings.TrimPrefix(request.RawArgs, request.Args[0]))
	switch subcommand {
	case "help":
		return p.respond(ctx, request, helpText(request.Prefix))
	case "search":
		return p.handleChat(ctx, request, rest, true, false)
	case "image":
		return p.handleImage(ctx, request, rest)
	case "tts":
		return p.handleTTS(ctx, request, rest)
	case "audio":
		return p.handleChat(ctx, request, rest, false, true)
	case "searchaudio":
		return p.handleChat(ctx, request, rest, true, true)
	case "settings":
		return p.handleSettings(ctx, request)
	case "status":
		return p.handleStatus(ctx, request)
	case "select":
		return p.handleSelect(ctx, request, request.Args[1:])
	case "apikey":
		return p.handleAPIKey(ctx, request, request.Args[1:])
	case "baseurl":
		return p.handleBaseURL(ctx, request, request.Args[1:])
	case "thirdparty":
		return p.handleThirdParty(ctx, request, request.Args[1:])
	case "model":
		return p.handleModel(ctx, request, request.Args[1:])
	case "chatmodel", "searchmodel", "imagemodel", "ttsmodel":
		return p.handleModelShortcut(ctx, request, subcommand, rest)
	case "ttsvoice":
		return p.handleVoice(ctx, request, request.Args[1:])
	case "maxtokens":
		return p.handleMaxTokens(ctx, request, request.Args[1:])
	case "context":
		return p.handleContext(ctx, request, request.Args[1:])
	case "prompt":
		return p.handlePrompt(ctx, request, request.Args[1:])
	case "collapse":
		return p.handleToggle(ctx, request, "collapse", request.Args[1:])
	case "telegraph":
		return p.handleTelegraph(ctx, request, request.Args[1:])
	case "config":
		return p.handleConfig(ctx, request, request.Args[1:])
	default:
		return p.handleChat(ctx, request, request.RawArgs, false, false)
	}
}

func (p *Plugin) handleChat(
	ctx context.Context,
	request command.Request,
	question string,
	search bool,
	audio bool,
) error {
	reply, _ := p.repliedMessage(ctx, request)
	display, prompt := combineQuestion(question, reply.Text)
	image, err := p.visionImage(ctx, request, reply)
	if err != nil {
		p.services.Logger.Warn("read AI vision image", "error", err)
		return p.respond(ctx, request, "❌ 无法读取图片")
	}
	if prompt == "" {
		if image == nil {
			return p.respond(ctx, request, "❌ 请直接提问或回复一条文字/图片消息")
		}
		prompt = "用中文详细描述这张图片"
		display = "描述图片"
	}
	progress := "⏳ 生成回答…"
	if search {
		progress = "⏳ 搜索…"
	}
	if err := p.respond(ctx, request, progress); err != nil {
		return err
	}
	requested := featureChat
	if search {
		requested = featureSearch
	}
	var history []chatMessage
	if !search {
		history = p.history(ctx, request.Message.ChatID)
	}
	result, cfg, err := p.generateText(
		ctx,
		requested,
		prompt,
		history,
		image,
	)
	if err != nil {
		p.services.Logger.Warn("AI text request failed", "error", err)
		return p.respond(ctx, request, "❌ "+friendlyError(err))
	}
	if !search {
		if err := p.appendHistory(
			ctx,
			request.Message.ChatID,
			prompt,
			result.Text,
		); err != nil {
			p.services.Logger.Warn("save AI history", "error", err)
		}
	}
	if audio {
		if err := p.respond(ctx, request, "⏳ 生成语音…"); err != nil {
			return err
		}
		speech, speechCfg, speechErr := p.generateSpeech(ctx, result.Text)
		if speechErr != nil {
			text := formatAnswer(display, result.Text, cfg, search) +
				"\n\n⚠️ 语音生成失败：" + friendlyError(speechErr)
			text = p.publishLongAnswer(ctx, display, text)
			return p.finishText(
				ctx,
				request,
				text,
				reply.ID,
			)
		}
		return p.sendGeneratedFile(
			ctx,
			request,
			speech.Data,
			speech.MIME,
			"ai-answer",
			telegram.MediaVoice,
			truncateRunes(formatAnswer(display, result.Text, speechCfg, search), 900),
			reply.ID,
		)
	}
	plainAnswer := formatAnswer(display, result.Text, cfg, search)
	answer := p.publishLongAnswer(
		ctx,
		display,
		plainAnswer,
	)
	if answer == plainAnswer && len([]rune(answer)) <= 3500 {
		return p.finishAnswerHTML(
			ctx,
			request,
			display,
			result.Text,
			cfg,
			search,
			reply.ID,
		)
	}
	return p.finishText(
		ctx,
		request,
		answer,
		reply.ID,
	)
}

func (p *Plugin) finishAnswerHTML(
	ctx context.Context,
	request command.Request,
	question string,
	answer string,
	cfg requestConfig,
	search bool,
	replyTo int,
) error {
	quoteTag := "<blockquote>"
	if p.read(ctx, "collapse", "off") == "on" {
		quoteTag = "<blockquote expandable>"
	}
	var text strings.Builder
	if strings.TrimSpace(question) != "" {
		text.WriteString("<b>Q:</b>\n")
		text.WriteString(quoteTag)
		text.WriteString(html.EscapeString(strings.TrimSpace(question)))
		text.WriteString("</blockquote>\n\n")
	}
	text.WriteString("<b>A:</b>\n")
	text.WriteString(quoteTag)
	text.WriteString(html.EscapeString(strings.TrimSpace(answer)))
	text.WriteString("</blockquote>\n\n<i>— ")
	text.WriteString(html.EscapeString(providerLabel(cfg)))
	if search {
		text.WriteString(" · Search")
	}
	text.WriteString("</i>")
	if request.Message.Outgoing && replyTo == 0 {
		_, err := telegram.EditHTML(
			ctx,
			p.services.Telegram,
			request.Message.ChatID,
			request.Message.ID,
			text.String(),
		)
		return err
	}
	if replyTo == 0 {
		replyTo = request.Message.ID
	}
	if _, err := telegram.ReplyHTML(
		ctx,
		p.services.Telegram,
		request.Message.ChatID,
		replyTo,
		text.String(),
	); err != nil {
		return err
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

func (p *Plugin) handleImage(
	ctx context.Context,
	request command.Request,
	prompt string,
) error {
	reply, _ := p.repliedMessage(ctx, request)
	display, apiPrompt := combineQuestion(prompt, reply.Text)
	if apiPrompt == "" {
		return p.respond(ctx, request, "❌ 请提供图片生成提示或回复一条文字消息")
	}
	if err := p.respond(ctx, request, "⏳ 生成图片…"); err != nil {
		return err
	}
	result, cfg, err := p.generateImage(ctx, apiPrompt)
	if err != nil {
		p.services.Logger.Warn("AI image request failed", "error", err)
		return p.respond(ctx, request, "❌ "+friendlyError(err))
	}
	if len(result.Data) == 0 && result.URL != "" {
		result.Data, result.MIME, err = p.downloadGenerated(ctx, result.URL)
		if err != nil {
			p.services.Logger.Warn("download AI image", "error", err)
			return p.respond(ctx, request, "❌ 无法下载生成图片")
		}
	}
	if len(result.Data) == 0 {
		return p.respond(ctx, request, "❌ 图片接口没有返回图片数据")
	}
	return p.sendGeneratedFile(
		ctx,
		request,
		result.Data,
		firstNonEmpty(result.MIME, "image/png"),
		"ai-image",
		telegram.MediaPhoto,
		"🎨 "+display+"\n\n— "+providerLabel(cfg),
		reply.ID,
	)
}

func (p *Plugin) handleTTS(
	ctx context.Context,
	request command.Request,
	text string,
) error {
	reply, _ := p.repliedMessage(ctx, request)
	display, apiText := combineQuestion(text, reply.Text)
	if apiText == "" {
		return p.respond(ctx, request, "❌ 请提供要转换为语音的文本")
	}
	if err := p.respond(ctx, request, "⏳ 生成语音…"); err != nil {
		return err
	}
	result, cfg, err := p.generateSpeech(ctx, apiText)
	if err != nil {
		p.services.Logger.Warn("AI speech request failed", "error", err)
		return p.respond(ctx, request, "❌ "+friendlyError(err))
	}
	return p.sendGeneratedFile(
		ctx,
		request,
		result.Data,
		result.MIME,
		"ai-speech",
		telegram.MediaVoice,
		truncateRunes(display+"\n\n— "+providerLabel(cfg)+" · 语音", 900),
		reply.ID,
	)
}

func (p *Plugin) repliedMessage(
	ctx context.Context,
	request command.Request,
) (telegram.Message, error) {
	if request.Message.ReplyToID <= 0 {
		return telegram.Message{}, nil
	}
	messages, err := p.services.Telegram.GetMessages(
		ctx,
		request.Message.ChatID,
		[]int{request.Message.ReplyToID},
	)
	if err != nil || len(messages) == 0 {
		return telegram.Message{}, err
	}
	return messages[0], nil
}

func (p *Plugin) visionImage(
	ctx context.Context,
	request command.Request,
	reply telegram.Message,
) (*imageInput, error) {
	source := request.Message
	if source.Media == nil || source.Media.Kind != telegram.MediaPhoto {
		source = reply
	}
	if source.Media == nil || source.Media.Kind != telegram.MediaPhoto {
		return nil, nil
	}
	if source.Media.Size > maxVisionBytes {
		return nil, fmt.Errorf("图片超过 %d MiB 限制", maxVisionBytes>>20)
	}
	var buffer bytes.Buffer
	media, err := p.services.Telegram.DownloadMedia(
		ctx,
		source.ChatID,
		source.ID,
		&buffer,
	)
	if err != nil {
		return nil, err
	}
	if buffer.Len() > maxVisionBytes {
		return nil, fmt.Errorf("图片超过 %d MiB 限制", maxVisionBytes>>20)
	}
	return &imageInput{
		MIME: firstNonEmpty(media.MIMEType, "image/jpeg"),
		Data: buffer.Bytes(),
	}, nil
}

func (p *Plugin) sendGeneratedFile(
	ctx context.Context,
	request command.Request,
	data []byte,
	mimeType string,
	baseName string,
	kind telegram.MediaKind,
	caption string,
	replyTo int,
) error {
	if len(data) == 0 {
		return p.respond(ctx, request, "❌ 接口未返回音频文件")
	}
	if strings.Contains(strings.ToLower(mimeType), "audio/l16") ||
		strings.Contains(strings.ToLower(mimeType), "audio/pcm") {
		data = pcmToWAV(data, mimeType)
		mimeType = "audio/wav"
	}
	jobDir, err := os.MkdirTemp(p.workDir, "job-*")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(jobDir); cleanupErr != nil {
			p.services.Logger.Warn("remove AI job", "path", jobDir, "error", cleanupErr)
		}
	}()
	extension := extensionForMIME(mimeType, kind)
	path := filepath.Join(jobDir, baseName+extension)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if replyTo == 0 {
		replyTo = request.Message.ReplyToID
	}
	_, err = p.services.Telegram.SendFile(
		ctx,
		request.Message.ChatID,
		telegram.Upload{
			Path:      path,
			FileName:  baseName + extension,
			MIMEType:  mimeType,
			Caption:   caption,
			ReplyToID: replyTo,
			Kind:      kind,
		},
	)
	if err != nil {
		return p.respond(ctx, request, "❌ 发送生成文件失败："+friendlyError(err))
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

func (p *Plugin) finishText(
	ctx context.Context,
	request command.Request,
	text string,
	replyTo int,
) error {
	parts := splitText(text, 3900)
	if len(parts) == 0 {
		parts = []string{"未返回内容"}
	}
	if request.Message.Outgoing && replyTo == 0 {
		if _, err := p.services.Telegram.EditText(
			ctx,
			request.Message.ChatID,
			request.Message.ID,
			parts[0],
		); err != nil {
			return err
		}
	} else {
		if replyTo == 0 {
			replyTo = request.Message.ID
		}
		if _, err := p.services.Telegram.ReplyText(
			ctx,
			request.Message.ChatID,
			replyTo,
			parts[0],
		); err != nil {
			return err
		}
		if request.Message.Outgoing {
			if err := p.services.Telegram.DeleteMessages(
				ctx,
				request.Message.ChatID,
				[]int{request.Message.ID},
			); err != nil {
				return err
			}
		}
	}
	for _, part := range parts[1:] {
		if _, err := p.services.Telegram.SendText(
			ctx,
			request.Message.ChatID,
			part,
		); err != nil {
			return err
		}
	}
	return nil
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

func combineQuestion(question, replyText string) (display, prompt string) {
	question = strings.TrimSpace(question)
	replyText = strings.TrimSpace(replyText)
	switch {
	case question != "" && replyText != "":
		return question, "原消息内容：" + replyText + "\n\n问题：" + question
	case question != "":
		return question, question
	case replyText != "":
		return replyText, replyText
	default:
		return "", ""
	}
}

func formatAnswer(
	question string,
	answer string,
	cfg requestConfig,
	search bool,
) string {
	var result strings.Builder
	result.WriteString(strings.TrimSpace(answer))
	result.WriteString("\n\n— ")
	result.WriteString(providerLabel(cfg))
	if search {
		result.WriteString(" · Search")
	}
	return result.String()
}

func providerLabel(cfg requestConfig) string {
	switch cfg.Selected {
	case providerGemini:
		return "Google Gemini (" + cfg.Model + ")"
	case providerOpenAI:
		return "OpenAI (" + cfg.Model + ")"
	case providerClaude:
		return "Anthropic Claude (" + cfg.Model + ")"
	case providerDeepSeek:
		return "DeepSeek (" + cfg.Model + ")"
	case providerGrok:
		return "xAI Grok (" + cfg.Model + ")"
	default:
		return "Third-party " + strings.ToUpper(string(cfg.Protocol)) +
			" (" + cfg.Model + ")"
	}
}

func extensionForMIME(mimeType string, kind telegram.MediaKind) string {
	mimeType = strings.ToLower(strings.Split(mimeType, ";")[0])
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	}
	if kind == telegram.MediaPhoto {
		return ".png"
	}
	if kind == telegram.MediaVoice {
		return ".ogg"
	}
	return ".bin"
}

func pcmToWAV(data []byte, mimeType string) []byte {
	sampleRate := 24000
	if index := strings.Index(strings.ToLower(mimeType), "rate="); index >= 0 {
		value := mimeType[index+5:]
		if end := strings.IndexAny(value, "; "); end >= 0 {
			value = value[:end]
		}
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			sampleRate = parsed
		}
	}
	result := make([]byte, 44+len(data))
	copy(result[0:4], "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], uint32(36+len(data)))
	copy(result[8:12], "WAVE")
	copy(result[12:16], "fmt ")
	binary.LittleEndian.PutUint32(result[16:20], 16)
	binary.LittleEndian.PutUint16(result[20:22], 1)
	binary.LittleEndian.PutUint16(result[22:24], 1)
	binary.LittleEndian.PutUint32(result[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(result[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(result[32:34], 2)
	binary.LittleEndian.PutUint16(result[34:36], 16)
	copy(result[36:40], "data")
	binary.LittleEndian.PutUint32(result[40:44], uint32(len(data)))
	copy(result[44:], data)
	return result
}

func splitText(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 {
		return nil
	}
	runes := []rune(text)
	result := make([]string, 0, len(runes)/limit+1)
	for len(runes) > limit {
		cut := limit
		for index := limit; index > limit/2; index-- {
			if runes[index-1] == '\n' {
				cut = index
				break
			}
		}
		result = append(result, strings.TrimSpace(string(runes[:cut])))
		runes = runes[cut:]
	}
	if tail := strings.TrimSpace(string(runes)); tail != "" {
		result = append(result, tail)
	}
	return result
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

func friendlyError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	lower := strings.ToLower(text)
	switch {
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(lower, "timeout"):
		return "请求超时"
	case strings.Contains(lower, "401"), strings.Contains(lower, "unauthorized"):
		return "API Key 无效"
	case strings.Contains(lower, "403"), strings.Contains(lower, "forbidden"):
		return "接口拒绝访问"
	case strings.Contains(lower, "429"), strings.Contains(lower, "rate limit"):
		return "额度不足或频率受限"
	}
	return "模型服务返回错误"
}

func helpText(prefix string) string {
	return "✨ 模型工具\n\n" +
		"常用\n" +
		"• " + prefix + "ai <问题>\n" +
		"• " + prefix + "ai search <问题>\n" +
		"• " + prefix + "ai image <提示>\n" +
		"• " + prefix + "ai tts <文本>\n" +
		"• " + prefix + "ai audio <问题>\n\n" +
		"配置\n" +
		"• " + prefix + "ai settings\n" +
		"• " + prefix + "ai status\n" +
		"• " + prefix + "ai select <服务商>\n" +
		"• " + prefix + "ai model <list|set|auto>\n" +
		"• " + prefix + "ai context <on|off|show|clear>\n" +
		"• " + prefix + "ai prompt <add|del|list|set|show>"
}
