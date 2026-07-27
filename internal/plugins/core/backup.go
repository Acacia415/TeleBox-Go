package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/corebackup"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const backupTargetsKey = "backup_targets"

func (p *Plugin) backup(ctx context.Context, request command.Request) error {
	if len(request.Args) > 0 {
		switch strings.ToLower(request.Args[0]) {
		case "help", "h":
			return p.backupHelp(ctx, request)
		case "set":
			return p.setBackupTargets(ctx, request)
		case "del", "delete", "remove":
			return p.deleteBackupTarget(ctx, request)
		}
	}
	if !p.backupMu.TryLock() {
		return p.respond(ctx, request, "⏳ 已有备份或恢复任务正在进行")
	}
	defer p.backupMu.Unlock()

	full := len(request.Args) == 1 &&
		strings.EqualFold(request.Args[0], "all")
	oneTime := len(request.Args) > 0 &&
		strings.EqualFold(request.Args[0], "to")
	if len(request.Args) > 0 && !full && !oneTime {
		return p.backupHelp(ctx, request)
	}
	var targets []int64
	var err error
	if oneTime {
		if len(request.Args) < 2 {
			return p.backupHelp(ctx, request)
		}
		targets, err = p.resolveBackupTargets(ctx, request.Args[1:])
	} else {
		targets, err = p.loadBackupTargets(ctx)
	}
	if err != nil {
		return p.respond(ctx, request, "❌ 备份目标无效："+err.Error())
	}
	if len(targets) == 0 {
		targets = []int64{request.Message.ChatID}
	}
	if err := p.respond(ctx, request, "📦 正在创建 TeleBox-Go 备份…"); err != nil {
		return err
	}
	temp, err := os.CreateTemp("", "telebox_backup_*.tar.gz")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建备份临时文件失败："+err.Error())
	}
	archivePath := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(archivePath)
		return p.respond(ctx, request, "❌ 创建备份临时文件失败："+err.Error())
	}
	defer os.Remove(archivePath)
	manifest, err := corebackup.Create(
		ctx,
		p.services.Storage,
		p.backupPaths(),
		full,
		archivePath,
	)
	if err != nil {
		p.services.Logger.Error("create TeleBox backup failed", "error", err)
		return p.respond(ctx, request, "❌ 备份失败："+err.Error())
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取备份文件失败："+err.Error())
	}
	kind := "标准备份"
	content := "插件、插件数据、配置状态"
	if full {
		kind = "全量备份"
		content = "插件、插件数据、配置文件（不含登录会话和日志）"
	}
	caption := fmt.Sprintf(
		"📦 TeleBox-Go %s\n\n🕐 %s\n💾 %.2f MiB\n📋 %s",
		kind,
		manifest.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		float64(info.Size())/(1<<20),
		content,
	)
	var failures []string
	for _, chatID := range targets {
		if _, sendErr := p.services.Telegram.SendFile(ctx, chatID, telegram.Upload{
			Path:     archivePath,
			FileName: backupFileName(manifest.CreatedAt),
			MIMEType: "application/gzip",
			Caption:  caption,
			Kind:     telegram.MediaDocument,
		}); sendErr != nil {
			failures = append(failures, strconv.FormatInt(chatID, 10))
			p.services.Logger.Warn(
				"send TeleBox backup failed",
				"chat_id", chatID,
				"error", sendErr,
			)
		}
	}
	if len(failures) == len(targets) {
		return p.respond(ctx, request, "❌ 备份已创建，但发送失败")
	}
	message := fmt.Sprintf(
		"✅ %s完成\n\n• 目标：%d 个\n• 大小：%.2f MiB\n• 文件：%d 个",
		kind,
		len(targets)-len(failures),
		float64(info.Size())/(1<<20),
		len(manifest.Files),
	)
	if len(failures) > 0 {
		message += "\n• 发送失败：" + strings.Join(failures, "、")
	}
	return p.respond(ctx, request, message)
}

func (p *Plugin) restore(ctx context.Context, request command.Request) error {
	if len(request.Args) > 0 {
		if len(request.Args) == 1 &&
			(strings.EqualFold(request.Args[0], "help") ||
				strings.EqualFold(request.Args[0], "h")) {
			return p.restoreHelp(ctx, request)
		}
		return p.restoreHelp(ctx, request)
	}
	if request.Message.ReplyToID <= 0 {
		return p.respondHTML(
			ctx,
			request,
			"❌ 请回复由 <code>"+html.EscapeString(request.Prefix)+
				"bf</code> 创建的 <code>.tar.gz</code> 文件",
		)
	}
	if !p.backupMu.TryLock() {
		return p.respond(ctx, request, "⏳ 已有备份或恢复任务正在进行")
	}
	defer p.backupMu.Unlock()

	messages, err := p.services.Telegram.GetMessages(
		ctx,
		request.Message.ChatID,
		[]int{request.Message.ReplyToID},
	)
	if err != nil || len(messages) == 0 || messages[0].Media == nil {
		return p.respond(ctx, request, "❌ 无法读取回复的备份文件")
	}
	media := messages[0].Media
	if !strings.HasSuffix(strings.ToLower(media.FileName), ".tar.gz") {
		return p.respond(ctx, request, "❌ 回复的文件不是 .tar.gz 备份")
	}
	if media.Size > 512<<20 {
		return p.respond(ctx, request, "❌ 备份文件超过 512 MiB 安全限制")
	}
	if err := p.respond(ctx, request, "📥 正在下载并校验备份…"); err != nil {
		return err
	}
	temp, err := os.CreateTemp("", "telebox_restore_*.tar.gz")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建恢复临时文件失败："+err.Error())
	}
	archivePath := temp.Name()
	defer os.Remove(archivePath)
	_, downloadErr := p.services.Telegram.DownloadMedia(
		ctx,
		request.Message.ChatID,
		request.Message.ReplyToID,
		temp,
	)
	closeErr := temp.Close()
	if downloadErr != nil || closeErr != nil {
		return p.respond(
			ctx,
			request,
			"❌ 下载备份失败："+errors.Join(downloadErr, closeErr).Error(),
		)
	}
	manifest, pending, err := corebackup.Stage(archivePath, p.backupPaths())
	if err != nil {
		p.services.Logger.Warn("reject TeleBox backup restore", "error", err)
		return p.respond(ctx, request, "❌ 备份校验失败："+err.Error())
	}
	mode := "标准备份"
	if manifest.Full {
		mode = "全量备份"
	}
	if p.services.Restart == nil {
		return p.respond(
			ctx,
			request,
			"✅ "+mode+"已校验并等待恢复\n请手动重启 TeleBox-Go\n"+pending,
		)
	}
	if err := p.respond(
		ctx,
		request,
		"✅ "+mode+"已校验，正在重启并恢复\n现有文件会保留在回滚目录中",
	); err != nil {
		return err
	}
	p.services.Restart()
	return nil
}

func (p *Plugin) setBackupTargets(
	ctx context.Context,
	request command.Request,
) error {
	if len(request.Args) < 2 {
		return p.backupHelp(ctx, request)
	}
	additions, err := p.resolveBackupTargets(ctx, request.Args[1:])
	if err != nil {
		return p.respond(ctx, request, "❌ 备份目标无效："+err.Error())
	}
	current, err := p.loadBackupTargets(ctx)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取备份目标失败："+err.Error())
	}
	seen := make(map[int64]struct{}, len(current)+len(additions))
	for _, id := range current {
		seen[id] = struct{}{}
	}
	for _, id := range additions {
		if _, exists := seen[id]; !exists {
			current = append(current, id)
			seen[id] = struct{}{}
		}
	}
	sort.Slice(current, func(i, j int) bool { return current[i] < current[j] })
	if err := p.saveBackupTargets(ctx, current); err != nil {
		return p.respond(ctx, request, "❌ 保存备份目标失败："+err.Error())
	}
	return p.respond(
		ctx,
		request,
		"✅ 备份目标已更新："+formatChatIDs(current),
	)
}

func (p *Plugin) deleteBackupTarget(
	ctx context.Context,
	request command.Request,
) error {
	if len(request.Args) != 2 {
		return p.backupHelp(ctx, request)
	}
	if strings.EqualFold(request.Args[1], "all") {
		if err := p.saveBackupTargets(ctx, nil); err != nil {
			return p.respond(ctx, request, "❌ 清空备份目标失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已清空全部备份目标")
	}
	targets, err := p.resolveBackupTargets(ctx, request.Args[1:])
	if err != nil || len(targets) != 1 {
		return p.respond(ctx, request, "❌ 备份目标无效")
	}
	current, err := p.loadBackupTargets(ctx)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取备份目标失败："+err.Error())
	}
	filtered := current[:0]
	for _, id := range current {
		if id != targets[0] {
			filtered = append(filtered, id)
		}
	}
	if err := p.saveBackupTargets(ctx, filtered); err != nil {
		return p.respond(ctx, request, "❌ 保存备份目标失败："+err.Error())
	}
	return p.respond(
		ctx,
		request,
		"✅ 已删除备份目标；当前："+formatChatIDs(filtered),
	)
}

func (p *Plugin) resolveBackupTargets(
	ctx context.Context,
	values []string,
) ([]int64, error) {
	fields := strings.Fields(strings.ReplaceAll(strings.Join(values, " "), ",", " "))
	if len(fields) == 0 {
		return nil, errors.New("没有提供目标")
	}
	result := make([]int64, 0, len(fields))
	seen := make(map[int64]struct{}, len(fields))
	for _, value := range fields {
		if len(value) > 3 && strings.HasPrefix(value, "100") {
			if _, err := strconv.ParseInt(value, 10, 64); err == nil {
				value = "-" + value
			}
		}
		id, err := p.resolveLogTarget(ctx, value)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("%s", value)
		}
		if _, exists := seen[id]; !exists {
			result = append(result, id)
			seen[id] = struct{}{}
		}
	}
	return result, nil
}

func (p *Plugin) loadBackupTargets(ctx context.Context) ([]int64, error) {
	data, err := p.services.Storage.Get(ctx, "core", backupTargetsKey)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var targets []int64
	if err := json.Unmarshal(data, &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (p *Plugin) saveBackupTargets(ctx context.Context, targets []int64) error {
	data, err := json.Marshal(targets)
	if err != nil {
		return err
	}
	return p.services.Storage.Put(ctx, "core", backupTargetsKey, data)
}

func (p *Plugin) backupPaths() corebackup.Paths {
	return corebackup.Paths{
		Config:  p.services.ConfigPath,
		Storage: p.services.StoragePath,
		Assets:  p.services.AssetsDir,
		Plugins: p.services.PluginsDir,
	}
}

func (p *Plugin) backupHelp(
	ctx context.Context,
	request command.Request,
) error {
	prefix := html.EscapeString(request.Prefix)
	return p.respondHTML(ctx, request, strings.Join([]string{
		"<b>📦 TeleBox-Go 备份</b>",
		"",
		"• <code>" + prefix + "bf</code>  备份插件、插件数据和配置状态",
		"• <code>" + prefix + "bf all</code>  额外包含主配置文件",
		"• <code>" + prefix + "bf set 对话ID...</code>  保存发送目标",
		"• <code>" + prefix + "bf to 对话ID...</code>  仅本次发送到指定目标",
		"• <code>" + prefix + "bf del 对话ID|all</code>  删除目标",
		"• 回复备份文件发送 <code>" + prefix + "hf</code> 恢复",
		"",
		"登录会话、日志和程序二进制不会写入备份。",
	}, "\n"))
}

func (p *Plugin) restoreHelp(
	ctx context.Context,
	request command.Request,
) error {
	return p.respondHTML(ctx, request,
		"<b>♻️ TeleBox-Go 恢复</b>\n\n回复 <code>"+
			html.EscapeString(request.Prefix)+"bf</code> 生成的备份文件，发送 <code>"+
			html.EscapeString(request.Prefix)+"hf</code>。\n"+
			"文件会先校验，重启后恢复；原文件保留在回滚目录。",
	)
}

func formatChatIDs(ids []int64) string {
	if len(ids) == 0 {
		return "无"
	}
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, strconv.FormatInt(id, 10))
	}
	return strings.Join(values, "、")
}

func backupFileName(created time.Time) string {
	return "telebox_backup_" + created.Local().Format("20060102_150405") + ".tar.gz"
}
