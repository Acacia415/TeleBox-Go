package trace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

var availableReactions = []string{
	"❤️‍🔥", "👍", "👎", "❤️", "🔥", "🥰", "👏", "😁", "🤔", "🤯", "😱",
	"🤬", "😢", "🎉", "🤩", "🤮", "💩", "🙏", "👌", "🕊", "🤡", "🥱",
	"🥴", "😍", "🐳", "🌚", "🌭", "💯", "🤣", "⚡️", "🍌", "🏆", "💔",
	"🤨", "😐", "🍓", "🍾", "💋", "🖕", "😈", "😎", "😇", "😤",
}

type traceConfig struct {
	KeepLog bool `json:"keep_log"`
	Big     bool `json:"big"`
}

type traceState struct {
	Users    map[string][]telegram.Reaction `json:"users"`
	Keywords map[string][]telegram.Reaction `json:"keywords"`
	Config   traceConfig                    `json:"config"`
}

type Plugin struct {
	services service.Container

	mu    sync.RWMutex
	state traceState
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services: services,
		state:    defaultState(),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "trace",
		Version:     "0.3.1",
		Description: "按用户或关键词自动发送 Telegram reaction",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "trace",
		Description: "管理用户和关键词 reaction 追踪",
		Usage: []string{
			"trace [reaction...]（回复目标消息；留空取消）",
			"trace kw add <关键词> <reaction...>",
			"trace kw del <关键词>",
			"trace status",
			"trace clean",
			"trace reset",
			"trace log <true|false>",
			"trace big <true|false>",
		},
		HelpHTML:  traceGuideHTML,
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(ctx context.Context) error {
	raw, err := p.services.Storage.Get(ctx, "trace", "state")
	if errors.Is(err, storage.ErrNotFound) {
		state, imported, migrateErr := p.loadLegacyState()
		if migrateErr != nil {
			return migrateErr
		}
		if !imported {
			return nil
		}
		normalizeState(&state)
		p.mu.Lock()
		p.state = state
		err = p.persistLocked(ctx)
		p.mu.Unlock()
		if err != nil {
			return err
		}
		p.services.Logger.Info(
			"migrated legacy trace state",
			"users", len(state.Users),
			"keywords", len(state.Keywords),
		)
		return nil
	}
	if err != nil {
		return err
	}
	var state traceState
	if err := json.Unmarshal(raw, &state); err != nil {
		p.services.Logger.Warn("decode trace state; using defaults", "error", err)
		return nil
	}
	normalizeState(&state)
	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
	return nil
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) OnMessage(ctx context.Context, message telegram.Message) error {
	if message.Outgoing || message.SenderID == 0 {
		return nil
	}
	p.mu.RLock()
	reactions := cloneReactions(p.state.Users[strconv.FormatInt(message.SenderID, 10)])
	if len(reactions) == 0 && message.Text != "" {
		keys := make([]string, 0, len(p.state.Keywords))
		for keyword := range p.state.Keywords {
			keys = append(keys, keyword)
		}
		sort.Strings(keys)
		for _, keyword := range keys {
			if strings.Contains(message.Text, keyword) {
				reactions = cloneReactions(p.state.Keywords[keyword])
				break
			}
		}
	}
	big := p.state.Config.Big
	p.mu.RUnlock()
	if len(reactions) == 0 {
		return nil
	}
	return p.services.Telegram.SendReaction(
		ctx,
		message.ChatID,
		message.ID,
		reactions,
		big,
	)
}

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if request.Message.ReplyToID > 0 {
		if len(request.Args) == 0 {
			return p.untraceUser(ctx, request)
		}
		return p.traceUser(ctx, request)
	}
	if len(request.Args) == 0 || strings.EqualFold(request.Args[0], "help") ||
		strings.EqualFold(request.Args[0], "h") {
		return p.respondPersistent(ctx, request, helpText(request.Prefix))
	}
	switch strings.ToLower(request.Args[0]) {
	case "kw":
		return p.keyword(ctx, request)
	case "status":
		return p.status(ctx, request)
	case "clean":
		return p.clean(ctx, request, false)
	case "reset":
		return p.clean(ctx, request, true)
	case "log":
		return p.setConfig(ctx, request, "log")
	case "big":
		return p.setConfig(ctx, request, "big")
	default:
		return p.respondPersistent(ctx, request, helpText(request.Prefix))
	}
}

func (p *Plugin) traceUser(ctx context.Context, request command.Request) error {
	userID, err := p.repliedUserID(ctx, request)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	reactions := parseReactions(strings.Join(request.Args, " "), request.Message.CustomEmojiIDs)
	if len(reactions) == 0 {
		return p.respond(ctx, request, "❌ 未找到有效 reaction")
	}
	p.mu.Lock()
	p.state.Users[strconv.FormatInt(userID, 10)] = reactions
	err = p.persistLocked(ctx)
	count := len(p.state.Users)
	p.mu.Unlock()
	if err != nil {
		return p.respond(ctx, request, "❌ 保存追踪失败："+err.Error())
	}
	if err := p.services.Telegram.SendReaction(
		ctx,
		request.Message.ChatID,
		request.Message.ReplyToID,
		reactions,
		p.big(),
	); err != nil {
		p.services.Logger.Warn("send initial trace reaction", "error", err)
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf(
			"✅ 已添加用户追踪\n\n• 用户 ID：%d\n• 表情：%s\n• 用户总数：%d",
			userID,
			reactionDisplay(reactions),
			count,
		),
	)
}

func (p *Plugin) untraceUser(ctx context.Context, request command.Request) error {
	userID, err := p.repliedUserID(ctx, request)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	key := strconv.FormatInt(userID, 10)
	p.mu.Lock()
	_, exists := p.state.Users[key]
	if exists {
		delete(p.state.Users, key)
		err = p.persistLocked(ctx)
	}
	remaining := len(p.state.Users)
	p.mu.Unlock()
	if err != nil {
		return p.respond(ctx, request, "❌ 保存追踪失败："+err.Error())
	}
	if !exists {
		return p.respond(ctx, request, "ℹ️ 该用户未被追踪")
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("🗑️ 已取消用户追踪\n\n• 用户 ID：%d\n• 用户总数：%d", userID, remaining),
	)
}

func (p *Plugin) keyword(ctx context.Context, request command.Request) error {
	if len(request.Args) < 3 {
		return p.respond(
			ctx,
			request,
			"用法："+request.Prefix+"trace kw <add|del> <关键词> [reaction]",
		)
	}
	action := strings.ToLower(request.Args[1])
	keyword := request.Args[2]
	switch action {
	case "add":
		reactions := parseReactions(
			strings.Join(request.Args[3:], " "),
			request.Message.CustomEmojiIDs,
		)
		if len(reactions) == 0 {
			return p.respond(ctx, request, "❌ 未找到有效 reaction")
		}
		p.mu.Lock()
		p.state.Keywords[keyword] = reactions
		err := p.persistLocked(ctx)
		count := len(p.state.Keywords)
		p.mu.Unlock()
		if err != nil {
			return p.respond(ctx, request, "❌ 保存关键字失败："+err.Error())
		}
		return p.respond(
			ctx,
			request,
			fmt.Sprintf(
				"✅ 已添加关键词追踪\n\n• 关键词：%s\n• 表情：%s\n• 关键词总数：%d",
				keyword,
				reactionDisplay(reactions),
				count,
			),
		)
	case "del":
		p.mu.Lock()
		_, exists := p.state.Keywords[keyword]
		delete(p.state.Keywords, keyword)
		err := p.persistLocked(ctx)
		p.mu.Unlock()
		if err != nil {
			return p.respond(ctx, request, "❌ 保存关键字失败："+err.Error())
		}
		if !exists {
			return p.respond(ctx, request, "ℹ️ 该关键字未被追踪")
		}
		return p.respond(ctx, request, "🗑️ 已删除关键字："+keyword)
	default:
		return p.respond(ctx, request, "❌ 关键字操作只支持 add 或 del")
	}
}

func (p *Plugin) status(ctx context.Context, request command.Request) error {
	p.mu.RLock()
	state := cloneState(p.state)
	p.mu.RUnlock()
	var lines []string
	lines = append(
		lines,
		"📊 Trace 追踪状态",
		"",
		fmt.Sprintf("• 用户：%d 个", len(state.Users)),
		fmt.Sprintf("• 关键词：%d 个", len(state.Keywords)),
		fmt.Sprintf("• 保留回执：%t", state.Config.KeepLog),
		fmt.Sprintf("• 大号动画：%t", state.Config.Big),
	)
	if len(state.Users) > 0 {
		lines = append(lines, "", "追踪用户：")
		keys := sortedKeys(state.Users)
		for index, key := range keys {
			if index >= 50 {
				lines = append(lines, "…")
				break
			}
			lines = append(lines, key+" → "+reactionDisplay(state.Users[key]))
		}
	}
	if len(state.Keywords) > 0 {
		lines = append(lines, "", "追踪关键词：")
		keys := sortedKeys(state.Keywords)
		for index, key := range keys {
			if index >= 50 {
				lines = append(lines, "…")
				break
			}
			lines = append(lines, key+" → "+reactionDisplay(state.Keywords[key]))
		}
	}
	return p.respondPersistent(ctx, request, strings.Join(lines, "\n"))
}

func (p *Plugin) clean(
	ctx context.Context,
	request command.Request,
	resetConfig bool,
) error {
	p.mu.Lock()
	users := len(p.state.Users)
	keywords := len(p.state.Keywords)
	config := p.state.Config
	p.state = defaultState()
	if !resetConfig {
		p.state.Config = config
	}
	err := p.persistLocked(ctx)
	p.mu.Unlock()
	if err != nil {
		return p.respond(ctx, request, "❌ 清理追踪失败："+err.Error())
	}
	return p.respondAfter(
		ctx,
		request,
		fmt.Sprintf("🗑️ 已清除 %d 个用户和 %d 个关键字", users, keywords),
		10*time.Second,
	)
}

func (p *Plugin) setConfig(
	ctx context.Context,
	request command.Request,
	name string,
) error {
	if len(request.Args) < 2 {
		return p.respond(ctx, request, "❌ 请使用 true 或 false")
	}
	value, err := strconv.ParseBool(strings.ToLower(request.Args[1]))
	if err != nil {
		return p.respond(ctx, request, "❌ 请使用 true 或 false")
	}
	p.mu.Lock()
	if name == "big" {
		p.state.Config.Big = value
	} else {
		p.state.Config.KeepLog = value
	}
	err = p.persistLocked(ctx)
	p.mu.Unlock()
	if err != nil {
		return p.respond(ctx, request, "❌ 保存配置失败："+err.Error())
	}
	return p.respondAfter(
		ctx,
		request,
		fmt.Sprintf("✅ %s 已设置为 %t", name, value),
		10*time.Second,
	)
}

func (p *Plugin) repliedUserID(
	ctx context.Context,
	request command.Request,
) (int64, error) {
	messages, err := p.services.Telegram.GetMessages(
		ctx,
		request.Message.ChatID,
		[]int{request.Message.ReplyToID},
	)
	if err != nil || len(messages) == 0 || messages[0].SenderID <= 0 {
		return 0, errors.New("无法获取回复消息的用户")
	}
	return messages[0].SenderID, nil
}

func (p *Plugin) persistLocked(ctx context.Context) error {
	raw, err := json.Marshal(p.state)
	if err != nil {
		return err
	}
	return p.services.Storage.Put(ctx, "trace", "state", raw)
}

func (p *Plugin) big() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state.Config.Big
}

func (p *Plugin) respond(ctx context.Context, request command.Request, text string) error {
	return p.respondAfter(ctx, request, text, 5*time.Second)
}

func (p *Plugin) respondAfter(
	ctx context.Context,
	request command.Request,
	text string,
	delay time.Duration,
) error {
	sent, err := p.sendResponse(ctx, request, text)
	if err != nil || p.keepLog() {
		return err
	}
	time.AfterFunc(delay, func() {
		deleteCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if deleteErr := p.services.Telegram.DeleteMessages(
			deleteCtx,
			sent.ChatID,
			[]int{sent.MessageID},
		); deleteErr != nil {
			p.services.Logger.Warn(
				"delete transient trace response",
				"error", deleteErr,
			)
		}
	})
	return nil
}

func (p *Plugin) respondPersistent(
	ctx context.Context,
	request command.Request,
	text string,
) error {
	_, err := p.sendResponse(ctx, request, text)
	return err
}

func (p *Plugin) sendResponse(
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

func (p *Plugin) keepLog() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state.Config.KeepLog
}

func parseReactions(text string, customIDs []int64) []telegram.Reaction {
	seenEmoji := make(map[string]struct{})
	seenCustom := make(map[int64]struct{})
	result := make([]telegram.Reaction, 0)
	remaining := text
	for _, emoji := range availableReactions {
		if strings.Contains(remaining, emoji) {
			if _, exists := seenEmoji[emoji]; !exists {
				seenEmoji[emoji] = struct{}{}
				result = append(result, telegram.Reaction{Emoji: emoji})
			}
			// Consume compound emoji before checking shorter component emoji.
			remaining = strings.ReplaceAll(remaining, emoji, "")
		}
	}
	for _, documentID := range customIDs {
		if documentID <= 0 {
			continue
		}
		if _, exists := seenCustom[documentID]; !exists {
			seenCustom[documentID] = struct{}{}
			result = append(result, telegram.Reaction{DocumentID: documentID})
		}
	}
	return result
}

func reactionDisplay(reactions []telegram.Reaction) string {
	var values []string
	for _, reaction := range reactions {
		if reaction.Emoji != "" {
			values = append(values, reaction.Emoji)
		} else {
			values = append(values, fmt.Sprintf("[Premium:%d]", reaction.DocumentID))
		}
	}
	return strings.Join(values, " ")
}

func defaultState() traceState {
	return traceState{
		Users:    make(map[string][]telegram.Reaction),
		Keywords: make(map[string][]telegram.Reaction),
		Config: traceConfig{
			KeepLog: true,
			Big:     true,
		},
	}
}

func normalizeState(state *traceState) {
	if state.Users == nil {
		state.Users = make(map[string][]telegram.Reaction)
	}
	if state.Keywords == nil {
		state.Keywords = make(map[string][]telegram.Reaction)
	}
}

func cloneState(state traceState) traceState {
	result := traceState{
		Users:    make(map[string][]telegram.Reaction, len(state.Users)),
		Keywords: make(map[string][]telegram.Reaction, len(state.Keywords)),
		Config:   state.Config,
	}
	for key, value := range state.Users {
		result.Users[key] = cloneReactions(value)
	}
	for key, value := range state.Keywords {
		result.Keywords[key] = cloneReactions(value)
	}
	return result
}

func cloneReactions(value []telegram.Reaction) []telegram.Reaction {
	return append([]telegram.Reaction(nil), value...)
}

func sortedKeys(values map[string][]telegram.Reaction) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func helpText(prefix string) string {
	return "🎯 Trace 自动回应\n\n" +
		"回复消息 + " + prefix + "trace 👍👎  追踪该用户\n" +
		"回复消息 + " + prefix + "trace  取消追踪\n" +
		prefix + "trace kw add <词> 👍\n" +
		prefix + "trace kw del <词>\n" +
		prefix + "trace status\n" +
		prefix + "trace clean\n" +
		prefix + "trace reset\n" +
		prefix + "trace log <true|false>\n" +
		prefix + "trace big <true|false>"
}

const traceGuideHTML = `<b>🎯 Reaction 自动回应</b>

<b>用户追踪</b>
回复目标用户的消息后发送 <code>{{prefix}}trace 👍👎🥰</code>，为该用户设置 reaction
回复目标用户的消息后发送 <code>{{prefix}}trace</code>，取消该用户的追踪

<b>关键词追踪</b>
<code>{{prefix}}trace kw add &lt;关键词&gt; 👍👎🥰</code>
<code>{{prefix}}trace kw del &lt;关键词&gt;</code>

<b>管理</b>
<code>{{prefix}}trace status</code> 查看用户、关键词和配置
<code>{{prefix}}trace clean</code> 清除追踪规则
<code>{{prefix}}trace reset</code> 重置全部数据
<code>{{prefix}}trace log &lt;true|false&gt;</code> 是否保留操作回执
<code>{{prefix}}trace big &lt;true|false&gt;</code> 是否发送大号 reaction 动画

标准 reaction 不要求 Premium；自定义表情 reaction 需要当前账号具备相应权限。`
