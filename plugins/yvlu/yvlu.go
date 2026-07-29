package yvlu

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const (
	quoteAPIURL  = "https://quote-api-enhanced.zhetengsha.eu.org/generate.webp"
	quoteTimeout = 60 * time.Second
)

type options struct {
	Count        int
	IncludeReply bool
	FakeText     string
	FakeSender   string
}

type quoteImage struct {
	URL string `json:"url"`
}

type quoteMentionUser struct {
	ID int64 `json:"id"`
}

type quoteEntity struct {
	Offset        int               `json:"offset"`
	Length        int               `json:"length"`
	Type          string            `json:"type,omitempty"`
	URL           string            `json:"url,omitempty"`
	User          *quoteMentionUser `json:"user,omitempty"`
	CustomEmojiID string            `json:"custom_emoji_id,omitempty"`
}

type quoteSender struct {
	ID          int64       `json:"id"`
	FirstName   string      `json:"first_name"`
	LastName    string      `json:"last_name,omitempty"`
	Username    string      `json:"username,omitempty"`
	Photo       *quoteImage `json:"photo,omitempty"`
	EmojiStatus string      `json:"emoji_status,omitempty"`
}

type quoteReply struct {
	Name     string        `json:"name"`
	Text     string        `json:"text"`
	Entities []quoteEntity `json:"entities"`
	ChatID   int64         `json:"chatId,omitempty"`
}

type quoteMessage struct {
	From         quoteSender   `json:"from"`
	Text         string        `json:"text"`
	Entities     []quoteEntity `json:"entities"`
	Avatar       bool          `json:"avatar"`
	Media        *quoteImage   `json:"media,omitempty"`
	ReplyMessage *quoteReply   `json:"replyMessage,omitempty"`
}

type quoteRequest struct {
	Type            string         `json:"type"`
	Format          string         `json:"format"`
	BackgroundColor string         `json:"backgroundColor"`
	Width           int            `json:"width"`
	Height          int            `json:"height"`
	Scale           int            `json:"scale"`
	EmojiBrand      string         `json:"emojiBrand"`
	Messages        []quoteMessage `json:"messages"`
}

type senderInfo struct {
	ID          int64
	FirstName   string
	LastName    string
	Username    string
	EmojiStatus int64
}

type Plugin struct {
	services service.Container
	workDir  string
	mu       sync.Mutex
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services: services,
		workDir:  filepath.Join(os.TempDir(), "telebox-go-yvlu"),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "yvlu",
		Version:     "0.3.1",
		Description: "生成文字语录贴纸",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "yvlu",
		Description: "生成文字语录贴纸",
		Usage: []string{
			"yvlu [消息数]（回复消息）",
			"yvlu r [消息数]（包含回复）",
			"yvlu f <伪造消息>",
			"yvlu fr <伪造消息>（包含回复）",
			"yvlu u <用户ID|用户名> [消息数]",
			"yvlu ur <用户ID|用户名> [消息数]（包含回复）",
		},
		HelpHTML:  yvluGuideHTML,
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error {
	return os.MkdirAll(p.workDir, 0o700)
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	startedAt := time.Now()
	parsed, valid := parseOptions(request.RawArgs)
	if !valid {
		return p.respondHTML(ctx, request, helpHTML(request.Prefix))
	}
	if request.Message.ReplyToID <= 0 {
		return p.respond(ctx, request, "请回复一条消息")
	}
	if parsed.Count > 5 {
		return p.respond(ctx, request, "太多了 哒咩")
	}

	var fakeSender senderInfo
	var err error
	if parsed.FakeSender != "" {
		fakeSender, err = p.resolveSender(ctx, parsed.FakeSender)
		if err != nil {
			return p.respond(ctx, request,
				"无法获取 "+parsed.FakeSender+" 的信息，请检查用户ID/用户名是否正确")
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.respond(ctx, request, "正在生成语录贴纸..."); err != nil {
		return err
	}
	messages, err := p.collectMessages(
		ctx,
		request.Message.ChatID,
		request.Message.ReplyToID,
		parsed.Count,
	)
	if err != nil {
		return p.respond(ctx, request, "未找到消息")
	}

	items := make([]quoteMessage, 0, len(messages))
	for index, message := range messages {
		if index == 0 {
			if parsed.FakeText != "" {
				message.Text = parsed.FakeText
				message.Entities = fakeMessageEntities(
					request.Message,
					request.RawArgs,
					parsed.FakeText,
				)
			}
			if request.Message.ReplyQuote != "" {
				message.Text = request.Message.ReplyQuote
				message.Entities = append(
					[]telegram.MessageEntity(nil),
					request.Message.ReplyEntities...,
				)
			}
		}
		item, itemErr := p.quoteMessage(
			ctx,
			request.Message.ChatID,
			message,
			parsed.IncludeReply,
			fakeSender,
		)
		if itemErr != nil {
			return p.respond(ctx, request, itemErr.Error())
		}
		items = append(items, item)
	}

	document, err := json.Marshal(newQuoteRequest(items))
	if err != nil {
		return p.respond(ctx, request, "语录生成失败: "+err.Error())
	}
	image, err := p.generateQuote(ctx, document)
	if err != nil {
		return p.respond(ctx, request, "语录生成失败: "+err.Error())
	}
	if len(image) == 0 {
		return p.respond(ctx, request, "生成的图片数据为空")
	}

	jobDir, err := os.MkdirTemp(p.workDir, "quote-*")
	if err != nil {
		return p.respond(ctx, request, "发送文件失败: "+err.Error())
	}
	defer os.RemoveAll(jobDir)
	output := filepath.Join(jobDir, "sticker.webp")
	if err := os.WriteFile(output, image, 0o600); err != nil {
		return p.respond(ctx, request, "发送文件失败: "+err.Error())
	}
	if _, err := p.services.Telegram.SendFile(
		ctx,
		request.Message.ChatID,
		telegram.Upload{
			Path:         output,
			FileName:     "sticker.webp",
			MIMEType:     "image/webp",
			ReplyToID:    request.Message.ReplyToID,
			Kind:         telegram.MediaSticker,
			StickerEmoji: "quote",
		},
	); err != nil {
		return p.respond(ctx, request, "发送文件失败: "+err.Error())
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
	if p.services.Logger != nil {
		p.services.Logger.Info(
			"quote generated",
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
	}
	return nil
}

func newQuoteRequest(messages []quoteMessage) quoteRequest {
	return quoteRequest{
		Type:            "quote",
		Format:          "webp",
		BackgroundColor: "#1b1429",
		Width:           512,
		Height:          768,
		Scale:           2,
		EmojiBrand:      "apple",
		Messages:        messages,
	}
}

func (p *Plugin) generateQuote(ctx context.Context, document []byte) ([]byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, quoteTimeout)
	defer cancel()
	response, err := p.services.HTTP.Do(requestCtx, httpclient.Request{
		Method: http.MethodPost,
		URL:    quoteAPIURL,
		Headers: http.Header{
			"Content-Type": {"application/json"},
			"User-Agent":   {"TeleBox/0.2.1"},
		},
		Body:    document,
		Timeout: quoteTimeout,
	})
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("quote-api HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}

func (p *Plugin) quoteMessage(
	ctx context.Context,
	chatID int64,
	message telegram.Message,
	includeReply bool,
	fakeSender senderInfo,
) (quoteMessage, error) {
	sender := fakeSender
	if sender.ID == 0 {
		senderID := message.SenderID
		if message.ForwardSenderID != 0 {
			senderID = message.ForwardSenderID
		}
		if senderID != 0 {
			resolved, err := p.resolveSender(ctx, strconv.FormatInt(senderID, 10))
			if err == nil {
				sender = resolved
			}
		}
		if sender.ID == 0 && message.ForwardName != "" {
			sender.FirstName = message.ForwardName
		}
	}
	if sender.ID == 0 && strings.TrimSpace(sender.FirstName) == "" {
		return quoteMessage{}, errors.New("无法获取消息发送者信息")
	}

	from := quoteSender{
		ID:          sender.ID,
		FirstName:   sender.FirstName,
		LastName:    sender.LastName,
		Username:    sender.Username,
		EmojiStatus: optionalInt64(sender.EmojiStatus),
	}
	if sender.ID != 0 {
		var photo bytes.Buffer
		if err := p.services.Telegram.DownloadProfilePhoto(
			ctx,
			sender.ID,
			&photo,
		); err == nil && photo.Len() > 0 {
			from.Photo = &quoteImage{
				URL: "data:image/jpeg;base64," +
					base64.StdEncoding.EncodeToString(photo.Bytes()),
			}
		}
	}
	result := quoteMessage{
		From:     from,
		Text:     message.Text,
		Entities: quoteEntities(message.Entities),
		Avatar:   true,
	}
	if includeReply {
		result.ReplyMessage = p.replyMessage(ctx, chatID, message)
	}
	if message.Media != nil {
		var media bytes.Buffer
		downloaded, err := p.services.Telegram.DownloadMediaPreview(
			ctx,
			chatID,
			message.ID,
			&media,
		)
		if err == nil && media.Len() > 0 {
			mime := "image/jpeg"
			if downloaded.Kind == telegram.MediaSticker {
				mime = "image/webp"
			}
			result.Media = &quoteImage{
				URL: "data:" + mime + ";base64," +
					base64.StdEncoding.EncodeToString(media.Bytes()),
			}
		}
	}
	return result, nil
}

func (p *Plugin) replyMessage(
	ctx context.Context,
	chatID int64,
	message telegram.Message,
) *quoteReply {
	if message.ReplyQuote == "" && message.ReplyToID <= 0 {
		return nil
	}
	var replied telegram.Message
	if message.ReplyToID > 0 {
		messages, err := p.services.Telegram.GetMessages(
			ctx,
			chatID,
			[]int{message.ReplyToID},
		)
		if err == nil && len(messages) > 0 {
			replied = messages[0]
		}
	}
	senderName := "unknown"
	senderID := replied.SenderID
	if replied.ForwardSenderID != 0 {
		senderID = replied.ForwardSenderID
	}
	if senderID != 0 {
		if sender, err := p.resolveSender(
			ctx,
			strconv.FormatInt(senderID, 10),
		); err == nil {
			senderName = firstNonEmpty(
				strings.TrimSpace(sender.FirstName+" "+sender.LastName),
				sender.Username,
				"unknown",
			)
		}
	}
	if message.ReplyQuote != "" {
		return &quoteReply{
			Name:     senderName,
			Text:     message.ReplyQuote,
			Entities: quoteEntities(message.ReplyEntities),
			ChatID:   senderID,
		}
	}
	if strings.TrimSpace(replied.Text) == "" {
		return nil
	}
	return &quoteReply{
		Name:     senderName,
		Text:     replied.Text,
		Entities: quoteEntities(replied.Entities),
		ChatID:   senderID,
	}
}

func quoteEntities(values []telegram.MessageEntity) []quoteEntity {
	result := make([]quoteEntity, 0, len(values))
	for _, value := range values {
		entity := quoteEntity{
			Offset: value.Offset,
			Length: value.Length,
			Type:   value.Type,
			URL:    value.URL,
		}
		if value.DocumentID != 0 {
			entity.CustomEmojiID = strconv.FormatInt(value.DocumentID, 10)
		}
		if value.UserID != 0 {
			entity.User = &quoteMentionUser{ID: value.UserID}
		}
		result = append(result, entity)
	}
	return result
}

func fakeMessageEntities(
	message telegram.Message,
	rawArgs string,
	content string,
) []telegram.MessageEntity {
	rawOffset := strings.Index(message.Text, rawArgs)
	contentOffset := strings.Index(rawArgs, content)
	if rawOffset < 0 || contentOffset < 0 {
		return nil
	}
	cut := utf16Length(message.Text[:rawOffset+contentOffset])
	contentLength := utf16Length(content)
	result := make([]telegram.MessageEntity, 0, len(message.Entities))
	for _, entity := range message.Entities {
		start := entity.Offset
		end := entity.Offset + entity.Length
		if end <= cut || start >= cut+contentLength {
			continue
		}
		if start < cut {
			start = cut
		}
		if end > cut+contentLength {
			end = cut + contentLength
		}
		entity.Offset = start - cut
		entity.Length = end - start
		result = append(result, entity)
	}
	return result
}

func utf16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func (p *Plugin) resolveSender(ctx context.Context, target string) (senderInfo, error) {
	if user, err := p.services.Telegram.ResolveUser(ctx, target); err == nil {
		return senderInfo{
			ID:          user.ID,
			FirstName:   user.FirstName,
			LastName:    user.LastName,
			Username:    user.Username,
			EmojiStatus: user.EmojiStatus,
		}, nil
	}
	chat, err := p.services.Telegram.ResolveChatTarget(ctx, target)
	if err != nil {
		return senderInfo{}, err
	}
	return senderInfo{
		ID:        chat.ID,
		FirstName: chat.Title,
		Username:  chat.Username,
	}, nil
}

func (p *Plugin) collectMessages(
	ctx context.Context,
	chatID int64,
	firstID int,
	count int,
) ([]telegram.Message, error) {
	byID := make(map[int]telegram.Message)
	first, err := p.services.Telegram.GetMessages(ctx, chatID, []int{firstID})
	if err != nil || len(first) == 0 {
		return nil, firstNonNil(err, telegram.ErrMessageNotFound)
	}
	byID[firstID] = first[0]
	for start := firstID + 1; start <= firstID+1000 && len(byID) < count; start += 100 {
		ids := make([]int, 0, 100)
		for id := start; id < start+100; id++ {
			ids = append(ids, id)
		}
		found, scanErr := p.services.Telegram.GetMessages(ctx, chatID, ids)
		if scanErr != nil && !errors.Is(scanErr, telegram.ErrMessageNotFound) {
			return nil, scanErr
		}
		for _, message := range found {
			if message.ID >= firstID {
				byID[message.ID] = message
			}
		}
	}
	result := make([]telegram.Message, 0, len(byID))
	for _, message := range byID {
		result = append(result, message)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if len(result) > count {
		result = result[:count]
	}
	if len(result) == 0 {
		return nil, telegram.ErrMessageNotFound
	}
	return result, nil
}

func parseOptions(raw string) (options, bool) {
	result := options{Count: 1}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return result, true
	}
	if decimalDigits(fields[0]) {
		count, _ := strconv.Atoi(fields[0])
		result.Count = count
		if result.Count <= 0 {
			result.Count = 1
		}
		return result, true
	}
	switch strings.ToLower(fields[0]) {
	case "r":
		result.IncludeReply = true
		if len(fields) >= 2 {
			result.Count = parseCount(fields[1])
		}
	case "u", "ur":
		if len(fields) < 2 {
			return result, false
		}
		result.IncludeReply = strings.EqualFold(fields[0], "ur")
		result.FakeSender = fields[1]
		if len(fields) >= 3 {
			result.Count = parseCount(fields[2])
		}
	case "f", "fr":
		if len(fields) < 2 {
			return result, false
		}
		result.IncludeReply = strings.EqualFold(fields[0], "fr")
		result.FakeText = strings.TrimSpace(strings.TrimPrefix(
			strings.TrimSpace(raw),
			fields[0],
		))
	default:
		return result, false
	}
	return result, true
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func parseCount(value string) int {
	count, err := strconv.Atoi(value)
	if err != nil || count <= 0 {
		return 1
	}
	return count
}

func firstNonNil(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func optionalInt64(value int64) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
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

func helpHTML(prefix string) string {
	return strings.ReplaceAll(yvluGuideHTML, "{{prefix}}", html.EscapeString(prefix))
}

const yvluGuideHTML = `- 不包含回复

使用 <code>{{prefix}}yvlu [消息数]</code> 回复一条消息(支持选择部分引用回复) ⚠️ 不得超过 5 条

- 包含回复

<code>{{prefix}}yvlu r [消息数]</code> 回复一条消息(支持选择部分引用回复) ⚠️ 不得超过 5 条

- 伪造消息

<code>{{prefix}}yvlu f 伪造消息</code> 回复一条消息(支持富文本格式的伪造消息) ⚠️ 慎用

  - 包含回复

  <code>{{prefix}}yvlu fr 伪造消息</code> 回复一条消息(支持富文本格式的伪造消息) ⚠️ 慎用

- 伪造发送者

<code>{{prefix}}yvlu u 用户ID/用户名 [消息数]</code> 回复一条消息(支持选择部分引用回复) ⚠️ 慎用

  - 包含回复

  <code>{{prefix}}yvlu ur 用户ID/用户名 [消息数]</code> 回复一条消息(支持选择部分引用回复) ⚠️ 慎用`
