package ids

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type Plugin struct {
	services service.Container
}

func New(services service.Container) *Plugin {
	return &Plugin{services: services}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "ids",
		Version:     "0.3.1",
		Description: "查询用户、对话、消息及实体的详细信息",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{
		{
			Name:        "ids",
			Aliases:     []string{"id"},
			Description: "查询当前对话、回复对象、用户名、ID 或消息链接",
			Usage: []string{
				"id",
				"id <@用户名|用户ID|群组ID>",
				"id <Telegram消息链接>",
				"id（回复消息）",
			},
			HelpHTML:  idHelp("{{prefix}}"),
			OwnerOnly: true,
			Handler:   p.handleID,
		},
		{
			Name:        "entity",
			Description: "以 JSON 查看用户或对话实体",
			Usage: []string{
				"entity [@用户名|用户ID|群组ID]",
				"entity（回复消息）",
			},
			HelpHTML:  entityHelp("{{prefix}}"),
			OwnerOnly: true,
			Handler:   p.handleEntity,
		},
		{
			Name:        "msg",
			Description: "以 JSON 查看回复消息的可移植字段",
			Usage:       []string{"msg（回复消息）"},
			HelpHTML:    messageHelp("{{prefix}}"),
			OwnerOnly:   true,
			Handler:     p.handleMessage,
		},
		{
			Name:        "echo",
			Description: "原样复制回复的文字或媒体消息",
			Usage:       []string{"echo（回复消息）"},
			HelpHTML:    echoHelp("{{prefix}}"),
			OwnerOnly:   true,
			Handler:     p.handleEcho,
		},
	}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handleID(ctx context.Context, request command.Request) error {
	if hasHelp(request.Args) {
		return p.respondHTML(ctx, request, idHelp(request.Prefix))
	}
	target := strings.TrimSpace(request.RawArgs)
	if target != "" {
		text, err := p.describeTarget(ctx, target)
		if err != nil {
			p.services.Logger.Warn("resolve id target failed", "target", target, "error", err)
			return p.respond(ctx, request, "❌ 无法解析目标："+err.Error())
		}
		return p.respondHTML(ctx, request, text)
	}

	var sections []string
	if request.Message.ReplyToID > 0 {
		message, err := p.repliedMessage(ctx, request)
		if err != nil {
			return p.respond(ctx, request, "❌ 无法读取回复消息："+err.Error())
		}
		if message.SenderID > 0 {
			if user, err := p.services.Telegram.ResolveUser(
				ctx,
				strconv.FormatInt(message.SenderID, 10),
			); err == nil {
				sections = append(sections, formatUser(user, "回复用户"))
			}
		}
		sections = append(sections, formatMessage(message))
	} else {
		if user, err := p.services.Telegram.ResolveUser(ctx, "me"); err == nil {
			sections = append(sections, formatUser(user, "当前账号"))
		}
		sections = append(sections, formatMessage(request.Message))
	}
	if chat, err := p.services.Telegram.ResolveChat(
		ctx,
		request.Message.ChatID,
	); err == nil {
		sections = append(sections, formatChat(chat))
	}
	if len(sections) == 0 {
		return p.respond(ctx, request, "❌ 未获取到可显示的信息")
	}
	return p.respondHTML(ctx, request, strings.Join(sections, "\n\n"))
}

func (p *Plugin) describeTarget(ctx context.Context, target string) (string, error) {
	if chatID, messageID, ok, err := p.parseMessageLink(ctx, target); err != nil {
		return "", err
	} else if ok {
		messages, err := p.services.Telegram.GetMessages(ctx, chatID, []int{messageID})
		if err != nil || len(messages) == 0 {
			if err == nil {
				err = telegram.ErrMessageNotFound
			}
			return "", err
		}
		message := messages[0]
		sections := []string{"<b>🔗 消息链接</b>", formatMessage(message)}
		if message.SenderID > 0 {
			if user, resolveErr := p.services.Telegram.ResolveUser(
				ctx,
				strconv.FormatInt(message.SenderID, 10),
			); resolveErr == nil {
				sections = append(sections, formatUser(user, "消息发送者"))
			}
		}
		if chat, resolveErr := p.services.Telegram.ResolveChat(ctx, chatID); resolveErr == nil {
			sections = append(sections, formatChat(chat))
		}
		return strings.Join(sections, "\n\n"), nil
	}

	normalized := strings.TrimSpace(target)
	if parsed, err := strconv.ParseInt(normalized, 10, 64); err == nil && parsed < 0 {
		chat, err := p.services.Telegram.ResolveChat(ctx, parsed)
		if err != nil {
			return "", err
		}
		return formatChat(chat), nil
	}
	if user, err := p.services.Telegram.ResolveUser(ctx, normalized); err == nil {
		return formatUser(user, "用户"), nil
	}
	chat, err := p.services.Telegram.ResolveChatTarget(ctx, normalized)
	if err != nil {
		return "", err
	}
	return formatChat(chat), nil
}

func (p *Plugin) handleEntity(ctx context.Context, request command.Request) error {
	if hasHelp(request.Args) {
		return p.respondHTML(ctx, request, entityHelp(request.Prefix))
	}
	target := strings.TrimSpace(request.RawArgs)
	if target == "" && request.Message.ReplyToID > 0 {
		message, err := p.repliedMessage(ctx, request)
		if err != nil {
			return p.respond(ctx, request, "❌ 无法读取回复消息："+err.Error())
		}
		if message.SenderID > 0 {
			target = strconv.FormatInt(message.SenderID, 10)
		}
	}
	var value any
	if target == "" {
		chat, err := p.services.Telegram.ResolveChat(ctx, request.Message.ChatID)
		if err != nil {
			return p.respond(ctx, request, "❌ 无法读取当前对话实体："+err.Error())
		}
		value = chat
	} else if parsed, err := strconv.ParseInt(target, 10, 64); err == nil && parsed < 0 {
		chat, err := p.services.Telegram.ResolveChat(ctx, parsed)
		if err != nil {
			return p.respond(ctx, request, "❌ 无法读取对话实体："+err.Error())
		}
		value = chat
	} else if user, err := p.services.Telegram.ResolveUser(ctx, target); err == nil {
		value = user
	} else {
		chat, err := p.services.Telegram.ResolveChatTarget(ctx, target)
		if err != nil {
			return p.respond(ctx, request, "❌ 无法解析实体："+err.Error())
		}
		value = chat
	}
	return p.respondJSON(ctx, request, "entity", value)
}

func (p *Plugin) handleMessage(ctx context.Context, request command.Request) error {
	if hasHelp(request.Args) {
		return p.respondHTML(ctx, request, messageHelp(request.Prefix))
	}
	if request.Message.ReplyToID <= 0 {
		return p.respond(ctx, request, "❌ 请回复一条消息后再使用 "+request.Prefix+"msg")
	}
	message, err := p.repliedMessage(ctx, request)
	if err != nil {
		return p.respond(ctx, request, "❌ 无法读取回复消息："+err.Error())
	}
	return p.respondJSON(ctx, request, fmt.Sprintf("msg_%d", message.ID), message)
}

func (p *Plugin) handleEcho(ctx context.Context, request command.Request) error {
	if hasHelp(request.Args) {
		return p.respondHTML(ctx, request, echoHelp(request.Prefix))
	}
	if request.Message.ReplyToID <= 0 {
		return p.respond(ctx, request, "❌ 请回复一条消息后再使用 "+request.Prefix+"echo")
	}
	if err := p.services.Telegram.CopyMessages(
		ctx,
		request.Message.ChatID,
		request.Message.ChatID,
		[]int{request.Message.ReplyToID},
	); err != nil {
		return p.respond(ctx, request, "❌ 原样复制消息失败："+err.Error())
	}
	if request.Message.Outgoing {
		return p.services.Telegram.DeleteMessages(
			ctx,
			request.Message.ChatID,
			[]int{request.Message.ID},
		)
	}
	return p.respond(ctx, request, "✅ 消息已复制")
}

func (p *Plugin) repliedMessage(
	ctx context.Context,
	request command.Request,
) (telegram.Message, error) {
	messages, err := p.services.Telegram.GetMessages(
		ctx,
		request.Message.ChatID,
		[]int{request.Message.ReplyToID},
	)
	if err != nil {
		return telegram.Message{}, err
	}
	if len(messages) == 0 {
		return telegram.Message{}, telegram.ErrMessageNotFound
	}
	return messages[0], nil
}

func (p *Plugin) parseMessageLink(
	ctx context.Context,
	value string,
) (int64, int, bool, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return 0, 0, false, nil
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "t.me" && host != "telegram.me" {
		return 0, 0, false, nil
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 3 && parts[0] == "c" {
		channel, idErr := strconv.ParseInt(parts[1], 10, 64)
		messageID, messageErr := strconv.Atoi(parts[2])
		if idErr != nil || messageErr != nil || channel <= 0 || messageID <= 0 {
			return 0, 0, true, errors.New("消息链接格式无效")
		}
		chatID, idErr := strconv.ParseInt("-100"+strconv.FormatInt(channel, 10), 10, 64)
		return chatID, messageID, true, idErr
	}
	if len(parts) == 2 {
		messageID, idErr := strconv.Atoi(parts[1])
		if idErr != nil || messageID <= 0 {
			return 0, 0, false, nil
		}
		chat, resolveErr := p.services.Telegram.ResolveChatTarget(ctx, "@"+parts[0])
		if resolveErr != nil {
			return 0, 0, true, resolveErr
		}
		return chat.ID, messageID, true, nil
	}
	return 0, 0, false, nil
}

func (p *Plugin) respondJSON(
	ctx context.Context,
	request command.Request,
	baseName string,
	value any,
) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return p.respond(ctx, request, "❌ 无法序列化数据："+err.Error())
	}
	if len(data) <= 3000 {
		return p.respondHTML(
			ctx,
			request,
			"<blockquote expandable><code>"+html.EscapeString(string(data))+
				"</code></blockquote>",
		)
	}
	directory := filepath.Join(p.services.AssetsDir, "ids", "temp")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return p.respond(ctx, request, "❌ 保存 JSON 失败："+err.Error())
	}
	file, err := os.CreateTemp(directory, baseName+"-*.json")
	if err != nil {
		return p.respond(ctx, request, "❌ 保存 JSON 失败："+err.Error())
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0o600); err == nil {
		_, err = file.Write(data)
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return p.respond(ctx, request, "❌ 保存 JSON 失败："+errors.Join(err, closeErr).Error())
	}
	_, err = p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:      path,
		FileName:  baseName + ".json",
		MIMEType:  "application/json",
		Caption:   "TeleBox-Go 调试信息",
		ReplyToID: request.Message.ID,
		Kind:      telegram.MediaDocument,
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 发送 JSON 失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 完整数据已作为 JSON 文件发送")
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

func formatUser(user telegram.User, titles ...string) string {
	title := "用户"
	if len(titles) > 0 && strings.TrimSpace(titles[0]) != "" {
		title = titles[0]
	}
	displayName := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if displayName == "" {
		displayName = "未设置"
	}
	username := "未设置"
	if user.Username != "" {
		username = "@" + user.Username
	}
	dc := "无头像"
	if user.PhotoDC > 0 {
		dc = fmt.Sprintf("DC%d", user.PhotoDC)
	}
	var tags []string
	if user.Bot {
		tags = append(tags, "机器人")
	}
	if user.Verified {
		tags = append(tags, "已认证")
	}
	if user.Premium {
		tags = append(tags, "Premium")
	}
	if user.Scam {
		tags = append(tags, "诈骗标记")
	}
	if user.Fake {
		tags = append(tags, "虚假标记")
	}
	if user.Deleted {
		tags = append(tags, "已注销")
	}
	status := "普通账号"
	if len(tags) > 0 {
		status = strings.Join(tags, " · ")
	}
	bio := strings.TrimSpace(user.Bio)
	if bio == "" {
		bio = "无"
	}
	if runes := []rune(bio); len(runes) > 200 {
		bio = string(runes[:200]) + "…"
	}
	profileLink := fmt.Sprintf("tg://user?id=%d", user.ID)
	chatLink := fmt.Sprintf("https://t.me/@id%d", user.ID)
	if user.Username != "" {
		chatLink = "https://t.me/" + user.Username
	}
	messageLink := fmt.Sprintf("tg://openmessage?user_id=%d", user.ID)
	return fmt.Sprintf(
		"<b>👤 %s</b>\n"+
			"• 名称：<code>%s</code>\n"+
			"• 用户名：<code>%s</code>\n"+
			"• ID：<code>%d</code>\n"+
			"• 数据中心：<code>%s</code>\n"+
			"• 共同群组：<code>%d 个</code>\n"+
			"• 状态：%s\n"+
			"• 简介：%s\n"+
			"\n<b>跳转链接</b>\n"+
			"• <a href=\"%s\">用户资料</a>\n"+
			"• <a href=\"%s\">聊天链接</a>\n"+
			"• <a href=\"%s\">打开消息</a>\n"+
			"\n<b>链接文本</b>\n"+
			"• <code>%s</code>\n"+
			"• <code>%s</code>\n"+
			"• <code>%s</code>",
		html.EscapeString(title),
		html.EscapeString(displayName),
		html.EscapeString(username),
		user.ID,
		html.EscapeString(dc),
		user.CommonChats,
		html.EscapeString(status),
		html.EscapeString(bio),
		html.EscapeString(profileLink),
		html.EscapeString(chatLink),
		html.EscapeString(messageLink),
		html.EscapeString(profileLink),
		html.EscapeString(chatLink),
		html.EscapeString(messageLink),
	)
}

func formatChat(chat telegram.Chat) string {
	username := "未设置"
	if chat.Username != "" {
		username = "@" + chat.Username
	}
	dc := "无头像"
	if chat.PhotoDC > 0 {
		dc = fmt.Sprintf("DC%d", chat.PhotoDC)
	}
	link := chat.InviteLink
	if link == "" && chat.Username != "" {
		link = "https://t.me/" + chat.Username
	}
	linkLine := ""
	if link != "" {
		linkLine = "\n• 链接：<a href=\"" + html.EscapeString(link) + "\">打开对话</a>"
	}
	return fmt.Sprintf(
		"<b>💬 对话</b>\n"+
			"• 标题：<code>%s</code>\n"+
			"• 类型：<code>%s</code>\n"+
			"• 用户名：<code>%s</code>\n"+
			"• ID：<code>%d</code>\n"+
			"• 数据中心：<code>%s</code>\n"+
			"• 成员：<code>%d</code>\n"+
			"• 论坛：<code>%t</code>%s",
		html.EscapeString(chat.Title),
		html.EscapeString(string(chat.Kind)),
		html.EscapeString(username),
		chat.ID,
		html.EscapeString(dc),
		chat.MemberCount,
		chat.Forum,
		linkLine,
	)
}

func formatMessage(message telegram.Message) string {
	media := "无"
	if message.Media != nil {
		media = string(message.Media.Kind)
		if message.Media.FileName != "" {
			media += " · " + message.Media.FileName
		}
	}
	date := "未知"
	if !message.Date.IsZero() {
		date = message.Date.Local().Format(time.DateTime)
	}
	return fmt.Sprintf(
		"<b>✉️ 消息</b>\n"+
			"• 消息 ID：<code>%d</code>\n"+
			"• 对话 ID：<code>%d</code>\n"+
			"• 发送者 ID：<code>%d</code>\n"+
			"• 回复消息 ID：<code>%d</code>\n"+
			"• 媒体：<code>%s</code>\n"+
			"• 时间：<code>%s</code>",
		message.ID,
		message.ChatID,
		message.SenderID,
		message.ReplyToID,
		html.EscapeString(media),
		html.EscapeString(date),
	)
}

func hasHelp(args []string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, "help") || strings.EqualFold(arg, "h") {
			return true
		}
	}
	return false
}

func idHelp(prefix string) string {
	commandName := html.EscapeString(prefix + "id")
	return "<b>🆔 身份与对话信息</b>\n\n" +
		"• <code>" + commandName + "</code>  当前对话和账号\n" +
		"• <code>" + commandName + " @用户名</code>\n" +
		"• <code>" + commandName + " 用户ID或群组ID</code>\n" +
		"• <code>" + commandName + " Telegram消息链接</code>\n" +
		"• 回复消息后发送 <code>" + commandName + "</code>"
}

func entityHelp(prefix string) string {
	name := html.EscapeString(prefix + "entity")
	return "<b>🧩 实体数据</b>\n\n• <code>" + name +
		" [@用户名|用户ID|群组ID]</code>\n• 也可回复消息后发送 <code>" + name + "</code>"
}

func messageHelp(prefix string) string {
	name := html.EscapeString(prefix + "msg")
	return "<b>✉️ 消息数据</b>\n\n回复一条消息后发送 <code>" + name + "</code>"
}

func echoHelp(prefix string) string {
	name := html.EscapeString(prefix + "echo")
	return "<b>📨 原样复制</b>\n\n回复一条消息后发送 <code>" + name + "</code>"
}
