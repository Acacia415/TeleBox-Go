package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const maxSendLogSize = 50 << 20

func (p *Plugin) sendLog(ctx context.Context, request command.Request) error {
	if strings.TrimSpace(p.services.LogPath) == "" {
		return p.respond(ctx, request, "❌ 当前构建未配置日志文件")
	}
	if len(request.Args) > 0 {
		switch strings.ToLower(request.Args[0]) {
		case "set":
			if len(request.Args) != 2 {
				return p.respond(
					ctx,
					request,
					"❌ 用法："+request.Prefix+"sendlog set <对话ID|@用户名|me>",
				)
			}
			target := strings.TrimSpace(request.Args[1])
			if _, err := p.resolveLogTarget(ctx, target); err != nil {
				return p.respond(ctx, request, "❌ 无法解析日志目标："+err.Error())
			}
			if err := p.services.Storage.Put(
				ctx,
				"core",
				"log_target",
				[]byte(target),
			); err != nil {
				return p.respond(ctx, request, "❌ 保存日志目标失败："+err.Error())
			}
			return p.respond(ctx, request, "✅ 已设置日志发送目标")
		case "clean":
			if len(request.Args) != 1 {
				return p.respond(ctx, request, "❌ 用法："+request.Prefix+"sendlog clean")
			}
			return p.cleanLogs(ctx, request)
		case "help", "h":
			return p.respond(
				ctx,
				request,
				"日志命令\n\n"+
					request.Prefix+"sendlog\n"+
					request.Prefix+"sendlog set <对话ID|@用户名|me>\n"+
					request.Prefix+"sendlog clean",
			)
		default:
			return p.respond(ctx, request, "❌ 未知日志操作")
		}
	}

	target := "me"
	if stored, err := p.services.Storage.Get(ctx, "core", "log_target"); err == nil {
		if value := strings.TrimSpace(string(stored)); value != "" {
			target = value
		}
	}
	chatID, err := p.resolveLogTarget(ctx, target)
	if err != nil {
		return p.respond(ctx, request, "❌ 无法解析日志目标："+err.Error())
	}
	if err := p.respond(ctx, request, "🔍 正在整理日志文件…"); err != nil {
		return err
	}
	paths := []string{p.services.LogPath, p.services.LogPath + ".1"}
	var sent, skipped int
	for _, path := range paths {
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			continue
		}
		if info.Size() > maxSendLogSize {
			skipped++
			continue
		}
		if _, sendErr := p.services.Telegram.SendFile(ctx, chatID, telegram.Upload{
			Path:     path,
			FileName: filepath.Base(path),
			MIMEType: "text/plain",
			Caption: fmt.Sprintf(
				"TeleBox-Go 日志 · %.2f MiB",
				float64(info.Size())/(1<<20),
			),
			Kind: telegram.MediaDocument,
		}); sendErr != nil {
			return p.respond(ctx, request, "❌ 日志发送失败："+sendErr.Error())
		}
		sent++
	}
	if sent == 0 {
		if skipped > 0 {
			return p.respond(ctx, request, "⚠️ 日志超过 50 MiB，请先清理或在服务器查看")
		}
		return p.respond(ctx, request, "❌ 暂无可发送的日志文件")
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("✅ 已发送 %d 个日志文件", sent),
	)
}

func (p *Plugin) cleanLogs(
	ctx context.Context,
	request command.Request,
) error {
	var cleaned int64
	if info, err := os.Stat(p.services.LogPath); err == nil && info.Mode().IsRegular() {
		cleaned += info.Size()
		if err := os.Truncate(p.services.LogPath, 0); err != nil {
			return p.respond(ctx, request, "❌ 清理当前日志失败："+err.Error())
		}
	}
	rotated := p.services.LogPath + ".1"
	if info, err := os.Stat(rotated); err == nil && info.Mode().IsRegular() {
		cleaned += info.Size()
		if err := os.Remove(rotated); err != nil {
			return p.respond(ctx, request, "❌ 清理轮转日志失败："+err.Error())
		}
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("✅ 日志清理完成，共释放 %.2f MiB", float64(cleaned)/(1<<20)),
	)
}

func (p *Plugin) resolveLogTarget(
	ctx context.Context,
	target string,
) (int64, error) {
	target = strings.TrimSpace(target)
	if strings.EqualFold(target, "me") {
		user, err := p.services.Telegram.ResolveUser(ctx, "me")
		return user.ID, err
	}
	if id, err := strconv.ParseInt(target, 10, 64); err == nil {
		return id, nil
	}
	if user, err := p.services.Telegram.ResolveUser(ctx, target); err == nil {
		return user.ID, nil
	}
	chat, err := p.services.Telegram.ResolveChatTarget(ctx, target)
	if err != nil {
		return 0, err
	}
	return chat.ID, nil
}
