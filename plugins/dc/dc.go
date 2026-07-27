package dc

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
)

type Plugin struct {
	services service.Container
}

func New(services service.Container) *Plugin {
	return &Plugin{services: services}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "dc",
		Version:     "0.2.0",
		Description: "查询用户、群组或频道头像所在数据中心",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "dc",
		Description: "查询头像 DC",
		Usage: []string{
			"dc",
			"dc <@用户名|用户ID>",
			"dc（回复目标消息）",
		},
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if len(request.Args) > 1 {
		return p.respond(ctx, request, "❌ 参数错误，最多只能指定一个用户")
	}
	if err := p.respond(ctx, request, "⏳ 读取 DC 信息…"); err != nil {
		return err
	}
	if len(request.Args) == 1 {
		return p.userDC(ctx, request, request.Args[0])
	}
	if request.Message.ReplyToID > 0 {
		messages, err := p.services.Telegram.GetMessages(
			ctx,
			request.Message.ChatID,
			[]int{request.Message.ReplyToID},
		)
		if err != nil || len(messages) == 0 {
			return p.respond(ctx, request, "❌ 无法获取回复的消息")
		}
		senderID := messages[0].SenderID
		if senderID > 0 {
			return p.userDC(ctx, request, strconv.FormatInt(senderID, 10))
		}
		if senderID != 0 {
			chat, chatErr := p.services.Telegram.ResolveChat(ctx, senderID)
			if chatErr == nil {
				return p.respond(ctx, request, formatChatDC(chat.Title, chat.PhotoDC))
			}
		}
		return p.respond(ctx, request, "❌ 无法获取回复消息发送者的 DC")
	}
	chat, err := p.services.Telegram.ResolveChat(ctx, request.Message.ChatID)
	if err != nil {
		p.services.Logger.Warn("dc resolve current chat failed", "error", err)
		return p.respond(ctx, request, "❌ 当前会话不是群组或频道")
	}
	return p.respond(ctx, request, formatChatDC(chat.Title, chat.PhotoDC))
}

func (p *Plugin) userDC(
	ctx context.Context,
	request command.Request,
	target string,
) error {
	user, err := p.services.Telegram.ResolveUser(ctx, target)
	if err != nil {
		p.services.Logger.Warn("dc resolve user failed", "target", target, "error", err)
		return p.respond(ctx, request, "❌ 未找到该用户")
	}
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name == "" {
		name = fallback(user.Username, fmt.Sprintf("用户 %d", user.ID))
	}
	if user.PhotoDC <= 0 {
		return p.respond(ctx, request, "❌ "+name+" 没有头像，无法获取 DC 信息")
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("📍 数据中心\n\n• 对象：%s\n• DC：DC%d", name, user.PhotoDC),
	)
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

func formatChatDC(title string, photoDC int) string {
	title = fallback(strings.TrimSpace(title), "当前聊天")
	if photoDC <= 0 {
		return "❌ " + title + " 没有头像，无法获取 DC 信息"
	}
	return fmt.Sprintf("📍 数据中心\n\n• 对象：%s\n• DC：DC%d", title, photoDC)
}

func fallback(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}
