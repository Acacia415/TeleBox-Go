package telegrambackup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const (
	messageLimit = 20000
	batchSize    = 100
)

type Plugin struct {
	services  service.Container
	assetDir  string
	workDir   string
	database  *backupDB
	mu        sync.Mutex
	nameCache map[int64]string
}

func New(services service.Container) *Plugin {
	assetDir := filepath.Join(services.AssetsDir, "telegram-backup")
	if services.AssetsDir == "" {
		assetDir = filepath.Join(os.TempDir(), "telebox-go-telegram-backup-assets")
	}
	return &Plugin{
		services:  services,
		assetDir:  assetDir,
		workDir:   filepath.Join(os.TempDir(), "telebox-go-telegram-backup"),
		nameCache: make(map[int64]string),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "telegram-backup",
		Version:     "0.1.0",
		Description: "备份私聊消息元数据以及群组、频道链接并导出安全 ZIP",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "tb",
		Description: "Telegram 对话与链接备份",
		OwnerOnly:   true,
		Handler:     p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error {
	if err := os.MkdirAll(p.assetDir, 0o700); err != nil {
		return fmt.Errorf("create telegram backup asset directory: %w", err)
	}
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return fmt.Errorf("create telegram backup work directory: %w", err)
	}
	database, err := openBackupDB(
		filepath.Join(p.assetDir, "telegram_backup.db"),
	)
	if err != nil {
		return err
	}
	p.database = database
	return nil
}

func (p *Plugin) Stop(context.Context) error {
	return p.database.close()
}

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.database == nil {
		return errors.New("telegram backup database is not open")
	}
	if len(request.Args) == 0 {
		return p.respond(ctx, request, helpText(request.Prefix))
	}
	switch strings.ToLower(request.Args[0]) {
	case "saved":
		return p.backupSaved(ctx, request)
	case "chat":
		return p.backupTarget(ctx, request)
	case "private":
		return p.backupPrivate(ctx, request)
	case "all":
		return p.backupAll(ctx, request)
	case "groups":
		return p.backupLinks(ctx, request, "group")
	case "channels":
		return p.backupLinks(ctx, request, "channel")
	case "links":
		return p.backupLinks(ctx, request, "")
	case "showgroups":
		return p.showGroups(ctx, request)
	case "join":
		return p.joinChannels(ctx, request)
	case "list":
		return p.list(ctx, request)
	case "export":
		return p.export(ctx, request)
	case "exportall":
		return p.exportAllCommand(ctx, request)
	case "restore", "restoreall":
		return p.restore(ctx, request)
	case "delete", "del":
		return p.delete(ctx, request)
	case "clear":
		return p.clear(ctx, request)
	default:
		return p.respond(ctx, request, "❌ 未知命令\n\n"+helpText(request.Prefix))
	}
}

func (p *Plugin) backupSaved(
	ctx context.Context,
	request command.Request,
) error {
	me, err := p.services.Telegram.ResolveUser(ctx, "me")
	if err != nil {
		return p.respond(ctx, request, "❌ 无法解析收藏夹："+err.Error())
	}
	chat := telegram.Chat{
		ID:    me.ID,
		Title: "Saved Messages",
		Kind:  telegram.ChatPrivate,
	}
	return p.backupOne(ctx, request, chat, "saved")
}

func (p *Plugin) backupTarget(
	ctx context.Context,
	request command.Request,
) error {
	var (
		chat telegram.Chat
		err  error
	)
	if len(request.Args) >= 2 {
		chat, err = p.services.Telegram.ResolveChatTarget(ctx, request.Args[1])
	} else {
		chat, err = p.services.Telegram.ResolveChat(ctx, request.Message.ChatID)
	}
	if err != nil {
		return p.respond(ctx, request, "❌ 无法解析对话："+err.Error())
	}
	return p.backupOne(ctx, request, chat, string(chat.Kind))
}

func (p *Plugin) backupOne(
	ctx context.Context,
	request command.Request,
	chat telegram.Chat,
	chatType string,
) error {
	if err := p.respond(ctx, request, "⏳ 备份 "+chat.Title+"…"); err != nil {
		return err
	}
	count, err := p.backupChat(ctx, chat, chatType, func(current int) error {
		if current%500 != 0 {
			return nil
		}
		return p.respond(ctx, request, fmt.Sprintf(
			"⏳ 备份 %s…（已处理 %d 条）",
			chat.Title,
			current,
		))
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 备份失败："+err.Error())
	}
	suffix := ""
	if count >= messageLimit {
		suffix = "（已达到 20000 条上限）"
	}
	return p.respond(ctx, request, fmt.Sprintf(
		"✅ 已备份 %s：%d 条消息%s",
		chat.Title,
		count,
		suffix,
	))
}

func (p *Plugin) backupPrivate(
	ctx context.Context,
	request command.Request,
) error {
	if err := p.respond(ctx, request, "⏳ 读取私聊列表…"); err != nil {
		return err
	}
	chats, err := p.services.Telegram.ListChats(ctx, 500)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取对话列表失败："+err.Error())
	}
	me, _ := p.services.Telegram.ResolveUser(ctx, "me")
	var private []telegram.Chat
	for _, chat := range chats {
		if chat.Kind == telegram.ChatPrivate && chat.ID != me.ID {
			private = append(private, chat)
		}
	}
	totalMessages := 0
	for index, chat := range private {
		if err := p.respond(ctx, request, fmt.Sprintf(
			"⏳ 备份私聊（%d/%d）：%s",
			index+1,
			len(private),
			chat.Title,
		)); err != nil {
			return err
		}
		count, err := p.backupChat(ctx, chat, "private", nil)
		if err != nil {
			p.services.Logger.Warn(
				"backup private chat",
				"chat_id", chat.ID,
				"error", err,
			)
			continue
		}
		totalMessages += count
	}
	return p.respond(ctx, request, fmt.Sprintf(
		"✅ 已备份 %d 个私聊，共 %d 条消息",
		len(private),
		totalMessages,
	))
}

func (p *Plugin) backupAll(
	ctx context.Context,
	request command.Request,
) error {
	if err := p.respond(ctx, request, "⏳ 读取全部对话…"); err != nil {
		return err
	}
	chats, err := p.services.Telegram.ListChats(ctx, 500)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取对话列表失败："+err.Error())
	}
	totalMessages := 0
	success := 0
	for index, chat := range chats {
		if err := p.respond(ctx, request, fmt.Sprintf(
			"⏳ 备份全部对话（%d/%d）：%s",
			index+1,
			len(chats),
			chat.Title,
		)); err != nil {
			return err
		}
		count, backupErr := p.backupChat(ctx, chat, string(chat.Kind), nil)
		if backupErr != nil {
			p.services.Logger.Warn(
				"backup dialog",
				"chat_id", chat.ID,
				"error", backupErr,
			)
			continue
		}
		totalMessages += count
		success++
	}
	return p.respond(ctx, request, fmt.Sprintf(
		"✅ 已备份 %d/%d 个对话，共 %d 条消息",
		success,
		len(chats),
		totalMessages,
	))
}

func (p *Plugin) backupChat(
	ctx context.Context,
	chat telegram.Chat,
	chatType string,
	progress func(int) error,
) (int, error) {
	backupID, err := p.database.create(
		ctx,
		strconv.FormatInt(chat.ID, 10),
		firstNonEmpty(chat.Title, chat.Username, strconv.FormatInt(chat.ID, 10)),
		chatType,
	)
	if err != nil {
		return 0, err
	}
	count := 0
	offsetID := 0
	for count < messageLimit {
		limit := batchSize
		if remaining := messageLimit - count; remaining < limit {
			limit = remaining
		}
		messages, err := p.services.Telegram.GetHistory(
			ctx,
			telegram.HistoryQuery{
				ChatID:   chat.ID,
				Limit:    limit,
				OffsetID: offsetID,
			},
		)
		if err != nil {
			return count, err
		}
		if len(messages) == 0 {
			break
		}
		records := make([]messageRecord, 0, len(messages))
		for _, message := range messages {
			record := p.messageRecord(ctx, message)
			records = append(records, record)
		}
		if err := p.database.addMessages(ctx, backupID, records); err != nil {
			return count, err
		}
		count += len(records)
		offsetID = messages[len(messages)-1].ID
		if progress != nil {
			if err := progress(count); err != nil {
				return count, err
			}
		}
		if len(messages) < limit {
			break
		}
	}
	if err := p.database.updateCount(ctx, backupID); err != nil {
		return count, err
	}
	return count, nil
}

func (p *Plugin) messageRecord(
	ctx context.Context,
	message telegram.Message,
) messageRecord {
	record := messageRecord{
		MessageID:  message.ID,
		SenderID:   strconv.FormatInt(message.SenderID, 10),
		SenderName: p.senderName(ctx, message.SenderID),
		Date:       message.Date.UTC().Format(time.RFC3339),
		Text:       message.Text,
		ReplyToID:  message.ReplyToID,
	}
	if message.SenderID == 0 {
		record.SenderID = ""
	}
	if message.Media != nil {
		record.MediaType = string(message.Media.Kind)
		record.MediaID = message.Media.FileName
	}
	record.RawData = rawMessageJSON(record)
	return record
}

func (p *Plugin) senderName(ctx context.Context, senderID int64) string {
	if senderID == 0 {
		return "Unknown"
	}
	if value := p.nameCache[senderID]; value != "" {
		return value
	}
	user, err := p.services.Telegram.ResolveUser(
		ctx,
		strconv.FormatInt(senderID, 10),
	)
	if err != nil {
		value := strconv.FormatInt(senderID, 10)
		p.nameCache[senderID] = value
		return value
	}
	value := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if value == "" {
		value = firstNonEmpty(user.Username, strconv.FormatInt(senderID, 10))
	}
	p.nameCache[senderID] = value
	return value
}

func (p *Plugin) backupLinks(
	ctx context.Context,
	request command.Request,
	linkType string,
) error {
	if err := p.respond(ctx, request, "⏳ 读取群组和频道…"); err != nil {
		return err
	}
	chats, err := p.services.Telegram.ListChats(ctx, 500)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取对话列表失败："+err.Error())
	}
	savedGroups := 0
	savedChannels := 0
	for _, chat := range chats {
		kind := ""
		switch chat.Kind {
		case telegram.ChatGroup, telegram.ChatSupergroup:
			kind = "group"
		case telegram.ChatChannel:
			kind = "channel"
		default:
			continue
		}
		if linkType != "" && linkType != kind {
			continue
		}
		link := ""
		if chat.Username != "" {
			link = "https://t.me/" + strings.TrimPrefix(chat.Username, "@")
		}
		if err := p.database.saveLink(ctx, chatLink{
			ChatID:      strconv.FormatInt(chat.ID, 10),
			ChatTitle:   chat.Title,
			ChatType:    kind,
			Username:    chat.Username,
			InviteLink:  link,
			MemberCount: chat.MemberCount,
			Verified:    chat.Verified,
			Scam:        chat.Scam,
			Fake:        chat.Fake,
		}); err != nil {
			return p.respond(ctx, request, "❌ 保存链接失败："+err.Error())
		}
		if kind == "group" {
			savedGroups++
		} else {
			savedChannels++
		}
	}
	return p.respond(ctx, request, fmt.Sprintf(
		"✅ 链接备份完成\n群组：%d\n频道：%d\n"+
			"仅公开用户名可生成可加入链接；私有邀请链接不会被主动导出。",
		savedGroups,
		savedChannels,
	))
}

func (p *Plugin) showGroups(
	ctx context.Context,
	request command.Request,
) error {
	links, err := p.database.links(ctx, "group")
	if err != nil {
		return p.respond(ctx, request, "❌ 读取群组链接失败："+err.Error())
	}
	if len(links) == 0 {
		return p.respond(ctx, request, "📭 暂无群组链接")
	}
	var text strings.Builder
	text.WriteString("👥 群组链接\n\n")
	for index, link := range links {
		fmt.Fprintf(&text, "%d. %s\n", index+1, link.ChatTitle)
		if link.InviteLink != "" {
			text.WriteString(link.InviteLink + "\n")
		} else {
			text.WriteString("无公开链接\n")
		}
	}
	return p.finishText(ctx, request, text.String())
}

func (p *Plugin) joinChannels(
	ctx context.Context,
	request command.Request,
) error {
	links, err := p.database.links(ctx, "channel")
	if err != nil {
		return p.respond(ctx, request, "❌ 读取频道链接失败："+err.Error())
	}
	if len(links) == 0 {
		return p.respond(ctx, request, "📭 暂无频道链接")
	}
	success := 0
	failed := 0
	for index, link := range links {
		target := firstNonEmpty(link.InviteLink, link.Username)
		if target == "" {
			failed++
			continue
		}
		if err := p.respond(ctx, request, fmt.Sprintf(
			"⏳ 加入频道（%d/%d）：%s",
			index+1,
			len(links),
			link.ChatTitle,
		)); err != nil {
			return err
		}
		if err := p.services.Telegram.JoinChat(ctx, target); err != nil {
			failed++
			continue
		}
		success++
	}
	return p.respond(ctx, request, fmt.Sprintf(
		"✅ 加入频道完成\n成功：%d\n失败/无链接：%d",
		success,
		failed,
	))
}

func (p *Plugin) list(ctx context.Context, request command.Request) error {
	backups, err := p.database.list(ctx)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取备份列表失败："+err.Error())
	}
	if len(backups) == 0 {
		return p.respond(ctx, request, "📭 暂无备份记录")
	}
	var text strings.Builder
	text.WriteString("📦 备份列表\n\n")
	for index, backup := range backups {
		if index >= 50 {
			fmt.Fprintf(&text, "…另有 %d 个备份\n", len(backups)-index)
			break
		}
		fmt.Fprintf(
			&text,
			"#%d %s\n类型：%s｜消息：%d｜更新：%s\n\n",
			backup.ID,
			backup.ChatTitle,
			backup.ChatType,
			backup.MessageCount,
			backup.UpdatedAt,
		)
	}
	return p.finishText(ctx, request, text.String())
}

func (p *Plugin) export(ctx context.Context, request command.Request) error {
	if len(request.Args) != 2 {
		return p.respond(ctx, request, "❌ 用法："+request.Prefix+"tb export <ID>")
	}
	id, err := strconv.ParseInt(request.Args[1], 10, 64)
	if err != nil || id <= 0 {
		return p.respond(ctx, request, "❌ 备份 ID 无效")
	}
	if err := p.respond(ctx, request, "⏳ 导出备份…"); err != nil {
		return err
	}
	path, backup, err := p.exportOne(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p.respond(ctx, request, "❌ 备份不存在")
		}
		return p.respond(ctx, request, "❌ 导出失败："+err.Error())
	}
	defer os.RemoveAll(filepath.Dir(path))
	_, err = p.services.Telegram.SendFile(
		ctx,
		request.Message.ChatID,
		telegram.Upload{
			Path:     path,
			FileName: filepath.Base(path),
			MIMEType: "application/zip",
			Caption: fmt.Sprintf(
				"📦 Telegram 备份\n%s\n消息：%d",
				backup.ChatTitle,
				backup.MessageCount,
			),
			ReplyToID: request.Message.ReplyToID,
			Kind:      telegram.MediaDocument,
		},
	)
	if err != nil {
		return p.respond(ctx, request, "❌ 发送导出文件失败："+err.Error())
	}
	return p.deleteCommand(ctx, request)
}

func (p *Plugin) exportAllCommand(
	ctx context.Context,
	request command.Request,
) error {
	if err := p.respond(ctx, request, "⏳ 导出全部备份…"); err != nil {
		return err
	}
	path, count, messages, err := p.exportAll(ctx)
	if err != nil {
		return p.respond(ctx, request, "❌ 导出失败："+err.Error())
	}
	defer os.RemoveAll(filepath.Dir(path))
	_, err = p.services.Telegram.SendFile(
		ctx,
		request.Message.ChatID,
		telegram.Upload{
			Path:     path,
			FileName: filepath.Base(path),
			MIMEType: "application/zip",
			Caption: fmt.Sprintf(
				"📦 Telegram 全部备份\n备份：%d\n消息：%d\n请妥善保管。",
				count,
				messages,
			),
			Kind: telegram.MediaDocument,
		},
	)
	if err != nil {
		return p.respond(ctx, request, "❌ 发送导出文件失败："+err.Error())
	}
	return p.deleteCommand(ctx, request)
}

func (p *Plugin) restore(ctx context.Context, request command.Request) error {
	if request.Message.ReplyToID <= 0 {
		return p.respond(ctx, request, "❌ 请回复 JSON 或 ZIP 备份文件")
	}
	if err := p.respond(ctx, request, "⏳ 验证并恢复备份…"); err != nil {
		return err
	}
	jobDir, err := os.MkdirTemp(p.workDir, "restore-*")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建恢复目录失败："+err.Error())
	}
	defer os.RemoveAll(jobDir)
	filePath := filepath.Join(jobDir, "restore.data")
	output, err := os.OpenFile(
		filePath,
		os.O_CREATE|os.O_WRONLY|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return p.respond(ctx, request, "❌ 创建恢复文件失败："+err.Error())
	}
	_, downloadErr := p.services.Telegram.DownloadMedia(
		ctx,
		request.Message.ChatID,
		request.Message.ReplyToID,
		&boundedWriter{writer: output, remaining: maxRestoreBytes},
	)
	closeErr := output.Close()
	if downloadErr != nil {
		return p.respond(ctx, request, "❌ 下载备份失败："+downloadErr.Error())
	}
	if closeErr != nil {
		return p.respond(ctx, request, "❌ 保存备份失败："+closeErr.Error())
	}
	success, failed, err := p.restoreArchive(ctx, filePath)
	if err != nil && success == 0 {
		return p.respond(ctx, request, "❌ 恢复失败："+err.Error())
	}
	return p.respond(ctx, request, fmt.Sprintf(
		"✅ 恢复完成\n成功：%d\n失败：%d",
		success,
		failed,
	))
}

func (p *Plugin) delete(ctx context.Context, request command.Request) error {
	if len(request.Args) != 2 {
		return p.respond(ctx, request, "❌ 用法："+request.Prefix+"tb delete <ID>")
	}
	id, err := strconv.ParseInt(request.Args[1], 10, 64)
	if err != nil || id <= 0 {
		return p.respond(ctx, request, "❌ 备份 ID 无效")
	}
	deleted, err := p.database.delete(ctx, id)
	if err != nil {
		return p.respond(ctx, request, "❌ 删除失败："+err.Error())
	}
	if !deleted {
		return p.respond(ctx, request, "❌ 备份不存在")
	}
	return p.respond(ctx, request, fmt.Sprintf("✅ 已删除备份 #%d", id))
}

func (p *Plugin) clear(ctx context.Context, request command.Request) error {
	if len(request.Args) != 2 || !strings.EqualFold(request.Args[1], "confirm") {
		return p.respond(ctx, request,
			"⚠️ 该操作会清空所有消息与链接备份。\n确认请发送 "+
				request.Prefix+"tb clear confirm")
	}
	if err := p.database.clear(ctx); err != nil {
		return p.respond(ctx, request, "❌ 清空备份失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 已清空全部 Telegram 备份")
}

func (p *Plugin) finishText(
	ctx context.Context,
	request command.Request,
	text string,
) error {
	parts := splitText(text, 3900)
	if len(parts) == 0 {
		return nil
	}
	if err := p.respond(ctx, request, parts[0]); err != nil {
		return err
	}
	for _, part := range parts[1:] {
		if _, err := p.services.Telegram.SendText(
			ctx,
			request.Message.ChatID,
			part,
		); err != nil {
			return err
		}
	}
	return nil
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

func (p *Plugin) deleteCommand(ctx context.Context, request command.Request) error {
	if !request.Message.Outgoing {
		return nil
	}
	return p.services.Telegram.DeleteMessages(
		ctx,
		request.Message.ChatID,
		[]int{request.Message.ID},
	)
}

func splitText(text string, limit int) []string {
	runes := []rune(strings.TrimSpace(text))
	var result []string
	for len(runes) > limit {
		cut := limit
		for index := limit; index > limit/2; index-- {
			if runes[index-1] == '\n' {
				cut = index
				break
			}
		}
		result = append(result, strings.TrimSpace(string(runes[:cut])))
		runes = runes[cut:]
	}
	if len(runes) > 0 {
		result = append(result, strings.TrimSpace(string(runes)))
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func helpText(prefix string) string {
	return "📦 Telegram 对话备份\n\n" +
		"消息备份：\n" +
		prefix + "tb saved\n" +
		prefix + "tb private\n" +
		prefix + "tb chat [@用户名]\n" +
		prefix + "tb all\n\n" +
		"群组/频道：\n" +
		prefix + "tb groups | channels | links\n" +
		prefix + "tb showgroups\n" +
		prefix + "tb join\n\n" +
		"管理：\n" +
		prefix + "tb list\n" +
		prefix + "tb export <ID> | exportall\n" +
		prefix + "tb restore（回复 JSON/ZIP）\n" +
		prefix + "tb delete <ID>\n" +
		prefix + "tb clear confirm\n\n" +
		"消息备份保存文字与媒体元数据，不会重复下载所有媒体文件。"
}
