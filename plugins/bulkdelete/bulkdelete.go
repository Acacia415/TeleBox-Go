package bulkdelete

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const maxDeleteCount = 99

type Plugin struct {
	services service.Container
}

func New(services service.Container) *Plugin {
	return &Plugin{services: services}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "bulk_delete",
		Version:     "0.2.0",
		Description: "批量删除范围消息或自己最近的消息",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "bd",
		Description: "批量删除消息",
		Usage: []string{
			"bd（回复消息，删除至当前命令）",
			"bd <1–99>",
			"bd on|off",
		},
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	me, err := p.services.Telegram.ResolveUser(ctx, "me")
	if err != nil {
		return p.respond(ctx, request, "❌ 无法获取当前账号："+err.Error())
	}
	modeKey := "delete_others/" + strconv.FormatInt(me.ID, 10)
	if len(request.Args) > 0 {
		switch strings.ToLower(request.Args[0]) {
		case "on", "off":
			enabled := strings.EqualFold(request.Args[0], "on")
			if err := p.saveMode(ctx, modeKey, enabled); err != nil {
				return p.respond(ctx, request, "❌ 保存设置失败："+err.Error())
			}
			status := "关闭"
			if enabled {
				status = "开启"
			}
			return p.respond(ctx, request, "✅ 已"+status+"删除他人消息权限")
		}
	}

	if request.Message.ReplyToID > 0 {
		return p.deleteRange(ctx, request, me.ID, modeKey)
	}
	if len(request.Args) == 1 {
		count, err := strconv.Atoi(request.Args[0])
		if err == nil {
			if count < 1 || count > maxDeleteCount {
				return p.respond(
					ctx,
					request,
					fmt.Sprintf("❌ 删除数量必须为 1–%d", maxDeleteCount),
				)
			}
			return p.deleteRecentOwn(ctx, request, me.ID, count)
		}
	}
	enabled := p.loadMode(ctx, modeKey)
	return p.respond(ctx, request, helpText(request.Prefix, enabled))
}

func (p *Plugin) deleteRecentOwn(
	ctx context.Context,
	request command.Request,
	selfID int64,
	count int,
) error {
	messageIDs := []int{request.Message.ID}
	offsetID := request.Message.ID
	found := 0
	for found < count {
		batch, err := p.services.Telegram.GetHistory(ctx, telegram.HistoryQuery{
			ChatID:   request.Message.ChatID,
			Limit:    100,
			OffsetID: offsetID,
		})
		if err != nil {
			return p.respond(ctx, request, "❌ 收集消息失败："+err.Error())
		}
		if len(batch) == 0 {
			break
		}
		for _, message := range batch {
			if message.SenderID == selfID && message.ID != request.Message.ID {
				messageIDs = append(messageIDs, message.ID)
				found++
				if found >= count {
					break
				}
			}
		}
		offsetID = batch[len(batch)-1].ID
		if len(batch) < 100 {
			break
		}
	}
	if err := p.services.Telegram.DeleteMessages(
		ctx,
		request.Message.ChatID,
		messageIDs,
	); err != nil {
		return p.respond(ctx, request, "❌ 删除消息失败："+err.Error())
	}
	if found > 0 {
		_, err := p.services.Telegram.SendText(
			ctx,
			request.Message.ChatID,
			fmt.Sprintf("✅ 已删除最近 %d 条消息", found),
		)
		return err
	}
	return nil
}

func (p *Plugin) deleteRange(
	ctx context.Context,
	request command.Request,
	selfID int64,
	modeKey string,
) error {
	startID := request.Message.ReplyToID
	endID := request.Message.ID
	if startID > endID {
		startID, endID = endID, startID
	}
	permissions, err := p.services.Telegram.GetMyPermissions(
		ctx,
		request.Message.ChatID,
	)
	if err != nil {
		p.services.Logger.Warn("read bulk-delete permissions", "error", err)
	}
	deleteOthers := p.loadMode(ctx, modeKey) && permissions.DeleteMessages
	history, err := p.services.Telegram.GetHistory(ctx, telegram.HistoryQuery{
		ChatID: request.Message.ChatID,
		Limit:  100,
		MinID:  startID - 1,
		MaxID:  endID + 1,
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 收集消息列表失败："+err.Error())
	}
	messageIDs := make([]int, 0, len(history)+1)
	seen := make(map[int]struct{}, len(history)+1)
	for _, message := range history {
		if message.ID < startID || message.ID > endID {
			continue
		}
		if deleteOthers || message.SenderID == selfID {
			messageIDs = append(messageIDs, message.ID)
			seen[message.ID] = struct{}{}
		}
	}
	if _, ok := seen[request.Message.ID]; !ok {
		messageIDs = append(messageIDs, request.Message.ID)
	}
	deletedContent := len(messageIDs) - 1
	if deletedContent <= 0 {
		modeHint := ""
		if !p.loadMode(ctx, modeKey) {
			modeHint = "\n当前仅删除自己的消息，使用 " + request.Prefix + "bd on 开启管理员范围删除"
		}
		return p.respond(ctx, request, "🚫 该范围内没有可删除的消息"+modeHint)
	}
	if err := p.services.Telegram.DeleteMessages(
		ctx,
		request.Message.ChatID,
		messageIDs,
	); err != nil {
		return p.respond(ctx, request, "❌ 删除范围消息失败："+err.Error())
	}
	_, err = p.services.Telegram.SendText(
		ctx,
		request.Message.ChatID,
		fmt.Sprintf("✅ 已删除范围内 %d 条消息", deletedContent),
	)
	return err
}

func (p *Plugin) loadMode(ctx context.Context, key string) bool {
	value, err := p.services.Storage.Get(ctx, "bulk_delete", key)
	if err != nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(string(value)), "false")
}

func (p *Plugin) saveMode(ctx context.Context, key string, enabled bool) error {
	return p.services.Storage.Put(
		ctx,
		"bulk_delete",
		key,
		[]byte(strconv.FormatBool(enabled)),
	)
}

func (p *Plugin) respond(ctx context.Context, request command.Request, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("bulk-delete response text is empty")
	}
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

func helpText(prefix string, deleteOthers bool) string {
	mode := "关闭"
	if deleteOthers {
		mode = "开启"
	}
	return "🗑️ 批量删除\n\n" +
		"回复一条消息后使用 " + prefix + "bd，删除该消息到命令之间的消息\n" +
		prefix + "bd <1–99>  删除自己最近的消息\n" +
		prefix + "bd on/off  切换删除他人消息权限\n\n当前删除他人权限：" + mode
}
