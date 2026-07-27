package isalive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type Plugin struct {
	services service.Container
	now      func() time.Time
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services: services,
		now:      time.Now,
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "isalive",
		Version:     "0.3.0",
		Description: "查询 Telegram 用户最后上线状态",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{
		{
			Name:        "isalive",
			Description: "查询用户名或 UID 的最后上线状态",
			Usage:       []string{"isalive <@用户名|用户ID>"},
			OwnerOnly:   true,
			Handler:     p.handle,
		},
	}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	target := strings.TrimSpace(request.RawArgs)
	if target == "" {
		return p.respond(ctx, request,
			fmt.Sprintf("❌ 用法：%sisalive <用户名或 UID>", request.Prefix),
		)
	}
	user, err := p.services.Telegram.ResolveUser(ctx, target)
	if err != nil {
		p.services.Logger.Warn("isalive failed to resolve user",
			"target", target,
			"error", err,
		)
		return p.respond(ctx, request, "❌ 未找到该用户")
	}
	return p.respond(ctx, request, formatStatus(user, p.now()))
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

func formatStatus(user telegram.User, now time.Time) string {
	name := strings.TrimSpace(strings.Join([]string{user.FirstName, user.LastName}, " "))
	if name == "" {
		name = "N/A"
	}
	username := "N/A"
	if user.Username != "" {
		username = "@" + user.Username
	}
	lastOnline, days := presenceText(user, now)
	deleted := "否"
	if user.Deleted {
		deleted = "是"
	}
	return fmt.Sprintf(
		"🟢 上线状态\n\n"+
			"• 用户：%s %s\n"+
			"• ID：%d\n"+
			"• 最后上线：%s\n"+
			"• 离线天数：%s\n"+
			"• 已注销：%s",
		name,
		username,
		user.ID,
		lastOnline,
		days,
		deleted,
	)
}

func presenceText(user telegram.User, now time.Time) (string, string) {
	switch user.Presence {
	case telegram.PresenceOnline:
		return "在线", "0"
	case telegram.PresenceRecently:
		return "最近上线", "0"
	case telegram.PresenceLastWeek:
		return "一周内", "7"
	case telegram.PresenceLastMonth:
		return "一个月内", "30"
	case telegram.PresenceOffline:
		if user.LastSeen.IsZero() {
			return "未知", "未知"
		}
		shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
		days := int(now.Sub(user.LastSeen).Hours() / 24)
		if days < 0 {
			days = 0
		}
		return user.LastSeen.In(shanghai).Format("2006-01-02 15:04:05"), fmt.Sprintf("%d", days)
	default:
		return "未知", "未知"
	}
}
