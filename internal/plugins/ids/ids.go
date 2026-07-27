package ids

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"

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
		Version:     "0.1.0",
		Description: "查询用户身份、DC 和资料信息",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "ids",
		Description: "查询自己、指定用户或回复用户的信息",
		OwnerOnly:   true,
		Handler:     p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if hasHelp(request.Args) {
		return p.respondHTML(ctx, request, helpText(request.Prefix))
	}
	target := "me"
	if len(request.Args) > 0 {
		target = request.Args[0]
	} else if request.Message.ReplyToID > 0 {
		messages, err := p.services.Telegram.GetMessages(
			ctx,
			request.Message.ChatID,
			[]int{request.Message.ReplyToID},
		)
		if err != nil {
			p.services.Logger.Warn("ids read replied message failed", "error", err)
			return p.respond(ctx, request, "❌ 无法读取回复消息")
		}
		if len(messages) > 0 && messages[0].SenderID > 0 {
			target = strconv.FormatInt(messages[0].SenderID, 10)
		}
	}
	if err := p.respond(ctx, request, "⏳ 查询用户…"); err != nil {
		return err
	}
	user, err := p.services.Telegram.ResolveUser(ctx, target)
	if err != nil {
		p.services.Logger.Warn("ids resolve user failed", "target", target, "error", err)
		return p.respond(ctx, request, "❌ 未找到该用户")
	}
	return p.respondHTML(ctx, request, formatUser(user))
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

func formatUser(user telegram.User) string {
	displayName := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if displayName == "" && user.Username != "" {
		displayName = "@" + user.Username
	}
	if displayName == "" {
		displayName = fmt.Sprintf("用户 %d", user.ID)
	}
	username := "无用户名"
	chatLink := fmt.Sprintf("tg://user?id=%d", user.ID)
	if user.Username != "" {
		username = "@" + user.Username
		chatLink = "https://t.me/" + user.Username
	}
	dc := "无头像"
	if user.PhotoDC > 0 {
		dc = fmt.Sprintf("DC%d", user.PhotoDC)
	}
	var tags []string
	if user.Bot {
		tags = append(tags, "🤖 机器人")
	}
	if user.Verified {
		tags = append(tags, "✅ 已验证")
	}
	if user.Premium {
		tags = append(tags, "⭐ Premium")
	}
	if user.Scam {
		tags = append(tags, "⚠️ 诈骗")
	}
	if user.Fake {
		tags = append(tags, "❌ 虚假")
	}
	if user.Deleted {
		tags = append(tags, "🗑️ 已注销")
	}
	status := "无"
	if len(tags) > 0 {
		status = strings.Join(tags, " ")
	}
	bio := strings.TrimSpace(user.Bio)
	if bio == "" {
		bio = "无简介"
	}
	bioRunes := []rune(bio)
	if len(bioRunes) > 200 {
		bio = string(bioRunes[:200]) + "…"
	}
	profileLink := fmt.Sprintf("tg://user?id=%d", user.ID)
	openLink := fmt.Sprintf("tg://openmessage?user_id=%d", user.ID)
	return fmt.Sprintf(
		"<b>👤 用户信息</b>\n\n"+
			"<b>基本资料</b>\n"+
			"• 显示名称：<code>%s</code>\n"+
			"• 用户名：<code>%s</code>\n"+
			"• 用户 ID：<code>%d</code>\n"+
			"• 数据中心：<code>%s</code>\n"+
			"• 共同群组：<code>%d 个</code>\n"+
			"• 账号状态：%s\n\n"+
			"<b>简介</b>\n%s\n\n"+
			"<b>跳转</b>\n"+
			"• <a href=\"%s\">个人资料</a> · <a href=\"%s\">公开主页</a> · <a href=\"%s\">打开对话</a>",
		html.EscapeString(displayName),
		html.EscapeString(username),
		user.ID,
		html.EscapeString(dc),
		user.CommonChats,
		html.EscapeString(status),
		html.EscapeString(bio),
		html.EscapeString(profileLink),
		html.EscapeString(chatLink),
		html.EscapeString(openLink),
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

func helpText(prefix string) string {
	commandName := html.EscapeString(prefix + "ids")
	return "<b>🆔 用户信息</b>\n\n" +
		"<b>用法</b>\n" +
		"• <code>" + commandName + "</code>  查询自己\n" +
		"• <code>" + commandName + " @用户名</code>\n" +
		"• <code>" + commandName + " 用户ID</code>\n" +
		"• 回复目标消息后发送 <code>" + commandName + "</code>"
}
