package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/legacyconfig"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type accessRecord struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type allowedMessage struct {
	ID       int64  `json:"id"`
	Message  string `json:"message"`
	Redirect string `json:"redirect,omitempty"`
}

type accessState struct {
	Users    []accessRecord   `json:"users"`
	Chats    []accessRecord   `json:"chats"`
	Messages []allowedMessage `json:"messages,omitempty"`
}

func (p *Plugin) loadAccessSettings(ctx context.Context) {
	sudo := p.loadAccessState(ctx, "sudo_access", "sudo/sudo.db", false)
	sure := p.loadAccessState(ctx, "sure_access", "sure/sure.db", true)
	p.accessMu.Lock()
	p.sudoAccess = sudo
	p.sureAccess = sure
	p.accessMu.Unlock()
	p.applySudoDelegates(sudo)
}

func (p *Plugin) loadAccessState(
	ctx context.Context,
	key, legacyPath string,
	includeMessages bool,
) accessState {
	if encoded, err := p.services.Storage.Get(ctx, "core", key); err == nil {
		var result accessState
		if decodeErr := json.Unmarshal(encoded, &result); decodeErr == nil {
			return normalizeAccessState(result)
		}
		p.services.Logger.Warn("decode persisted access state", "key", key)
	} else if !errors.Is(err, storage.ErrNotFound) {
		p.services.Logger.Warn("load persisted access state", "key", key, "error", err)
		return accessState{}
	}
	var legacy legacyconfig.AccessDatabase
	for _, databasePath := range legacyconfig.CandidatePaths(
		p.services.AssetsDir,
		p.services.LegacyAssetsDir,
		legacyPath,
	) {
		candidate, err := legacyconfig.ReadAccessDatabase(
			databasePath,
			includeMessages,
		)
		if err != nil {
			p.services.Logger.Warn(
				"read legacy access database",
				"path", databasePath,
				"error", err,
			)
			continue
		}
		if len(candidate.Users)+len(candidate.Chats)+len(candidate.Messages) > 0 {
			legacy = candidate
			break
		}
	}
	result := accessState{}
	for _, item := range legacy.Users {
		result.Users = append(result.Users, accessRecord{ID: item.ID, Name: item.Name})
	}
	for _, item := range legacy.Chats {
		result.Chats = append(result.Chats, accessRecord{ID: item.ID, Name: item.Name})
	}
	for _, item := range legacy.Messages {
		result.Messages = append(result.Messages, allowedMessage{
			ID: item.ID, Message: item.Message, Redirect: item.Redirect,
		})
	}
	result = normalizeAccessState(result)
	if len(result.Users)+len(result.Chats)+len(result.Messages) > 0 {
		if encoded, encodeErr := json.Marshal(result); encodeErr == nil {
			if writeErr := p.services.Storage.Put(ctx, "core", key, encoded); writeErr != nil {
				p.services.Logger.Warn("persist migrated access state", "key", key, "error", writeErr)
			}
		}
	}
	return result
}

func normalizeAccessState(state accessState) accessState {
	state.Users = normalizeAccessRecords(state.Users, true)
	state.Chats = normalizeAccessRecords(state.Chats, false)
	sort.Slice(state.Messages, func(i, j int) bool {
		return state.Messages[i].ID < state.Messages[j].ID
	})
	return state
}

func normalizeAccessRecords(records []accessRecord, positiveOnly bool) []accessRecord {
	byID := make(map[int64]accessRecord)
	for _, item := range records {
		if item.ID == 0 || positiveOnly && item.ID < 0 {
			continue
		}
		if strings.TrimSpace(item.Name) == "" {
			item.Name = strconv.FormatInt(item.ID, 10)
		}
		byID[item.ID] = item
	}
	result := make([]accessRecord, 0, len(byID))
	for _, item := range byID {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (p *Plugin) sudo(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 {
		return p.sudoHelp(ctx, request)
	}
	action := strings.ToLower(request.Args[0])
	if action == "ls" || action == "list" {
		p.accessMu.RLock()
		state := cloneAccessState(p.sudoAccess)
		p.accessMu.RUnlock()
		return p.listAccess(ctx, request, "Sudo 用户", state.Users, false)
	}
	if action == "chat" {
		return p.manageAccessChats(ctx, request, false)
	}
	if action != "add" && action != "del" && action != "delete" {
		return p.sudoHelp(ctx, request)
	}
	target := ""
	if len(request.Args) > 1 {
		target = request.Args[1]
	}
	record, err := p.resolveAccessUser(ctx, request, target)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	p.accessMu.RLock()
	state := cloneAccessState(p.sudoAccess)
	p.accessMu.RUnlock()
	if action == "add" {
		state.Users = upsertAccessRecord(state.Users, record)
	} else if !removeAccessRecord(&state.Users, record.ID) {
		return p.respond(ctx, request, "❌ 该用户不在 Sudo 列表中")
	}
	if err := p.saveAccessState(ctx, "sudo_access", state, false); err != nil {
		return p.respond(ctx, request, "❌ 保存 Sudo 设置失败："+err.Error())
	}
	return p.respondHTML(ctx, request, fmt.Sprintf(
		"✅ 已%s：<a href=\"tg://user?id=%d\">%s</a> <code>%d</code>",
		map[bool]string{true: "添加", false: "删除"}[action == "add"],
		record.ID,
		html.EscapeString(record.Name),
		record.ID,
	))
}

func (p *Plugin) sure(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 {
		return p.sureHelp(ctx, request)
	}
	action := strings.ToLower(request.Args[0])
	if action == "ls" || action == "list" {
		p.accessMu.RLock()
		state := cloneAccessState(p.sureAccess)
		p.accessMu.RUnlock()
		return p.listAccess(ctx, request, "Sure 用户", state.Users, false)
	}
	if action == "chat" {
		return p.manageAccessChats(ctx, request, true)
	}
	if action == "msg" {
		return p.manageSureMessages(ctx, request)
	}
	if action != "add" && action != "del" && action != "delete" {
		return p.sureHelp(ctx, request)
	}
	target := ""
	if len(request.Args) > 1 {
		target = request.Args[1]
	}
	record, err := p.resolveAccessUser(ctx, request, target)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	p.accessMu.RLock()
	state := cloneAccessState(p.sureAccess)
	p.accessMu.RUnlock()
	if action == "add" {
		state.Users = upsertAccessRecord(state.Users, record)
	} else if !removeAccessRecord(&state.Users, record.ID) {
		return p.respond(ctx, request, "❌ 该用户不在 Sure 列表中")
	}
	if err := p.saveAccessState(ctx, "sure_access", state, true); err != nil {
		return p.respond(ctx, request, "❌ 保存 Sure 设置失败："+err.Error())
	}
	return p.respondHTML(ctx, request, fmt.Sprintf(
		"✅ 已%s：<a href=\"tg://user?id=%d\">%s</a> <code>%d</code>",
		map[bool]string{true: "添加", false: "删除"}[action == "add"],
		record.ID,
		html.EscapeString(record.Name),
		record.ID,
	))
}

func (p *Plugin) manageAccessChats(
	ctx context.Context,
	request command.Request,
	sure bool,
) error {
	if len(request.Args) < 2 {
		if sure {
			return p.sureHelp(ctx, request)
		}
		return p.sudoHelp(ctx, request)
	}
	action := strings.ToLower(request.Args[1])
	p.accessMu.RLock()
	state := cloneAccessState(p.sudoAccess)
	if sure {
		state = cloneAccessState(p.sureAccess)
	}
	p.accessMu.RUnlock()
	if action == "ls" || action == "list" {
		return p.listAccess(ctx, request, "允许的对话", state.Chats, true)
	}
	if action != "add" && action != "del" && action != "delete" {
		return p.respond(ctx, request, "❌ 支持 chat add、chat del、chat ls")
	}
	target := ""
	if len(request.Args) > 2 {
		target = request.Args[2]
	}
	record, err := p.resolveAccessChat(ctx, request, target)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	if action == "add" {
		state.Chats = upsertAccessRecord(state.Chats, record)
	} else if !removeAccessRecord(&state.Chats, record.ID) {
		return p.respond(ctx, request, "❌ 该对话不在白名单中")
	}
	key := "sudo_access"
	if sure {
		key = "sure_access"
	}
	if err := p.saveAccessState(ctx, key, state, sure); err != nil {
		return p.respond(ctx, request, "❌ 保存对话白名单失败："+err.Error())
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf(
			"✅ 已%s对话：%s (%d)",
			map[bool]string{true: "添加", false: "删除"}[action == "add"],
			record.Name,
			record.ID,
		),
	)
}

func (p *Plugin) manageSureMessages(
	ctx context.Context,
	request command.Request,
) error {
	if len(request.Args) < 2 {
		return p.sureHelp(ctx, request)
	}
	action := strings.ToLower(request.Args[1])
	p.accessMu.RLock()
	state := cloneAccessState(p.sureAccess)
	p.accessMu.RUnlock()
	switch action {
	case "ls", "list":
		if len(state.Messages) == 0 {
			return p.respond(ctx, request, "⚠️ 尚未设置消息白名单")
		}
		lines := []string{"<b>Sure 消息白名单</b>", ""}
		for _, item := range state.Messages {
			line := fmt.Sprintf(
				"• <code>%d</code>：<code>%s</code>",
				item.ID,
				html.EscapeString(item.Message),
			)
			if item.Redirect != "" {
				line += " → <code>" + html.EscapeString(item.Redirect) + "</code>"
			}
			lines = append(lines, line)
		}
		return p.respondHTML(ctx, request, strings.Join(lines, "\n"))
	case "add":
		message := rawAfterWords(request.RawArgs, 2)
		if message == "" {
			return p.respond(ctx, request, "❌ 用法："+request.Prefix+"sure msg add <消息>")
		}
		for index := range state.Messages {
			if state.Messages[index].Message == message {
				return p.respond(ctx, request, "❌ 该消息已在白名单中")
			}
		}
		var nextID int64 = 1
		for _, item := range state.Messages {
			if item.ID >= nextID {
				nextID = item.ID + 1
			}
		}
		state.Messages = append(state.Messages, allowedMessage{ID: nextID, Message: message})
	case "del", "delete":
		if len(request.Args) != 3 {
			return p.respond(ctx, request, "❌ 用法："+request.Prefix+"sure msg del <ID>")
		}
		id, err := strconv.ParseInt(request.Args[2], 10, 64)
		if err != nil || !removeAllowedMessage(&state.Messages, id) {
			return p.respond(ctx, request, "❌ 未找到该消息 ID")
		}
	case "redirect":
		if len(request.Args) < 3 {
			return p.respond(ctx, request, "❌ 用法："+request.Prefix+"sure msg redirect <ID> [消息]")
		}
		id, err := strconv.ParseInt(request.Args[2], 10, 64)
		if err != nil {
			return p.respond(ctx, request, "❌ 消息 ID 无效")
		}
		redirect := rawAfterWords(request.RawArgs, 3)
		found := false
		for index := range state.Messages {
			if state.Messages[index].ID == id {
				state.Messages[index].Redirect = redirect
				found = true
				break
			}
		}
		if !found {
			return p.respond(ctx, request, "❌ 未找到该消息 ID")
		}
	default:
		return p.sureHelp(ctx, request)
	}
	if err := p.saveAccessState(ctx, "sure_access", state, true); err != nil {
		return p.respond(ctx, request, "❌ 保存消息白名单失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ Sure 消息白名单已更新")
}

func (p *Plugin) resolveAccessUser(
	ctx context.Context,
	request command.Request,
	target string,
) (accessRecord, error) {
	if target == "" && request.Message.ReplyToID > 0 {
		messages, err := p.services.Telegram.GetMessages(
			ctx,
			request.Message.ChatID,
			[]int{request.Message.ReplyToID},
		)
		if err != nil || len(messages) == 0 || messages[0].SenderID <= 0 {
			return accessRecord{}, errors.New("无法读取回复用户")
		}
		target = strconv.FormatInt(messages[0].SenderID, 10)
	}
	if target == "" {
		return accessRecord{}, errors.New("请回复目标用户或提供用户 ID/@用户名")
	}
	user, err := p.services.Telegram.ResolveUser(ctx, target)
	if err != nil {
		return accessRecord{}, errors.New("无法解析目标用户")
	}
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if user.Username != "" {
		name = strings.TrimSpace(name + " @" + user.Username)
	}
	if name == "" {
		name = strconv.FormatInt(user.ID, 10)
	}
	return accessRecord{ID: user.ID, Name: name}, nil
}

func (p *Plugin) resolveAccessChat(
	ctx context.Context,
	request command.Request,
	target string,
) (accessRecord, error) {
	if target == "" {
		chat, err := p.services.Telegram.ResolveChat(ctx, request.Message.ChatID)
		if err != nil {
			return accessRecord{ID: request.Message.ChatID, Name: strconv.FormatInt(request.Message.ChatID, 10)}, nil
		}
		name := firstNonEmpty(chat.Title, chat.Username, strconv.FormatInt(chat.ID, 10))
		return accessRecord{ID: chat.ID, Name: name}, nil
	}
	if id, err := strconv.ParseInt(target, 10, 64); err == nil {
		if chat, resolveErr := p.services.Telegram.ResolveChat(ctx, id); resolveErr == nil {
			return accessRecord{ID: chat.ID, Name: firstNonEmpty(chat.Title, chat.Username, target)}, nil
		}
		return accessRecord{ID: id, Name: target}, nil
	}
	chat, err := p.services.Telegram.ResolveChatTarget(ctx, target)
	if err != nil {
		return accessRecord{}, errors.New("无法解析目标对话")
	}
	return accessRecord{ID: chat.ID, Name: firstNonEmpty(chat.Title, chat.Username, target)}, nil
}

func (p *Plugin) saveAccessState(
	ctx context.Context,
	key string,
	state accessState,
	sure bool,
) error {
	state = normalizeAccessState(state)
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := p.services.Storage.Put(ctx, "core", key, encoded); err != nil {
		return err
	}
	p.accessMu.Lock()
	if sure {
		p.sureAccess = state
	} else {
		p.sudoAccess = state
	}
	p.accessMu.Unlock()
	if !sure {
		p.applySudoDelegates(state)
	}
	return nil
}

func (p *Plugin) applySudoDelegates(state accessState) {
	users := make([]int64, 0, len(state.Users))
	chats := make([]int64, 0, len(state.Chats))
	for _, item := range state.Users {
		users = append(users, item.ID)
	}
	for _, item := range state.Chats {
		chats = append(chats, item.ID)
	}
	p.router.SetDelegates(users, chats)
}

func (p *Plugin) listAccess(
	ctx context.Context,
	request command.Request,
	title string,
	records []accessRecord,
	emptyMeansAll bool,
) error {
	if len(records) == 0 {
		message := "当前列表为空"
		if emptyMeansAll {
			message = "⚠️ 未设置对话白名单，所有对话均可使用"
		}
		return p.respond(ctx, request, message)
	}
	lines := []string{"<b>" + html.EscapeString(title) + "</b>", ""}
	for _, item := range records {
		lines = append(lines, fmt.Sprintf(
			"• %s <code>%d</code>",
			html.EscapeString(item.Name),
			item.ID,
		))
	}
	return p.respondHTML(ctx, request, strings.Join(lines, "\n"))
}

func (p *Plugin) sudoHelp(ctx context.Context, request command.Request) error {
	return p.respondHTML(ctx, request,
		"<b>🛡️ Sudo 权限</b>\n\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"sudo add|del &lt;用户&gt;</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"sudo ls</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"sudo chat add|del [对话]</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"sudo chat ls</code>\n\n"+
			"未设置对话白名单时，授权用户可在所有对话使用命令。",
	)
}

func (p *Plugin) sureHelp(ctx context.Context, request command.Request) error {
	return p.respondHTML(ctx, request,
		"<b>✅ Sure 受控代发</b>\n\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"sure add|del &lt;用户&gt;</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"sure ls</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"sure chat add|del [对话]</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"sure msg add &lt;消息&gt;</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"sure msg redirect &lt;ID&gt; [消息]</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"sure msg del &lt;ID&gt;</code>\n"+
			"• <code>"+html.EscapeString(request.Prefix)+"sure msg ls</code>\n\n"+
			"消息以 <code>_command:</code> 开头时，会按命令前缀匹配并保留其后参数。",
	)
}

func (p *Plugin) ListensToMessages() bool {
	p.accessMu.RLock()
	defer p.accessMu.RUnlock()
	return len(p.sureAccess.Users) > 0 && len(p.sureAccess.Messages) > 0
}

func (p *Plugin) OnMessage(ctx context.Context, message telegram.Message) error {
	if message.Outgoing || strings.TrimSpace(message.Text) == "" ||
		message.ForwardSenderID != 0 || message.ForwardName != "" {
		return nil
	}
	p.accessMu.RLock()
	state := cloneAccessState(p.sureAccess)
	p.accessMu.RUnlock()
	if !containsAccessID(state.Users, message.SenderID) ||
		len(state.Chats) > 0 && !containsAccessID(state.Chats, message.ChatID) {
		return nil
	}
	output, matched := matchSureMessage(state.Messages, message.Text)
	if !matched {
		return nil
	}
	synthetic := message
	synthetic.Text = output
	if _, commandLike := p.router.Parse(synthetic); commandLike {
		_, err := p.router.DispatchAsOwner(ctx, synthetic)
		p.router.SuppressNext(message)
		if err != nil {
			return err
		}
	} else {
		if _, err := p.services.Telegram.SendText(ctx, message.ChatID, output); err != nil {
			return err
		}
	}
	go func(chatID int64, messageID int) {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		<-timer.C
		deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.services.Telegram.DeleteMessages(
			deleteCtx,
			chatID,
			[]int{messageID},
		); err != nil {
			p.services.Logger.Warn("delete Sure source message", "error", err)
		}
	}(message.ChatID, message.ID)
	return nil
}

func matchSureMessage(records []allowedMessage, message string) (string, bool) {
	for _, item := range records {
		if strings.HasPrefix(item.Message, "_command:") {
			prefix := strings.TrimPrefix(item.Message, "_command:")
			if message == prefix || strings.HasPrefix(message, prefix+" ") {
				if item.Redirect != "" {
					return item.Redirect + strings.TrimPrefix(message, prefix), true
				}
				return message, true
			}
			continue
		}
		if item.Message == message {
			if item.Redirect != "" {
				return item.Redirect, true
			}
			return message, true
		}
	}
	return "", false
}

func rawAfterWords(value string, count int) string {
	value = strings.TrimSpace(value)
	for index := 0; index < count; index++ {
		end := strings.IndexAny(value, " \t\r\n")
		if end < 0 {
			return ""
		}
		value = strings.TrimSpace(value[end:])
	}
	return value
}

func cloneAccessState(state accessState) accessState {
	return accessState{
		Users:    append([]accessRecord(nil), state.Users...),
		Chats:    append([]accessRecord(nil), state.Chats...),
		Messages: append([]allowedMessage(nil), state.Messages...),
	}
}

func upsertAccessRecord(records []accessRecord, item accessRecord) []accessRecord {
	for index := range records {
		if records[index].ID == item.ID {
			records[index] = item
			return normalizeAccessRecords(records, item.ID > 0)
		}
	}
	return normalizeAccessRecords(append(records, item), item.ID > 0)
}

func removeAccessRecord(records *[]accessRecord, id int64) bool {
	for index, item := range *records {
		if item.ID == id {
			*records = append((*records)[:index], (*records)[index+1:]...)
			return true
		}
	}
	return false
}

func removeAllowedMessage(records *[]allowedMessage, id int64) bool {
	for index, item := range *records {
		if item.ID == id {
			*records = append((*records)[:index], (*records)[index+1:]...)
			return true
		}
	}
	return false
}

func containsAccessID(records []accessRecord, id int64) bool {
	for _, item := range records {
		if item.ID == id {
			return true
		}
	}
	return false
}
