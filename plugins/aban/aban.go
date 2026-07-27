package aban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const managedCacheTTL = 10 * time.Minute

type managedCache struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Chats     []telegram.Chat `json:"chats"`
}

type Plugin struct {
	services service.Container
	now      func() time.Time
}

func New(services service.Container) *Plugin {
	return &Plugin{services: services, now: time.Now}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "aban",
		Version:     "0.2.0",
		Description: "单群及跨管理群封禁管理",
	}
}

func (p *Plugin) Commands() []command.Definition {
	definitions := []command.Definition{{
		Name:        "aban",
		Description: "显示封禁管理帮助",
		Usage:       []string{"help aban"},
		OwnerOnly:   true,
		Handler:     p.help,
	}}
	for _, action := range []telegram.ModerationAction{
		telegram.ModerationKick,
		telegram.ModerationBan,
		telegram.ModerationUnban,
		telegram.ModerationMute,
		telegram.ModerationUnmute,
	} {
		action := action
		definitions = append(definitions, command.Definition{
			Name:        string(action),
			Description: actionDescription(action),
			Usage:       moderationUsage(action),
			OwnerOnly:   true,
			Handler: func(ctx context.Context, request command.Request) error {
				return p.moderate(ctx, request, action)
			},
		})
	}
	definitions = append(definitions,
		command.Definition{
			Name:        "sb",
			Description: "在所有有管理权限的群组中封禁用户",
			Usage:       []string{"sb [@用户|用户ID]（或回复消息）"},
			OwnerOnly:   true,
			Handler: func(ctx context.Context, request command.Request) error {
				return p.superModerate(ctx, request, telegram.ModerationBan)
			},
		},
		command.Definition{
			Name:        "unsb",
			Description: "在所有有管理权限的群组中解封用户",
			Usage:       []string{"unsb [@用户|用户ID]（或回复消息）"},
			OwnerOnly:   true,
			Handler: func(ctx context.Context, request command.Request) error {
				return p.superModerate(ctx, request, telegram.ModerationUnban)
			},
		},
		command.Definition{
			Name:        "refresh",
			Description: "刷新有管理权限的群组缓存",
			Usage:       []string{"refresh"},
			OwnerOnly:   true,
			Handler:     p.refresh,
		},
	)
	return definitions
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) help(ctx context.Context, request command.Request) error {
	return p.respond(ctx, request, helpText(request.Prefix))
}

func moderationUsage(action telegram.ModerationAction) []string {
	if action == telegram.ModerationMute {
		return []string{"mute [@用户|用户ID] [30s|10m|2h|7d]（或回复消息）"}
	}
	return []string{string(action) + " [@用户|用户ID]（或回复消息）"}
}

func (p *Plugin) moderate(
	ctx context.Context,
	request command.Request,
	action telegram.ModerationAction,
) error {
	permissions, err := p.services.Telegram.GetMyPermissions(
		ctx,
		request.Message.ChatID,
	)
	if err != nil || !permissions.BanUsers {
		return p.respond(ctx, request, "❌ 当前会话没有封禁成员权限")
	}
	target, durationArg, err := p.target(ctx, request, action == telegram.ModerationMute)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	until := time.Time{}
	if action == telegram.ModerationMute && durationArg != "" {
		duration, err := parseDuration(durationArg)
		if err != nil {
			return p.respond(ctx, request, "❌ 禁言时长错误："+err.Error())
		}
		until = p.now().Add(duration)
	}
	display := displayUser(target)
	if err := p.respond(
		ctx,
		request,
		fmt.Sprintf("⏳ %s %s…", actionName(action), display),
	); err != nil {
		return err
	}
	if action == telegram.ModerationBan {
		if err := p.services.Telegram.DeleteUserHistory(
			ctx,
			request.Message.ChatID,
			target.ID,
		); err != nil {
			p.services.Logger.Warn("delete banned user history", "user_id", target.ID, "error", err)
		}
	}
	if err := p.services.Telegram.ModerateUser(ctx, telegram.ModerationRequest{
		ChatID: request.Message.ChatID,
		UserID: target.ID,
		Action: action,
		Until:  until,
	}); err != nil {
		return p.respond(ctx, request, "❌ "+actionName(action)+"失败："+err.Error())
	}
	suffix := ""
	if action == telegram.ModerationMute {
		if until.IsZero() {
			suffix = "（永久）"
		} else {
			suffix = "（至 " + until.Local().Format("2006-01-02 15:04:05") + "）"
		}
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("✅ 已%s %s%s", actionName(action), display, suffix),
	)
}

func (p *Plugin) superModerate(
	ctx context.Context,
	request command.Request,
	action telegram.ModerationAction,
) error {
	target, _, err := p.target(ctx, request, false)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	chats, err := p.managedChats(ctx, false)
	if err != nil {
		return p.respond(ctx, request, "❌ 获取管理群组失败："+err.Error())
	}
	if len(chats) == 0 {
		return p.respond(ctx, request, "❌ 没有发现具备封禁权限的群组或频道")
	}
	if err := p.respond(
		ctx,
		request,
		fmt.Sprintf("⏳ 在 %d 个群组中%s %s…", len(chats), actionName(action), displayUser(target)),
	); err != nil {
		return err
	}
	startedAt := p.now()
	success, failed := p.batchModerate(ctx, chats, target.ID, action)
	return p.respond(
		ctx,
		request,
		fmt.Sprintf(
			"✅ 跨群%s完成\n\n• 用户：%s\n• 成功：%d\n• 失败：%d\n• 耗时：%.1fs",
			actionName(action),
			displayUser(target),
			success,
			failed,
			p.now().Sub(startedAt).Seconds(),
		),
	)
}

func (p *Plugin) batchModerate(
	ctx context.Context,
	chats []telegram.Chat,
	userID int64,
	action telegram.ModerationAction,
) (int, int) {
	const workers = 8
	jobs := make(chan telegram.Chat)
	var (
		success atomic.Int64
		failed  atomic.Int64
		group   sync.WaitGroup
	)
	workerCount := workers
	if len(chats) < workerCount {
		workerCount = len(chats)
	}
	group.Add(workerCount)
	for range workerCount {
		go func() {
			defer group.Done()
			for chat := range jobs {
				err := p.services.Telegram.ModerateUser(ctx, telegram.ModerationRequest{
					ChatID: chat.ID,
					UserID: userID,
					Action: action,
				})
				if err != nil {
					failed.Add(1)
					p.services.Logger.Debug(
						"cross-chat moderation failed",
						"chat_id", chat.ID,
						"chat", chat.Title,
						"error", err,
					)
				} else {
					success.Add(1)
				}
			}
		}()
	}
	for _, chat := range chats {
		select {
		case jobs <- chat:
		case <-ctx.Done():
			failed.Add(int64(len(chats)) - success.Load() - failed.Load())
			close(jobs)
			group.Wait()
			return int(success.Load()), int(failed.Load())
		}
	}
	close(jobs)
	group.Wait()
	return int(success.Load()), int(failed.Load())
}

func (p *Plugin) refresh(ctx context.Context, request command.Request) error {
	if err := p.services.Storage.Delete(ctx, "aban", "managed_chats"); err != nil {
		p.services.Logger.Debug("clear managed-chat cache", "error", err)
	}
	chats, err := p.managedChats(ctx, true)
	if err != nil {
		return p.respond(ctx, request, "❌ 刷新失败："+err.Error())
	}
	return p.respond(ctx, request, fmt.Sprintf("✅ 已刷新 %d 个管理群组", len(chats)))
}

func (p *Plugin) managedChats(
	ctx context.Context,
	force bool,
) ([]telegram.Chat, error) {
	if !force {
		if raw, err := p.services.Storage.Get(ctx, "aban", "managed_chats"); err == nil {
			var cached managedCache
			if json.Unmarshal(raw, &cached) == nil &&
				p.now().Before(cached.FetchedAt.Add(managedCacheTTL)) {
				return cached.Chats, nil
			}
		}
	}
	chats, err := p.services.Telegram.ListManagedChats(ctx, 500)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(managedCache{
		FetchedAt: p.now().UTC(),
		Chats:     chats,
	})
	if err == nil {
		if err := p.services.Storage.Put(ctx, "aban", "managed_chats", encoded); err != nil {
			p.services.Logger.Warn("cache managed chats", "error", err)
		}
	}
	return chats, nil
}

func (p *Plugin) target(
	ctx context.Context,
	request command.Request,
	allowDuration bool,
) (telegram.User, string, error) {
	var (
		target      string
		durationArg string
	)
	if request.Message.ReplyToID > 0 {
		if len(request.Args) > 0 {
			if allowDuration && looksLikeDuration(request.Args[0]) {
				durationArg = request.Args[0]
			} else {
				target = request.Args[0]
				if allowDuration && len(request.Args) > 1 {
					durationArg = request.Args[1]
				}
			}
		}
		if target == "" {
			messages, err := p.services.Telegram.GetMessages(
				ctx,
				request.Message.ChatID,
				[]int{request.Message.ReplyToID},
			)
			if err != nil || len(messages) == 0 || messages[0].SenderID <= 0 {
				return telegram.User{}, "", errors.New("无法获取回复消息的用户")
			}
			target = strconv.FormatInt(messages[0].SenderID, 10)
		}
	} else {
		if len(request.Args) == 0 {
			return telegram.User{}, "", errors.New("请回复用户消息或指定 @用户名/用户ID")
		}
		target = request.Args[0]
		if allowDuration && len(request.Args) > 1 {
			durationArg = request.Args[1]
		}
	}
	user, err := p.services.Telegram.ResolveUser(ctx, target)
	if err != nil {
		return telegram.User{}, "", fmt.Errorf("解析用户失败：%w", err)
	}
	return user, durationArg, nil
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

func parseDuration(value string) (time.Duration, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return 0, nil
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days <= 0 {
			return 0, errors.New("请使用 30s、10m、2h 或 7d")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, errors.New("请使用 30s、10m、2h 或 7d")
	}
	return duration, nil
}

func looksLikeDuration(value string) bool {
	_, err := parseDuration(value)
	return err == nil
}

func displayUser(user telegram.User) string {
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name == "" {
		name = strconv.FormatInt(user.ID, 10)
	}
	if user.Username != "" {
		name += " (@" + user.Username + ")"
	}
	return name
}

func actionName(action telegram.ModerationAction) string {
	names := map[telegram.ModerationAction]string{
		telegram.ModerationKick:   "踢出",
		telegram.ModerationBan:    "封禁",
		telegram.ModerationUnban:  "解封",
		telegram.ModerationMute:   "禁言",
		telegram.ModerationUnmute: "解除禁言",
	}
	return names[action]
}

func actionDescription(action telegram.ModerationAction) string {
	return actionName(action) + "回复或指定的用户"
}

func helpText(prefix string) string {
	return "🛡️ 封禁管理\n\n" +
		prefix + "kick [@用户]  踢出\n" +
		prefix + "ban [@用户]  封禁并清理消息\n" +
		prefix + "unban [@用户]  解封\n" +
		prefix + "mute [@用户] [30s|10m|2h|7d]  禁言\n" +
		prefix + "unmute [@用户]  解除禁言\n" +
		prefix + "sb [@用户]  跨管理群封禁\n" +
		prefix + "unsb [@用户]  跨管理群解封\n" +
		prefix + "refresh  刷新管理群缓存\n\n以上用户参数均可改为回复消息"
}
