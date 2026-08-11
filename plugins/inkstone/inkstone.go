package inkstone

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const (
	pluginName         = "inkstone"
	bindingsKey        = "bindings"
	entryFormatVersion = "telegram-markdown-v2"
	searchCacheTTL     = 30 * time.Minute
)

var (
	noteIDPattern  = regexp.MustCompile(`^[0-9a-hjkmnp-tv-z]{26}$`)
	noteURLPattern = regexp.MustCompile(`(?i)(^|/n/)([0-9a-hjkmnp-tv-z]{26})($|[/?#])`)
)

type binding struct {
	Alias  string `json:"alias"`
	NoteID string `json:"note_id"`
	Title  string `json:"title,omitempty"`
}

type cachedSearch struct {
	Version   int                `json:"version"`
	Query     string             `json:"query"`
	Results   []noteSearchResult `json:"results"`
	CreatedAt int64              `json:"created_at"`
}

type pendingWrite struct {
	Version   int         `json:"version"`
	Completed bool        `json:"completed"`
	Request   editRequest `json:"request"`
	Result    editedNote  `json:"result,omitempty"`
}

type Plugin struct {
	services service.Container
}

func New(services service.Container) *Plugin {
	return &Plugin{services: services}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        pluginName,
		Version:     "0.2.5",
		Description: "将 Telegram 文字、链接和消息来源写入 Inkstone 笔记",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "inkstone",
		Aliases:     []string{"ink"},
		Description: "把文字或回复的消息追加到 Inkstone 笔记",
		Usage: []string{
			"ink <简称> <内容>",
			"ink <简称>（回复一条文字消息）",
			"ink <简称> -force（回复消息并强制重复写入）",
			"ink find <标题或关键词>",
			"ink bind <简称> <标题|#序号|笔记ID>",
			"ink list",
			"ink status",
			"ink test",
		},
		HelpHTML:  guideHTML,
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 || strings.EqualFold(request.Args[0], "help") {
		return p.respondHTML(ctx, request, helpHTML(request.Prefix))
	}
	switch strings.ToLower(request.Args[0]) {
	case "bind":
		return p.bind(ctx, request)
	case "find", "search":
		return p.find(ctx, request)
	case "unbind", "delete", "del":
		return p.unbind(ctx, request)
	case "list":
		return p.list(ctx, request)
	case "status":
		return p.status(ctx, request)
	case "test":
		return p.test(ctx, request)
	default:
		return p.write(ctx, request)
	}
}

func (p *Plugin) bind(ctx context.Context, request command.Request) error {
	alias, targetValue, err := parseBindRequest(request.RawArgs)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error()+"\n\n用法："+
			request.Prefix+"ink bind <简称> <标题|#序号|笔记ID>")
	}
	if err := validateAlias(alias); err != nil {
		return p.respond(ctx, request, "❌ 简称无效："+err.Error())
	}
	target, choices, err := p.resolveBindingTarget(ctx, request, targetValue)
	if err != nil {
		return p.respond(ctx, request, "❌ 查找 Inkstone 笔记失败："+err.Error())
	}
	if len(choices) > 0 {
		return p.respondHTML(ctx, request, formatSearchResults(
			request.Prefix, alias, targetValue, choices,
		))
	}
	bindings, err := p.loadBindings(ctx)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取绑定失败："+err.Error())
	}
	bindings[alias] = binding{
		Alias:  alias,
		NoteID: target.ID,
		Title:  target.Title,
	}
	if err := p.saveBindings(ctx, bindings); err != nil {
		return p.respond(ctx, request, "❌ 保存绑定失败："+err.Error())
	}
	name := target.Title
	if name == "" {
		name = target.ID
	}
	return p.respond(ctx, request, fmt.Sprintf("✅ 已绑定 %s → %s", alias, name))
}

func (p *Plugin) find(ctx context.Context, request command.Request) error {
	query := afterFirstField(request.RawArgs)
	if query == "" {
		return p.respond(ctx, request,
			"用法："+request.Prefix+"ink find <笔记标题或关键词>",
		)
	}
	client, err := p.client()
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	results, err := client.searchNotes(ctx, query)
	if err != nil {
		return p.respond(ctx, request, "❌ 搜索 Inkstone 笔记失败："+err.Error())
	}
	if len(results) == 0 {
		return p.respond(ctx, request, "没有找到与“"+query+"”匹配的笔记")
	}
	if err := p.saveSearchCache(ctx, request, query, results); err != nil {
		return p.respond(ctx, request, "❌ 保存搜索结果失败："+err.Error())
	}
	return p.respondHTML(ctx, request, formatSearchResults(
		request.Prefix, "", query, results,
	))
}

func (p *Plugin) unbind(ctx context.Context, request command.Request) error {
	if len(request.Args) != 2 {
		return p.respond(ctx, request, "用法："+request.Prefix+"ink unbind <简称>")
	}
	alias := normalizeAlias(request.Args[1])
	bindings, err := p.loadBindings(ctx)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取绑定失败："+err.Error())
	}
	if _, exists := bindings[alias]; !exists {
		return p.respond(ctx, request, "❌ 没有找到简称："+alias)
	}
	delete(bindings, alias)
	if err := p.saveBindings(ctx, bindings); err != nil {
		return p.respond(ctx, request, "❌ 删除绑定失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 已删除绑定："+alias)
}

func (p *Plugin) list(ctx context.Context, request command.Request) error {
	bindings, err := p.loadBindings(ctx)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取绑定失败："+err.Error())
	}
	if len(bindings) == 0 {
		return p.respond(ctx, request,
			"尚未绑定笔记。\n使用 "+request.Prefix+"ink find <标题> 查找笔记。",
		)
	}
	names := make([]string, 0, len(bindings))
	for name := range bindings {
		names = append(names, name)
	}
	sort.Strings(names)
	var result strings.Builder
	result.WriteString("🪨 Inkstone 笔记绑定\n\n")
	for _, name := range names {
		item := bindings[name]
		target := strings.TrimSpace(item.Title)
		if target == "" {
			target = item.NoteID
		}
		fmt.Fprintf(&result, "• %s → %s\n", item.Alias, target)
	}
	return p.respond(ctx, request, strings.TrimSpace(result.String()))
}

func (p *Plugin) status(ctx context.Context, request command.Request) error {
	endpointConfigured := strings.TrimSpace(os.Getenv("INKSTONE_MCP_URL")) != ""
	keyConfigured := strings.TrimSpace(os.Getenv("INKSTONE_API_KEY")) != ""
	bindings, err := p.loadBindings(ctx)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取绑定失败："+err.Error())
	}
	return p.respond(ctx, request, fmt.Sprintf(
		"🪨 Inkstone 状态\n\nMCP 地址：%s\nAPI 密钥：%s\n已绑定笔记：%d",
		endpointStatus(endpointConfigured),
		yesNo(keyConfigured),
		len(bindings),
	))
}

func (p *Plugin) test(ctx context.Context, request command.Request) error {
	client, err := p.client()
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	if err := client.test(ctx); err != nil {
		return p.respond(ctx, request, "❌ Inkstone 连接失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ Inkstone 连接正常，API 密钥可以读取笔记")
}

func (p *Plugin) write(ctx context.Context, request command.Request) error {
	alias := normalizeAlias(request.Args[0])
	bindings, err := p.loadBindings(ctx)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取绑定失败："+err.Error())
	}
	target, exists := bindings[alias]
	if !exists {
		return p.respond(ctx, request,
			"❌ 没有绑定笔记简称 “"+alias+"”。\n先使用 "+
				request.Prefix+"ink find <标题> 查找笔记。",
		)
	}

	text, force := parseWriteInput(request.RawArgs)
	var source *entrySource
	entryMessage := telegram.Message{}
	sourceMessageID := request.Message.ID
	if text == "" {
		if request.Message.ReplyToID == 0 {
			return p.respond(ctx, request,
				"❌ 请在简称后输入内容，或回复一条文字消息后再执行命令。",
			)
		}
		messages, getErr := p.services.Telegram.GetMessages(
			ctx,
			request.Message.ChatID,
			[]int{request.Message.ReplyToID},
		)
		if getErr != nil || len(messages) == 0 {
			return p.respond(ctx, request, "❌ 读取回复消息失败")
		}
		entryMessage = messages[0]
		resolved := resolveEntrySource(ctx, p.services, messages[0])
		source = &resolved
		sourceMessageID = messages[0].ID
	} else {
		entryMessage = request.Message
		entryMessage.Text = text
		entryMessage.Entities = sliceMessageEntities(
			request.Message.Text,
			request.Message.Entities,
			request.RawArgs,
			text,
		)
	}

	entryMessage = p.resolveCustomEmojiEntities(ctx, entryMessage)
	entry, err := buildPlainEntry(entryMessage, source)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	client, err := p.client()
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}

	operationMessageID := sourceMessageID
	if force {
		operationMessageID = request.Message.ID
	}
	operationID := stableOperationID(request.Message.ChatID, operationMessageID, target.NoteID)
	result, reused, err := p.executeWrite(ctx, client, target, operationID, entry)
	if err != nil {
		return p.respond(ctx, request, "❌ 写入 Inkstone 失败："+err.Error())
	}
	title := strings.TrimSpace(result.Title)
	if title == "" {
		title = alias
	}
	if p.services.Logger != nil {
		p.services.Logger.Info(
			"Inkstone write completed",
			"alias", alias,
			"note_id", target.NoteID,
			"source_message_id", sourceMessageID,
			"command_message_id", request.Message.ID,
			"force", force,
			"reused", reused,
			"revision", result.Rev,
		)
	}
	if reused {
		return p.respond(ctx, request,
			"ℹ️ 这条消息已经写入过，本次未重复追加。\n如需再次写入："+
				request.Prefix+"ink "+alias+" -force",
		)
	}
	if force {
		return p.respond(ctx, request, "✅ 已强制写入："+title)
	}
	return p.respond(ctx, request, "✅ 已写入："+title)
}

func (p *Plugin) executeWrite(
	ctx context.Context,
	client *mcpClient,
	target binding,
	operationID string,
	entry string,
) (editedNote, bool, error) {
	key := "operation:" + operationID
	for attempt := 0; attempt < 2; attempt++ {
		pending, found, err := p.loadPending(ctx, key)
		if err != nil {
			return editedNote{}, false, err
		}
		if found && pending.Completed {
			return pending.Result, true, nil
		}
		if !found {
			request, prepareErr := prepareWrite(ctx, client, target, operationID, entry)
			if prepareErr != nil {
				return editedNote{}, false, prepareErr
			}
			pending = pendingWrite{Version: 1, Request: request}
			if err := p.savePending(ctx, key, pending); err != nil {
				return editedNote{}, false, err
			}
		}

		result, editErr := client.editNote(ctx, pending.Request)
		if editErr == nil {
			pending.Completed = true
			pending.Result = result
			if err := p.savePending(ctx, key, pending); err != nil {
				return editedNote{}, false, err
			}
			return result, false, nil
		}
		if attempt == 0 && isRevisionConflict(editErr) {
			if err := p.services.Storage.Delete(ctx, pluginName, key); err != nil &&
				!errors.Is(err, storage.ErrNotFound) {
				return editedNote{}, false, err
			}
			continue
		}
		return editedNote{}, false, editErr
	}
	return editedNote{}, false, errors.New("笔记版本持续变化，请稍后重试")
}

func prepareWrite(
	ctx context.Context,
	client *mcpClient,
	target binding,
	operationID string,
	entry string,
) (editRequest, error) {
	note, err := client.fetchNote(ctx, target.NoteID)
	if err != nil {
		return editRequest{}, err
	}
	request := editRequest{
		OperationID: operationID,
		NoteID:      target.NoteID,
		ExpectedRev: note.Rev,
		Operation:   "append",
		Text:        entry,
	}
	return request, nil
}

func (p *Plugin) resolveBindingTarget(
	ctx context.Context,
	request command.Request,
	value string,
) (noteSearchResult, []noteSearchResult, error) {
	if noteID := extractNoteID(value); noteID != "" {
		return noteSearchResult{ID: noteID}, nil, nil
	}
	if strings.HasPrefix(value, "#") {
		index, err := strconv.Atoi(strings.TrimPrefix(value, "#"))
		if err != nil || index < 1 {
			return noteSearchResult{}, nil, errors.New("搜索结果序号格式不正确")
		}
		cached, err := p.loadSearchCache(ctx, request)
		if err != nil {
			return noteSearchResult{}, nil, err
		}
		if index > len(cached.Results) {
			return noteSearchResult{}, nil, fmt.Errorf(
				"搜索结果中没有第 %d 项，请重新执行 %sink find",
				index,
				request.Prefix,
			)
		}
		return cached.Results[index-1], nil, nil
	}

	client, err := p.client()
	if err != nil {
		return noteSearchResult{}, nil, err
	}
	results, err := client.searchNotes(ctx, value)
	if err != nil {
		return noteSearchResult{}, nil, err
	}
	if len(results) == 0 {
		return noteSearchResult{}, nil, errors.New("没有找到与“" + value + "”匹配的笔记")
	}
	if err := p.saveSearchCache(ctx, request, value, results); err != nil {
		return noteSearchResult{}, nil, err
	}
	exact := make([]noteSearchResult, 0, len(results))
	for _, item := range results {
		if strings.EqualFold(strings.TrimSpace(item.Title), strings.TrimSpace(value)) {
			exact = append(exact, item)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil, nil
	}
	if len(results) == 1 {
		return results[0], nil, nil
	}
	return noteSearchResult{}, results, nil
}

func (p *Plugin) client() (*mcpClient, error) {
	return newMCPClient(
		p.services.HTTP,
		os.Getenv("INKSTONE_MCP_URL"),
		os.Getenv("INKSTONE_API_KEY"),
	)
}

func (p *Plugin) saveSearchCache(
	ctx context.Context,
	request command.Request,
	query string,
	results []noteSearchResult,
) error {
	if p.services.Storage == nil {
		return errors.New("存储服务不可用")
	}
	value := cachedSearch{
		Version:   1,
		Query:     strings.TrimSpace(query),
		Results:   append([]noteSearchResult(nil), results...),
		CreatedAt: time.Now().Unix(),
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return p.services.Storage.Put(ctx, pluginName, searchCacheKey(request), raw)
}

func (p *Plugin) loadSearchCache(
	ctx context.Context,
	request command.Request,
) (cachedSearch, error) {
	if p.services.Storage == nil {
		return cachedSearch{}, errors.New("存储服务不可用")
	}
	key := searchCacheKey(request)
	raw, err := p.services.Storage.Get(ctx, pluginName, key)
	if errors.Is(err, storage.ErrNotFound) {
		return cachedSearch{}, errors.New("没有可用的搜索结果，请先执行 ink find")
	}
	if err != nil {
		return cachedSearch{}, err
	}
	var result cachedSearch
	if err := json.Unmarshal(raw, &result); err != nil || result.Version != 1 {
		return cachedSearch{}, errors.New("搜索结果记录已损坏，请重新搜索")
	}
	created := time.Unix(result.CreatedAt, 0)
	if result.CreatedAt < 1 || time.Since(created) > searchCacheTTL ||
		time.Since(created) < -time.Minute {
		_ = p.services.Storage.Delete(ctx, pluginName, key)
		return cachedSearch{}, errors.New("搜索结果已过期，请重新执行 ink find")
	}
	if len(result.Results) == 0 {
		return cachedSearch{}, errors.New("搜索结果为空，请重新执行 ink find")
	}
	return result, nil
}

func (p *Plugin) loadBindings(ctx context.Context) (map[string]binding, error) {
	result := make(map[string]binding)
	if p.services.Storage == nil {
		return nil, errors.New("存储服务不可用")
	}
	raw, err := p.services.Storage.Get(ctx, pluginName, bindingsKey)
	if errors.Is(err, storage.ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("笔记绑定数据已损坏")
	}
	return result, nil
}

func (p *Plugin) saveBindings(ctx context.Context, bindings map[string]binding) error {
	raw, err := json.Marshal(bindings)
	if err != nil {
		return err
	}
	return p.services.Storage.Put(ctx, pluginName, bindingsKey, raw)
}

func (p *Plugin) loadPending(
	ctx context.Context,
	key string,
) (pendingWrite, bool, error) {
	raw, err := p.services.Storage.Get(ctx, pluginName, key)
	if errors.Is(err, storage.ErrNotFound) {
		return pendingWrite{}, false, nil
	}
	if err != nil {
		return pendingWrite{}, false, err
	}
	var result pendingWrite
	if err := json.Unmarshal(raw, &result); err != nil || result.Version != 1 {
		return pendingWrite{}, false, errors.New("写入重试记录已损坏")
	}
	return result, true, nil
}

func (p *Plugin) savePending(ctx context.Context, key string, value pendingWrite) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return p.services.Storage.Put(ctx, pluginName, key, raw)
}

func (p *Plugin) respond(ctx context.Context, request command.Request, text string) error {
	if request.Message.Outgoing {
		_, err := p.services.Telegram.EditText(ctx, request.Message.ChatID, request.Message.ID, text)
		return err
	}
	_, err := p.services.Telegram.ReplyText(ctx, request.Message.ChatID, request.Message.ID, text)
	return err
}

func (p *Plugin) respondHTML(ctx context.Context, request command.Request, text string) error {
	if request.Message.Outgoing {
		_, err := telegram.EditHTML(ctx, p.services.Telegram, request.Message.ChatID, request.Message.ID, text)
		return err
	}
	_, err := telegram.ReplyHTML(ctx, p.services.Telegram, request.Message.ChatID, request.Message.ID, text)
	return err
}

func normalizeAlias(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateAlias(value string) error {
	if value == "" || len([]rune(value)) > 32 || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return errors.New("简称需为不超过 32 个字符且不含空格的名称")
	}
	switch value {
	case "bind", "unbind", "delete", "del", "find", "search", "list", "status", "test", "help":
		return errors.New("该名称是插件命令，不能作为笔记简称")
	}
	return nil
}

func parseBindRequest(raw string) (string, string, error) {
	rest := afterFirstField(raw)
	alias, rest, ok := splitFirstField(rest)
	if !ok || strings.TrimSpace(rest) == "" {
		return "", "", errors.New("请提供笔记简称和标题")
	}
	fields := strings.Fields(rest)
	last := strings.ToLower(fields[len(fields)-1])
	if last == "month" {
		return "", "", errors.New("当前版本只支持普通文本追加，请去掉 month")
	}
	if last == "append" {
		lastIndex := strings.LastIndex(rest, fields[len(fields)-1])
		rest = strings.TrimSpace(rest[:lastIndex])
	}
	if strings.HasPrefix(strings.ToLower(rest), "title ") {
		rest = strings.TrimSpace(rest[len("title "):])
	}
	if rest == "" {
		return "", "", errors.New("笔记标题不能为空")
	}
	return normalizeAlias(alias), rest, nil
}

func splitFirstField(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	index := strings.IndexFunc(value, unicode.IsSpace)
	if index < 0 {
		return value, "", true
	}
	return value[:index], strings.TrimSpace(value[index:]), true
}

func extractNoteID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if noteIDPattern.MatchString(value) {
		return value
	}
	match := noteURLPattern.FindStringSubmatch(value)
	if len(match) == 4 {
		return strings.ToLower(match[2])
	}
	return ""
}

func searchCacheKey(request command.Request) string {
	return fmt.Sprintf(
		"search:%d:%d",
		request.Message.ChatID,
		request.Message.SenderID,
	)
}

func formatSearchResults(
	prefix string,
	alias string,
	query string,
	results []noteSearchResult,
) string {
	var output strings.Builder
	output.WriteString("<b>🔎 Inkstone 笔记</b>\n")
	output.WriteString("关键词：<code>" + html.EscapeString(query) + "</code>\n\n")
	for index, item := range results {
		fmt.Fprintf(
			&output,
			"%d. %s\n",
			index+1,
			html.EscapeString(truncateText(item.Title, 120)),
		)
	}
	output.WriteString("\n")
	if alias == "" {
		output.WriteString("绑定其中一项：\n<code>" +
			html.EscapeString(prefix+"ink bind <简称> #序号") + "</code>")
		return output.String()
	}
	commandText := prefix + "ink bind " + alias + " #序号"
	output.WriteString("找到多个结果，选择后继续：\n<code>" +
		html.EscapeString(commandText) + "</code>")
	return output.String()
}

func truncateText(value string, limit int) string {
	characters := []rune(strings.TrimSpace(value))
	if len(characters) <= limit {
		return string(characters)
	}
	return string(characters[:limit]) + "…"
}

func afterFirstField(value string) string {
	value = strings.TrimSpace(value)
	index := strings.IndexFunc(value, unicode.IsSpace)
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(value[index:])
}

func parseWriteInput(raw string) (string, bool) {
	content := afterFirstField(raw)
	first, remaining, ok := splitFirstField(content)
	if ok && strings.EqualFold(first, "-force") {
		return remaining, true
	}
	return content, false
}

func stableOperationID(chatID int64, messageID int, noteID string) string {
	value := fmt.Sprintf(
		"telebox-inkstone|%s|%d|%d|%s",
		entryFormatVersion,
		chatID,
		messageID,
		noteID,
	)
	digest := sha256.Sum256([]byte(value))
	return "tg_" + hex.EncodeToString(digest[:])
}

func isRevisionConflict(err error) bool {
	var toolErr *mcpToolError
	return errors.As(err, &toolErr) &&
		(toolErr.Status == 409 || strings.Contains(toolErr.Message, "被其他客户端修改"))
}

func yesNo(value bool) string {
	if value {
		return "已配置"
	}
	return "未配置"
}

func endpointStatus(configured bool) string {
	if configured {
		return "已配置"
	}
	return "未配置"
}

func helpHTML(prefix string) string {
	return strings.ReplaceAll(guideHTML, "{{prefix}}", html.EscapeString(prefix))
}

const guideHTML = `<b>🪨 Inkstone 笔记写入</b>

把 Telegram 中的文字、链接和消息来源保存到指定的 Inkstone 笔记。当前版本不上传图片、视频或文件。

<b>一、先在 Inkstone 配置</b>
1. 登录 Inkstone，打开“设置 → MCP”。
2. 启用 MCP，并允许“读取笔记”和“编辑笔记”。不需要开放回收站权限。
3. 创建名为 <code>TeleBox</code> 的 API 密钥，创建后立即复制保存。
4. 复制页面显示的远程 MCP 地址。每个用户应填写自己的地址。

<b>二、在 TeleBox 服务器配置</b>
编辑 <code>~/.config/telebox/telebox.env</code>，加入：
<pre>INKSTONE_MCP_URL=https://inkstone.example.com/mcp
INKSTONE_API_KEY=ink_你的密钥</pre>
保存后执行：
root 安装：<code>systemctl restart telebox.service</code>
普通用户安装：<code>systemctl --user restart telebox.service</code>

密钥不要发送到 Telegram，也不要写进插件命令。可用 <code>{{prefix}}ink test</code> 检查连接。

<b>三、查找并绑定笔记</b>
按标题或关键词查找：
<code>{{prefix}}ink find 浩希</code>

标题唯一时可以直接绑定：
<code>{{prefix}}ink bind hx 浩希</code>

如果找到多个结果，插件会显示编号。选择其中一项：
<code>{{prefix}}ink bind hx #2</code>

仍可使用 26 位笔记 ID 或完整的 <code>/n/笔记ID</code> 链接绑定，通常不需要手动查找 ID。

当前版本只在笔记末尾追加内容，不会额外生成任务列表或折叠区块。普通文字中的 Markdown 和 HTML 标记会安全转义；Telegram 原有的粗体、斜体、下划线、删除线、等宽、代码块、引用和链接会转换为 Inkstone 支持的 Markdown。普通 Emoji 会原样保留，自定义 Emoji 会转换为对应的普通 Emoji；无法识别的空白占位符不会写入。每次写入之间会保留空行，避免影响预览。

<b>四、写入内容</b>
直接写入：
<code>{{prefix}}ink hx 火锅店 200</code>

保存一条 Telegram 消息：先回复该消息，再发送：
<code>{{prefix}}ink hx</code>

回复写入会保留消息段落与已有文字格式，并在正文后另起一段保存正文外的链接和可取得的发送者、时间、聊天名称及消息链接。只有媒体、没有文字的消息暂不支持。

同一条 Telegram 消息写入同一篇笔记后，再次执行会提示“未重复追加”。确实需要再次写入时，回复原消息发送：
<code>{{prefix}}ink hx -force</code>

直接输入内容时也可以使用：
<code>{{prefix}}ink hx -force 火锅店 200</code>

写入成功后只显示笔记标题，不发送笔记地址。

<b>管理命令</b>
<code>{{prefix}}ink find &lt;关键词&gt;</code> 查找笔记
<code>{{prefix}}ink list</code> 查看所有绑定
<code>{{prefix}}ink unbind &lt;简称&gt;</code> 删除绑定
<code>{{prefix}}ink status</code> 查看配置状态
<code>{{prefix}}ink test</code> 测试 MCP 连接
<code>{{prefix}}ink help</code> 查看本说明`
