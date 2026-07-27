package yvlu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	_ "golang.org/x/image/webp"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const maxMediaBytes = 20 << 20

type options struct {
	Count        int
	IncludeReply bool
	FakeText     string
	FakeSender   string
}

type Plugin struct {
	services service.Container
	workDir  string
	renderer *renderer
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
		Version:     "0.2.0",
		Description: "纯 Go 本地生成 Telegram 消息语录贴纸",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "yvlu",
		Description: "把回复的消息生成语录贴纸",
		Usage: []string{
			"yvlu [消息数]（回复消息）",
			"yvlu r [消息数]（包含回复引用）",
			"yvlu f <伪造消息>",
			"yvlu fr <伪造消息>（包含回复引用）",
			"yvlu u <用户ID|用户名> [消息数]",
			"yvlu ur <用户ID|用户名> [消息数]",
		},
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error {
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return err
	}
	renderer, err := newRenderer(p.services.AssetsDir)
	if err != nil {
		return fmt.Errorf("load quote font: %w", err)
	}
	p.renderer = renderer
	return nil
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	parsed, err := parseOptions(request.RawArgs)
	if err != nil {
		return p.respond(ctx, request, helpText(request.Prefix)+"\n\n❌ "+err.Error())
	}
	if request.Message.ReplyToID <= 0 {
		return p.respond(ctx, request, "❌ 请回复一条消息")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.respond(ctx, request, "⏳ 生成语录贴纸…"); err != nil {
		return err
	}
	messages, err := p.collectMessages(
		ctx,
		request.Message.ChatID,
		request.Message.ReplyToID,
		parsed.Count,
	)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取消息失败："+err.Error())
	}
	var fakeUser telegram.User
	if parsed.FakeSender != "" {
		fakeUser, err = p.services.Telegram.ResolveUser(ctx, parsed.FakeSender)
		if err != nil {
			return p.respond(ctx, request,
				"❌ 无法获取伪造发送者："+err.Error())
		}
	}
	items := make([]quoteItem, 0, len(messages))
	for index, message := range messages {
		item, err := p.quoteItem(ctx, request.Message.ChatID, message, parsed, fakeUser)
		if err != nil {
			return p.respond(ctx, request, "❌ 处理消息失败："+err.Error())
		}
		if index == 0 && parsed.FakeText != "" {
			item.Text = parsed.FakeText
		}
		items = append(items, item)
	}
	data, err := p.renderer.render(items)
	if err != nil {
		return p.respond(ctx, request, "❌ 渲染语录失败："+err.Error())
	}
	jobDir, err := os.MkdirTemp(p.workDir, "job-*")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建临时目录失败："+err.Error())
	}
	defer os.RemoveAll(jobDir)
	output := filepath.Join(jobDir, "quote.png")
	if err := os.WriteFile(output, data, 0o600); err != nil {
		return p.respond(ctx, request, "❌ 保存语录贴纸失败："+err.Error())
	}
	_, err = p.services.Telegram.SendFile(
		ctx,
		request.Message.ChatID,
		telegram.Upload{
			Path:         output,
			FileName:     "quote.png",
			MIMEType:     "image/png",
			ReplyToID:    request.Message.ReplyToID,
			Kind:         telegram.MediaSticker,
			StickerEmoji: "💬",
		},
	)
	if err != nil {
		return p.respond(ctx, request, "❌ 发送语录贴纸失败："+err.Error())
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

func (p *Plugin) collectMessages(
	ctx context.Context,
	chatID int64,
	firstID int,
	count int,
) ([]telegram.Message, error) {
	byID := make(map[int]telegram.Message)
	exact, err := p.services.Telegram.GetMessages(ctx, chatID, []int{firstID})
	if err != nil || len(exact) == 0 {
		return nil, firstNonNil(err, telegram.ErrMessageNotFound)
	}
	byID[firstID] = exact[0]
	if count > 1 {
		history, historyErr := p.services.Telegram.GetHistory(
			ctx,
			telegram.HistoryQuery{
				ChatID:    chatID,
				Limit:     minInt(100, count+10),
				OffsetID:  firstID,
				AddOffset: -count - 5,
				MinID:     firstID - 1,
			},
		)
		if historyErr == nil {
			for _, message := range history {
				if message.ID >= firstID {
					byID[message.ID] = message
				}
			}
		}
	}
	if len(byID) < count {
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

func (p *Plugin) quoteItem(
	ctx context.Context,
	chatID int64,
	message telegram.Message,
	parsed options,
	fakeUser telegram.User,
) (quoteItem, error) {
	user := fakeUser
	senderID := message.SenderID
	if user.ID == 0 && message.ForwardSenderID != 0 {
		senderID = message.ForwardSenderID
	}
	if user.ID == 0 && senderID != 0 {
		resolved, err := p.services.Telegram.ResolveUser(
			ctx, strconv.FormatInt(senderID, 10),
		)
		if err == nil {
			user = resolved
		}
	}
	sender := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if sender == "" {
		sender = message.ForwardName
	}
	if sender == "" {
		sender = firstNonEmpty(user.Username, strconv.FormatInt(senderID, 10))
	}
	item := quoteItem{
		Sender: sender,
		Text:   message.Text,
	}
	if user.ID != 0 {
		var avatar bytes.Buffer
		if err := p.services.Telegram.DownloadProfilePhoto(
			ctx,
			user.ID,
			&avatar,
		); err == nil {
			item.Avatar, _, _ = image.Decode(bytes.NewReader(avatar.Bytes()))
		}
	}
	if parsed.IncludeReply && message.ReplyQuote != "" {
		item.Reply = message.ReplyQuote
	} else if parsed.IncludeReply && message.ReplyToID > 0 {
		replied, err := p.services.Telegram.GetMessages(
			ctx,
			chatID,
			[]int{message.ReplyToID},
		)
		if err == nil && len(replied) > 0 {
			replySender := p.senderLabel(ctx, replied[0].SenderID)
			item.Reply = replySender + "：" + messageDisplay(replied[0])
		}
	}
	if message.Media != nil && message.Media.Size <= maxMediaBytes &&
		(message.Media.Kind == telegram.MediaPhoto ||
			message.Media.Kind == telegram.MediaSticker) {
		var media bytes.Buffer
		if _, err := p.services.Telegram.DownloadMedia(
			ctx,
			chatID,
			message.ID,
			&media,
		); err == nil {
			item.Media, _, _ = image.Decode(bytes.NewReader(media.Bytes()))
		}
	}
	return item, nil
}

func (p *Plugin) senderLabel(ctx context.Context, senderID int64) string {
	if senderID == 0 {
		return "Unknown"
	}
	user, err := p.services.Telegram.ResolveUser(
		ctx,
		strconv.FormatInt(senderID, 10),
	)
	if err != nil {
		return strconv.FormatInt(senderID, 10)
	}
	return firstNonEmpty(
		strings.TrimSpace(user.FirstName+" "+user.LastName),
		user.Username,
		strconv.FormatInt(senderID, 10),
	)
}

func parseOptions(raw string) (options, error) {
	result := options{Count: 1}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return result, nil
	}
	if count, err := strconv.Atoi(fields[0]); err == nil {
		result.Count = count
		return validateOptions(result)
	}
	mode := strings.ToLower(fields[0])
	switch mode {
	case "r":
		result.IncludeReply = true
		if len(fields) >= 2 {
			count, err := strconv.Atoi(fields[1])
			if err != nil {
				return result, errors.New("消息数必须是数字")
			}
			result.Count = count
		}
	case "u", "ur":
		if len(fields) < 2 {
			return result, errors.New("请指定用户 ID 或用户名")
		}
		result.IncludeReply = mode == "ur"
		result.FakeSender = fields[1]
		if len(fields) >= 3 {
			count, err := strconv.Atoi(fields[2])
			if err != nil {
				return result, errors.New("消息数必须是数字")
			}
			result.Count = count
		}
	case "f", "fr":
		if len(fields) < 2 {
			return result, errors.New("请提供伪造消息内容")
		}
		result.IncludeReply = mode == "fr"
		result.FakeText = strings.TrimSpace(
			strings.TrimPrefix(strings.TrimSpace(raw), fields[0]),
		)
	default:
		return result, errors.New("参数无效")
	}
	return validateOptions(result)
}

func validateOptions(value options) (options, error) {
	if value.Count < 1 || value.Count > 5 {
		return value, errors.New("消息数必须在 1 到 5 之间")
	}
	return value, nil
}

func firstNonNil(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func messageDisplay(message telegram.Message) string {
	if strings.TrimSpace(message.Text) != "" {
		return strings.TrimSpace(message.Text)
	}
	if message.Media != nil {
		return "[" + string(message.Media.Kind) + "]"
	}
	return "[空消息]"
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
	return "💬 本地语录贴纸\n\n" +
		"回复消息后使用：\n" +
		prefix + "yvlu [消息数]  不包含回复引用\n" +
		prefix + "yvlu r [消息数]  包含回复引用\n" +
		prefix + "yvlu f <伪造消息>\n" +
		prefix + "yvlu fr <伪造消息>  包含回复引用\n" +
		prefix + "yvlu u <用户ID/用户名> [消息数]\n" +
		prefix + "yvlu ur <用户ID/用户名> [消息数]\n\n" +
		"消息数最多 5 条；全部在本地渲染，不上传到第三方 Quote API。"
}
