package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const (
	configKey       = "config"
	maxSearchResult = 100
)

type channel struct {
	Title        string `json:"title"`
	Handle       string `json:"handle"`
	ChatID       int64  `json:"chat_id"`
	LinkedChatID int64  `json:"linked_chat_id,omitempty"`
}

type config struct {
	Default   string    `json:"default"`
	Channels  []channel `json:"channels"`
	AdFilters []string  `json:"ad_filters"`
}

type candidate struct {
	Message telegram.Message
	Score   int
}

type Plugin struct {
	services service.Container
}

func New(services service.Container) *Plugin {
	return &Plugin{services: services}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "search",
		Version:     "0.3.0",
		Description: "多频道视频资源搜索、随机速览与广告过滤",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "search",
		Aliases:     []string{"so"},
		Description: "搜索已配置频道中的视频",
		Usage: []string{
			"so <关键词> [-r] [-s]",
			"so kkp [-s]",
			"so add <频道>（多个用 \\ 分隔）",
			"so del <频道|序号|all>",
			"so default <频道|序号|d>",
			"so list",
			"so export",
			"so import（回复配置文件）",
			"so ad <add|del|list> [关键词]",
		},
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	cfg, err := p.load(ctx)
	if err != nil {
		return p.respond(ctx, request, "❌ 读取搜索配置失败："+err.Error())
	}
	if len(request.Args) == 0 {
		return p.respond(ctx, request, helpText(request.Prefix))
	}

	switch strings.ToLower(request.Args[0]) {
	case "add":
		return p.add(ctx, request, &cfg)
	case "del", "delete":
		return p.remove(ctx, request, &cfg)
	case "default":
		return p.setDefault(ctx, request, &cfg)
	case "list":
		return p.list(ctx, request, cfg)
	case "export":
		return p.export(ctx, request, cfg)
	case "import":
		return p.importConfig(ctx, request, &cfg)
	case "ad":
		return p.ad(ctx, request, &cfg)
	case "kkp":
		return p.search(ctx, request, cfg, "", true, hasFlag(request.Args[1:], "-s"))
	default:
		query, random, spoiler := searchArgs(request.Args)
		if query == "" {
			return p.respond(ctx, request, "❌ 请输入搜索关键词")
		}
		return p.search(ctx, request, cfg, query, random, spoiler)
	}
}

func (p *Plugin) add(ctx context.Context, request command.Request, cfg *config) error {
	if len(request.Args) < 2 {
		return p.respond(ctx, request, "用法："+request.Prefix+"so add <频道链接或 @用户名>，多个源用 \\ 分隔")
	}
	targets := splitTargets(strings.Join(request.Args[1:], " "))
	if len(targets) == 0 {
		return p.respond(ctx, request, "❌ 未提供有效频道")
	}
	added := make([]string, 0, len(targets))
	var failures []string
	for _, target := range targets {
		if slices.ContainsFunc(cfg.Channels, func(item channel) bool {
			return strings.EqualFold(item.Handle, target)
		}) {
			continue
		}
		chat, err := p.services.Telegram.ResolveChatTarget(ctx, target)
		if err != nil {
			failures = append(failures, target+"："+err.Error())
			continue
		}
		cfg.Channels = append(cfg.Channels, channel{
			Title:        chat.Title,
			Handle:       target,
			ChatID:       chat.ID,
			LinkedChatID: chat.LinkedChatID,
		})
		added = append(added, chat.Title)
		if cfg.Default == "" {
			cfg.Default = target
		}
	}
	if len(added) > 0 {
		if err := p.save(ctx, *cfg); err != nil {
			return p.respond(ctx, request, "❌ 保存频道配置失败："+err.Error())
		}
	}
	var result strings.Builder
	fmt.Fprintf(&result, "✅ 已添加 %d 个搜索源", len(added))
	if len(added) > 0 {
		result.WriteString("：\n- " + strings.Join(added, "\n- "))
	}
	if len(failures) > 0 {
		result.WriteString("\n\n⚠️ 添加失败：\n- " + strings.Join(failures, "\n- "))
	}
	return p.respond(ctx, request, result.String())
}

func (p *Plugin) remove(ctx context.Context, request command.Request, cfg *config) error {
	if len(request.Args) < 2 {
		return p.respond(ctx, request, "用法："+request.Prefix+"so del <频道|序号|all>")
	}
	if strings.EqualFold(request.Args[1], "all") {
		count := len(cfg.Channels)
		cfg.Channels = nil
		cfg.Default = ""
		if err := p.save(ctx, *cfg); err != nil {
			return p.respond(ctx, request, "❌ 清空失败："+err.Error())
		}
		return p.respond(ctx, request, fmt.Sprintf("✅ 已清空 %d 个搜索源", count))
	}

	remove := make(map[string]struct{})
	for _, value := range splitDeleteTargets(request.Args[1:]) {
		index, err := strconv.Atoi(value)
		if err == nil && index > 0 && index <= len(cfg.Channels) {
			remove[cfg.Channels[index-1].Handle] = struct{}{}
			continue
		}
		remove[value] = struct{}{}
	}
	var removed []string
	cfg.Channels = slices.DeleteFunc(cfg.Channels, func(item channel) bool {
		for handle := range remove {
			if strings.EqualFold(handle, item.Handle) {
				removed = append(removed, item.Title)
				if strings.EqualFold(cfg.Default, item.Handle) {
					cfg.Default = ""
				}
				return true
			}
		}
		return false
	})
	if cfg.Default == "" && len(cfg.Channels) > 0 {
		cfg.Default = cfg.Channels[0].Handle
	}
	if err := p.save(ctx, *cfg); err != nil {
		return p.respond(ctx, request, "❌ 保存频道配置失败："+err.Error())
	}
	if len(removed) == 0 {
		return p.respond(ctx, request, "❓ 未找到指定搜索源")
	}
	return p.respond(ctx, request, "✅ 已移除：\n- "+strings.Join(removed, "\n- "))
}

func (p *Plugin) setDefault(ctx context.Context, request command.Request, cfg *config) error {
	if len(request.Args) != 2 {
		return p.respond(ctx, request, "用法："+request.Prefix+"so default <频道|序号|d>")
	}
	value := request.Args[1]
	if strings.EqualFold(value, "d") {
		cfg.Default = ""
		if err := p.save(ctx, *cfg); err != nil {
			return p.respond(ctx, request, "❌ 保存失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已移除默认搜索源")
	}
	if index, err := strconv.Atoi(value); err == nil && index > 0 && index <= len(cfg.Channels) {
		value = cfg.Channels[index-1].Handle
	}
	for _, item := range cfg.Channels {
		if strings.EqualFold(item.Handle, value) {
			cfg.Default = item.Handle
			if err := p.save(ctx, *cfg); err != nil {
				return p.respond(ctx, request, "❌ 保存失败："+err.Error())
			}
			return p.respond(ctx, request, "✅ 默认搜索源已设为 "+item.Title)
		}
	}
	return p.respond(ctx, request, "❌ 请先添加该频道")
}

func (p *Plugin) list(ctx context.Context, request command.Request, cfg config) error {
	if len(cfg.Channels) == 0 {
		return p.respond(ctx, request, "尚未添加搜索源")
	}
	var result strings.Builder
	result.WriteString("🔍 当前搜索源\n\n")
	for index, item := range cfg.Channels {
		marker := ""
		if strings.EqualFold(item.Handle, cfg.Default) {
			marker = "（默认）"
		}
		fmt.Fprintf(&result, "%d. %s %s%s\n", index+1, item.Title, item.Handle, marker)
	}
	return p.respond(ctx, request, strings.TrimSpace(result.String()))
}

func (p *Plugin) export(ctx context.Context, request command.Request, cfg config) error {
	if len(cfg.Channels) == 0 {
		return p.respond(ctx, request, "没有可导出的搜索源")
	}
	directory, err := os.MkdirTemp("", "telebox-search-export-*")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建导出目录失败："+err.Error())
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "so_channels_backup.txt")
	var handles []string
	for _, item := range cfg.Channels {
		handles = append(handles, item.Handle)
	}
	if err := os.WriteFile(path, []byte(strings.Join(handles, "\n")+"\n"), 0o600); err != nil {
		return p.respond(ctx, request, "❌ 写入导出文件失败："+err.Error())
	}
	_, err = p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:      path,
		FileName:  filepath.Base(path),
		MIMEType:  "text/plain",
		Caption:   "✅ 搜索频道配置已导出",
		ReplyToID: request.Message.ID,
		Kind:      telegram.MediaDocument,
	})
	return err
}

func (p *Plugin) importConfig(
	ctx context.Context,
	request command.Request,
	cfg *config,
) error {
	if request.Message.ReplyToID == 0 {
		return p.respond(ctx, request, "❌ 请回复由 search 导出的配置文件")
	}
	var data bytes.Buffer
	if _, err := p.services.Telegram.DownloadMedia(
		ctx,
		request.Message.ChatID,
		request.Message.ReplyToID,
		&limitedWriter{Writer: &data, Remaining: 256 * 1024},
	); err != nil {
		return p.respond(ctx, request, "❌ 下载配置文件失败："+err.Error())
	}
	targets := splitImportLines(data.String())
	if len(targets) == 0 {
		return p.respond(ctx, request, "❌ 配置文件中没有有效频道")
	}
	next := defaultConfig()
	var failures []string
	for _, target := range targets {
		chat, err := p.services.Telegram.ResolveChatTarget(ctx, target)
		if err != nil {
			failures = append(failures, target)
			continue
		}
		next.Channels = append(next.Channels, channel{
			Title:        chat.Title,
			Handle:       target,
			ChatID:       chat.ID,
			LinkedChatID: chat.LinkedChatID,
		})
		if next.Default == "" {
			next.Default = target
		}
	}
	if len(next.Channels) == 0 {
		return p.respond(ctx, request, "❌ 没有可访问的频道，原配置未更改")
	}
	*cfg = next
	if err := p.save(ctx, *cfg); err != nil {
		return p.respond(ctx, request, "❌ 保存导入配置失败："+err.Error())
	}
	result := fmt.Sprintf("✅ 已导入 %d 个搜索源", len(next.Channels))
	if len(failures) > 0 {
		result += fmt.Sprintf("，跳过 %d 个不可访问的源", len(failures))
	}
	return p.respond(ctx, request, result)
}

func (p *Plugin) ad(ctx context.Context, request command.Request, cfg *config) error {
	if len(request.Args) < 2 {
		return p.respond(ctx, request, "用法："+request.Prefix+"so ad <add|del|list> [关键词]")
	}
	switch strings.ToLower(request.Args[1]) {
	case "list":
		if len(cfg.AdFilters) == 0 {
			return p.respond(ctx, request, "当前没有广告过滤词")
		}
		return p.respond(ctx, request, "当前广告过滤词：\n"+strings.Join(cfg.AdFilters, "、"))
	case "add":
		if len(request.Args) < 3 {
			return p.respond(ctx, request, "❌ 请提供要添加的关键词")
		}
		added := 0
		for _, value := range request.Args[2:] {
			if !slices.Contains(cfg.AdFilters, value) {
				cfg.AdFilters = append(cfg.AdFilters, value)
				added++
			}
		}
		if err := p.save(ctx, *cfg); err != nil {
			return p.respond(ctx, request, "❌ 保存失败："+err.Error())
		}
		return p.respond(ctx, request, fmt.Sprintf("✅ 已添加 %d 个广告过滤词", added))
	case "del":
		if len(request.Args) < 3 {
			return p.respond(ctx, request, "❌ 请提供要删除的关键词")
		}
		before := len(cfg.AdFilters)
		cfg.AdFilters = slices.DeleteFunc(cfg.AdFilters, func(value string) bool {
			return slices.Contains(request.Args[2:], value)
		})
		if err := p.save(ctx, *cfg); err != nil {
			return p.respond(ctx, request, "❌ 保存失败："+err.Error())
		}
		return p.respond(ctx, request, fmt.Sprintf("✅ 已删除 %d 个广告过滤词", before-len(cfg.AdFilters)))
	default:
		return p.respond(ctx, request, "用法："+request.Prefix+"so ad <add|del|list> [关键词]")
	}
}

func (p *Plugin) search(
	ctx context.Context,
	request command.Request,
	cfg config,
	query string,
	random bool,
	spoiler bool,
) error {
	if len(cfg.Channels) == 0 {
		return p.respond(ctx, request, "❌ 请先使用 "+request.Prefix+"so add 添加搜索源")
	}
	status := "⏳ 搜索视频…"
	if query == "" {
		status = "⏳ 随机查找视频…"
	}
	if err := p.respond(ctx, request, status); err != nil {
		return err
	}

	order := orderedChannels(cfg)
	var candidates []candidate
	for index, source := range order {
		if index > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
		messages, err := p.searchSource(ctx, source, query, query == "")
		if err != nil {
			p.services.Logger.Warn("search source failed",
				"source", source.Handle,
				"error", err,
			)
			continue
		}
		for _, message := range messages {
			if query == "" {
				if !isUsableVideo(message, query, cfg.AdFilters, true) {
					continue
				}
			} else if !isVideo(message) || isAd(message, cfg.AdFilters) {
				continue
			}
			candidates = append(candidates, candidate{
				Message: message,
				Score:   relevance(message, query),
			})
		}
		if query != "" && !random && len(candidates) > 0 {
			break
		}
	}
	candidates = deduplicate(candidates)
	if len(candidates) == 0 {
		return p.respond(ctx, request, "❌ 未找到匹配的视频")
	}
	selected := candidates[0]
	if random || query == "" {
		selected = candidates[rand.IntN(len(candidates))]
	} else {
		slices.SortFunc(candidates, func(a, b candidate) int {
			switch {
			case a.Score > b.Score:
				return -1
			case a.Score < b.Score:
				return 1
			default:
				return b.Message.ID - a.Message.ID
			}
		})
		selected = candidates[0]
	}
	if err := p.respond(ctx, request, "⏳ 发送结果…"); err != nil {
		return err
	}
	if spoiler {
		return p.sendSpoiler(ctx, request, selected.Message, query)
	}
	if err := p.services.Telegram.ForwardMessages(
		ctx,
		selected.Message.ChatID,
		request.Message.ChatID,
		[]int{selected.Message.ID},
	); err != nil {
		if copyErr := p.services.Telegram.CopyMessages(
			ctx,
			selected.Message.ChatID,
			request.Message.ChatID,
			[]int{selected.Message.ID},
		); copyErr != nil {
			return p.respond(ctx, request, "❌ 发送搜索结果失败："+errors.Join(err, copyErr).Error())
		}
	}
	return p.deleteCommand(ctx, request)
}

func (p *Plugin) searchSource(
	ctx context.Context,
	source channel,
	query string,
	randomPreview bool,
) ([]telegram.Message, error) {
	if randomPreview {
		return p.services.Telegram.GetHistory(ctx, telegram.HistoryQuery{
			ChatID:    source.ChatID,
			Limit:     maxSearchResult,
			MediaKind: telegram.MediaVideo,
		})
	}
	result := make([]telegram.Message, 0)
	processedGroups := make(map[int64]struct{})
	if source.LinkedChatID != 0 {
		linked, err := p.searchLinkedDiscussion(ctx, source.LinkedChatID, query)
		if err != nil {
			p.services.Logger.Warn(
				"search linked discussion failed",
				"source", source.Handle,
				"error", err,
			)
		} else {
			result = append(result, linked...)
		}
	}
	found, err := p.services.Telegram.GetHistory(ctx, telegram.HistoryQuery{
		ChatID: source.ChatID,
		Limit:  maxSearchResult,
		Search: query,
	})
	if err != nil {
		return result, err
	}
	for _, message := range found {
		if !fuzzyMatch(message.Text+" "+mediaFileName(message), query) {
			continue
		}
		if message.GroupedID != 0 {
			if _, exists := processedGroups[message.GroupedID]; exists {
				continue
			}
			processedGroups[message.GroupedID] = struct{}{}
			surrounding, historyErr := p.services.Telegram.GetHistory(
				ctx,
				telegram.HistoryQuery{
					ChatID:   source.ChatID,
					Limit:    20,
					OffsetID: message.ID + 10,
				},
			)
			if historyErr == nil {
				for _, item := range surrounding {
					if item.GroupedID == message.GroupedID && isVideo(item) {
						result = append(result, item)
					}
				}
			}
			continue
		}
		if isVideo(message) {
			result = append(result, message)
		}
	}
	return result, nil
}

func (p *Plugin) searchLinkedDiscussion(
	ctx context.Context,
	chatID int64,
	query string,
) ([]telegram.Message, error) {
	textMessages, err := p.services.Telegram.GetHistory(ctx, telegram.HistoryQuery{
		ChatID: chatID,
		Limit:  maxSearchResult,
		Search: query,
	})
	if err != nil {
		return nil, err
	}
	var result []telegram.Message
	checked := 0
	for _, message := range textMessages {
		if !fuzzyMatch(message.Text+" "+mediaFileName(message), query) {
			continue
		}
		if checked >= 20 {
			break
		}
		checked++
		replies, replyErr := p.services.Telegram.GetHistory(ctx, telegram.HistoryQuery{
			ChatID:    chatID,
			Limit:     maxSearchResult,
			ReplyToID: message.ID,
		})
		if replyErr != nil {
			continue
		}
		for _, reply := range replies {
			if isVideo(reply) {
				result = append(result, reply)
			}
		}
		if len(result) > 0 {
			return result, nil
		}
	}
	if len(result) == 0 {
		videos, videoErr := p.services.Telegram.GetHistory(ctx, telegram.HistoryQuery{
			ChatID:    chatID,
			Limit:     maxSearchResult,
			Search:    query,
			MediaKind: telegram.MediaVideo,
		})
		if videoErr == nil {
			result = append(result, videos...)
		}
	}
	return result, nil
}

func isVideo(message telegram.Message) bool {
	return message.Media != nil &&
		(message.Media.Kind == telegram.MediaVideo ||
			message.Media.Kind == telegram.MediaAnimation)
}

func mediaFileName(message telegram.Message) string {
	if message.Media == nil {
		return ""
	}
	return message.Media.FileName
}

func (p *Plugin) sendSpoiler(
	ctx context.Context,
	request command.Request,
	message telegram.Message,
	query string,
) error {
	directory, err := os.MkdirTemp("", "telebox-search-video-*")
	if err != nil {
		return p.respond(ctx, request, "❌ 创建临时目录失败："+err.Error())
	}
	defer os.RemoveAll(directory)
	fileName := safeFileName(message.Media.FileName)
	if fileName == "" {
		fileName = "search-result.mp4"
	}
	path := filepath.Join(directory, fileName)
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return p.respond(ctx, request, "❌ 创建临时文件失败："+err.Error())
	}
	media, downloadErr := p.services.Telegram.DownloadMedia(
		ctx,
		message.ChatID,
		message.ID,
		output,
	)
	closeErr := output.Close()
	if downloadErr != nil {
		return p.respond(ctx, request, "❌ 下载搜索结果失败："+downloadErr.Error())
	}
	if closeErr != nil {
		return p.respond(ctx, request, "❌ 保存搜索结果失败："+closeErr.Error())
	}
	caption := query
	if caption == "" {
		caption = message.Text
	}
	_, err = p.services.Telegram.SendFile(ctx, request.Message.ChatID, telegram.Upload{
		Path:      path,
		FileName:  fileName,
		MIMEType:  media.MIMEType,
		Caption:   caption,
		ReplyToID: request.Message.ID,
		Kind:      telegram.MediaVideo,
		Width:     media.Width,
		Height:    media.Height,
		Duration:  media.Duration,
		Spoiler:   true,
	})
	if err != nil {
		return p.respond(ctx, request, "❌ 上传搜索结果失败："+err.Error())
	}
	return p.deleteCommand(ctx, request)
}

func (p *Plugin) load(ctx context.Context) (config, error) {
	cfg := defaultConfig()
	data, err := p.services.Storage.Get(ctx, "search", configKey)
	if err != nil {
		return cfg, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, err
	}
	if cfg.AdFilters == nil {
		cfg.AdFilters = append([]string(nil), defaultAdFilters...)
	}
	return cfg, nil
}

func (p *Plugin) save(ctx context.Context, cfg config) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return p.services.Storage.Put(ctx, "search", configKey, data)
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

func defaultConfig() config {
	return config{AdFilters: append([]string(nil), defaultAdFilters...)}
}

func orderedChannels(cfg config) []channel {
	result := append([]channel(nil), cfg.Channels...)
	if cfg.Default == "" {
		return result
	}
	slices.SortStableFunc(result, func(a, b channel) int {
		switch {
		case strings.EqualFold(a.Handle, cfg.Default):
			return -1
		case strings.EqualFold(b.Handle, cfg.Default):
			return 1
		default:
			return 0
		}
	})
	return result
}

func searchArgs(args []string) (query string, random bool, spoiler bool) {
	values := make([]string, 0, len(args))
	for _, value := range args {
		switch strings.ToLower(value) {
		case "-r":
			random = true
		case "-s":
			spoiler = true
		default:
			values = append(values, value)
		}
	}
	return strings.TrimSpace(strings.Join(values, " ")), random, spoiler
}

func hasFlag(args []string, wanted string) bool {
	return slices.ContainsFunc(args, func(value string) bool {
		return strings.EqualFold(value, wanted)
	})
}

func splitTargets(value string) []string {
	parts := strings.FieldsFunc(value, func(char rune) bool {
		return char == '\\' || char == '\n' || char == '\r'
	})
	return uniqueNonEmpty(parts)
}

func splitDeleteTargets(values []string) []string {
	return splitTargets(strings.Join(values, "\\"))
}

func splitImportLines(value string) []string {
	return uniqueNonEmpty(strings.FieldsFunc(value, func(char rune) bool {
		return unicode.IsSpace(char) || char == '\\'
	}))
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func isUsableVideo(
	message telegram.Message,
	query string,
	adFilters []string,
	randomPreview bool,
) bool {
	if message.Media == nil || (message.Media.Kind != telegram.MediaVideo &&
		message.Media.Kind != telegram.MediaAnimation) {
		return false
	}
	if isAd(message, adFilters) {
		return false
	}
	if randomPreview {
		return message.Media.Duration >= 20*time.Second &&
			message.Media.Duration <= 3*time.Minute
	}
	return fuzzyMatch(message.Text+" "+message.Media.FileName, query)
}

func isAd(message telegram.Message, filters []string) bool {
	content := strings.ToLower(message.Text + " " + message.Media.FileName)
	return slices.ContainsFunc(filters, func(filter string) bool {
		return filter != "" && strings.Contains(content, strings.ToLower(filter))
	})
}

func relevance(message telegram.Message, query string) int {
	normalizedQuery := normalize(query)
	score := 0
	if strings.Contains(normalize(message.Media.FileName), normalizedQuery) {
		score += 100
	}
	if strings.Contains(normalize(message.Text), normalizedQuery) {
		score += 50
	}
	return score
}

func fuzzyMatch(text, query string) bool {
	text = normalize(text)
	query = normalize(query)
	if text == "" || query == "" {
		return false
	}
	if strings.Contains(text, query) {
		return true
	}
	queryParts := strings.Fields(query)
	textParts := strings.Fields(text)
	for _, wanted := range queryParts {
		if !slices.ContainsFunc(textParts, func(part string) bool {
			return strings.Contains(part, wanted)
		}) {
			return false
		}
	}
	return true
}

func normalize(value string) string {
	value = strings.ToLower(value)
	value = strings.Map(func(char rune) rune {
		switch char {
		case '-', '_', '.', '|', '\\', '/', '#':
			return ' '
		default:
			return char
		}
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func deduplicate(values []candidate) []candidate {
	seen := make(map[string]struct{}, len(values))
	result := make([]candidate, 0, len(values))
	for _, value := range values {
		key := fmt.Sprintf("%d/%d", value.Message.ChatID, value.Message.ID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func safeFileName(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "\x00", "")
	if value == "." || value == string(filepath.Separator) {
		return ""
	}
	return value
}

func helpText(prefix string) string {
	return "🔍 多频道视频搜索\n\n" +
		prefix + "so <关键词> [-r] [-s]\n" +
		prefix + "so kkp [-s]\n" +
		prefix + "so add <频道>（多个用 \\ 分隔）\n" +
		prefix + "so del <频道|序号|all>\n" +
		prefix + "so default <频道|序号|d>\n" +
		prefix + "so list\n" +
		prefix + "so export / import（回复配置文件）\n" +
		prefix + "so ad <add|del|list> [关键词]\n\n" +
		"-r 随机选择，-s 以防剧透媒体重新上传"
}

type limitedWriter struct {
	io.Writer
	Remaining int64
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.Remaining {
		return 0, errors.New("配置文件超过 256 KiB")
	}
	count, err := w.Writer.Write(data)
	w.Remaining -= int64(count)
	return count, err
}

var defaultAdFilters = strings.Fields(`
	广告 推广 赞助 合作 代理 招商 加盟 投资 理财 贷款 借钱 网贷 信用卡
	pos机 刷单 兼职 副业 微商 代购 淘宝 拼多多 京东 直播带货 优惠券
	返利 红包 现金 提现 充值 游戏币 点卡 彩票 博彩 赌博 六和彩 时时彩
	北京赛车 股票 期货 外汇 数字货币 比特币 挖矿 保险 医疗 整容 减肥
	丰胸 壮阳 药品 假货 高仿 A货 精仿 原单 尾单 办证 刻章 发票 学历
	文凭 证书 黑客 破解 外挂 木马 病毒 盗号 vpn 翻墙 代理ip 科学上网 梯子
`)
