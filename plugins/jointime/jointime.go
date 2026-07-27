package jointime

import (
	"context"
	"errors"
	"fmt"
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
		Name:        "jointime",
		Version:     "0.3.0",
		Description: "查询用户加入群组的时间",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "jointime",
		Aliases:     []string{"jt"},
		Description: "查询普通成员记录或扫描入群服务消息",
		Usage: []string{
			"jt <@用户名|用户ID>",
			"jt（回复目标消息）",
			"jt su [@用户名|用户ID]（或回复消息）",
		},
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if len(request.Args) > 0 &&
		(strings.EqualFold(request.Args[0], "help") || strings.EqualFold(request.Args[0], "h")) {
		return p.respond(ctx, request, helpText(request.Prefix))
	}
	exact := len(request.Args) > 0 && strings.EqualFold(request.Args[0], "su")
	args := request.Args
	if exact {
		args = args[1:]
	}
	user, err := p.resolveTarget(ctx, request, args)
	if err != nil {
		p.services.Logger.Warn("jointime resolve target failed", "error", err)
		message := "❌ 未找到该用户"
		if strings.Contains(err.Error(), "回复") {
			message = "❌ 请回复目标消息或指定用户"
		}
		return p.respond(ctx, request, message+"\n\n"+helpText(request.Prefix))
	}
	if exact {
		return p.exact(ctx, request, user)
	}
	return p.normal(ctx, request, user)
}

func (p *Plugin) normal(
	ctx context.Context,
	request command.Request,
	user telegram.User,
) error {
	sent, err := p.begin(ctx, request, "⏳ 查询入群时间…")
	if err != nil {
		return err
	}
	member, err := p.services.Telegram.GetChatMember(
		ctx,
		request.Message.ChatID,
		user.ID,
	)
	if err != nil {
		p.services.Logger.Warn("jointime member lookup failed", "error", err)
		_, editErr := p.services.Telegram.EditText(
			ctx,
			sent.ChatID,
			sent.MessageID,
			"❌ 无法读取成员记录",
		)
		return errors.Join(err, editErr)
	}
	_, err = p.services.Telegram.EditText(
		ctx,
		sent.ChatID,
		sent.MessageID,
		formatResult(user, member.JoinedAt, member.Role),
	)
	return err
}

func (p *Plugin) exact(
	ctx context.Context,
	request command.Request,
	user telegram.User,
) error {
	sent, err := p.begin(
		ctx,
		request,
		"⏳ 搜索入群记录…\n\n• 扫描上限：100,000 条",
	)
	if err != nil {
		return err
	}
	joinedAt, found, err := p.services.Telegram.FindJoinTime(
		ctx,
		request.Message.ChatID,
		user.ID,
		100000,
		func(scanned, found int) error {
			_, editErr := p.services.Telegram.EditText(
				ctx,
				sent.ChatID,
				sent.MessageID,
				fmt.Sprintf(
					"⏳ 搜索入群记录…\n\n• 已扫描：%s 条\n• 已找到：%d 条",
					formatCount(scanned),
					found,
				),
			)
			return editErr
		},
	)
	if err != nil {
		p.services.Logger.Warn("jointime history scan failed", "error", err)
		_, editErr := p.services.Telegram.EditText(
			ctx,
			sent.ChatID,
			sent.MessageID,
			"❌ 无法扫描入群记录",
		)
		return errors.Join(err, editErr)
	}
	text := ""
	if joinedAt.IsZero() {
		text = fmt.Sprintf(
			"⏰ 入群时间\n\n• 用户：%s\n• ID：%d\n\n"+
				"⚪ 未找到入群记录\n"+
				"• 记录可能已删除或超出扫描范围",
			displayUser(user),
			user.ID,
		)
	} else {
		text = formatResult(user, joinedAt, telegram.MemberRoleMember) +
			fmt.Sprintf("\n📚 找到 %d 条入群记录，显示最早一条", found)
	}
	_, err = p.services.Telegram.EditText(ctx, sent.ChatID, sent.MessageID, text)
	return err
}

func (p *Plugin) resolveTarget(
	ctx context.Context,
	request command.Request,
	args []string,
) (telegram.User, error) {
	target := ""
	if len(args) > 0 {
		target = args[0]
	} else if request.Message.ReplyToID > 0 {
		messages, err := p.services.Telegram.GetMessages(
			ctx,
			request.Message.ChatID,
			[]int{request.Message.ReplyToID},
		)
		if err != nil || len(messages) == 0 || messages[0].SenderID <= 0 {
			return telegram.User{}, errors.New("无法获取回复消息的用户")
		}
		target = strconv.FormatInt(messages[0].SenderID, 10)
	}
	if target == "" {
		return telegram.User{}, errors.New("请回复目标用户的消息或指定 @用户名/用户ID")
	}
	user, err := p.services.Telegram.ResolveUser(ctx, target)
	if err != nil {
		return telegram.User{}, fmt.Errorf("找不到指定用户：%w", err)
	}
	return user, nil
}

func (p *Plugin) begin(
	ctx context.Context,
	request command.Request,
	text string,
) (telegram.SentMessage, error) {
	if request.Message.Outgoing {
		return p.services.Telegram.EditText(
			ctx,
			request.Message.ChatID,
			request.Message.ID,
			text,
		)
	}
	return p.services.Telegram.ReplyText(
		ctx,
		request.Message.ChatID,
		request.Message.ID,
		text,
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

func formatResult(user telegram.User, joinedAt time.Time, role telegram.MemberRole) string {
	roleLabel := ""
	timeLabel := "入群时间"
	switch role {
	case telegram.MemberRoleCreator:
		roleLabel = " 👑"
		timeLabel = "群组创建时间"
	case telegram.MemberRoleAdmin:
		roleLabel = " 🛡️"
	}
	return fmt.Sprintf(
		"⏰ 入群时间\n\n• 用户：%s%s\n• ID：%d\n• %s：%s",
		displayUser(user),
		roleLabel,
		user.ID,
		timeLabel,
		joinedAt.Local().Format("2006-01-02 15:04:05"),
	)
}

func displayUser(user telegram.User) string {
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name == "" {
		name = fmt.Sprintf("用户 %d", user.ID)
	}
	if user.Username != "" {
		name += " (@" + user.Username + ")"
	}
	return name
}

func formatCount(value int) string {
	raw := strconv.Itoa(value)
	for index := len(raw) - 3; index > 0; index -= 3 {
		raw = raw[:index] + "," + raw[index:]
	}
	return raw
}

func helpText(prefix string) string {
	return "⏰ 入群时间查询\n\n" +
		prefix + "jt  回复用户后查询\n" +
		prefix + "jt @用户名\n" +
		prefix + "jt 用户ID\n" +
		prefix + "jt su [@用户名]  扫描历史服务消息的精确模式\n\n" +
		"精确模式最多扫描 100,000 条消息。"
}
