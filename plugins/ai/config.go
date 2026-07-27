package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/storage"
)

type provider string
type feature string

const (
	providerGemini     provider = "gemini"
	providerOpenAI     provider = "openai"
	providerClaude     provider = "claude"
	providerDeepSeek   provider = "deepseek"
	providerGrok       provider = "grok"
	providerThirdParty provider = "thirdparty"

	featureChat   feature = "chat"
	featureSearch feature = "search"
	featureImage  feature = "image"
	featureTTS    feature = "tts"
)

var providers = []provider{
	providerGemini,
	providerOpenAI,
	providerClaude,
	providerDeepSeek,
	providerGrok,
	providerThirdParty,
}

var defaultBaseURLs = map[provider]string{
	providerGemini:   "https://generativelanguage.googleapis.com",
	providerOpenAI:   "https://api.openai.com",
	providerClaude:   "https://api.anthropic.com",
	providerDeepSeek: "https://api.deepseek.com",
	providerGrok:     "https://api.x.ai",
}

var defaultModels = map[provider]map[feature]string{
	providerGemini: {
		featureChat:   "gemini-3.6-flash",
		featureSearch: "gemini-3.6-flash",
		featureImage:  "gemini-3.1-flash-image",
		featureTTS:    "gemini-2.5-flash-preview-tts",
	},
	providerOpenAI: {
		featureChat:   "gpt-5-mini",
		featureSearch: "gpt-4o-mini-search-preview",
		featureImage:  "gpt-image-1",
		featureTTS:    "gpt-4o-mini-tts",
	},
	providerClaude: {
		featureChat:   "claude-sonnet-5",
		featureSearch: "claude-sonnet-5",
	},
	providerDeepSeek: {
		featureChat:   "deepseek-chat",
		featureSearch: "deepseek-chat",
	},
	providerGrok: {
		featureChat:   "grok-4.3",
		featureSearch: "grok-4.3",
		featureImage:  "grok-imagine-image",
	},
}

func parseProvider(value string) (provider, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range providers {
		if value == string(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func parseFeature(value string) (feature, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "chat":
		return featureChat, true
	case "search":
		return featureSearch, true
	case "image":
		return featureImage, true
	case "tts":
		return featureTTS, true
	default:
		return "", false
	}
}

func supports(selected provider, requested feature, compat provider) bool {
	effective := selected
	if selected == providerThirdParty {
		effective = compat
	}
	switch requested {
	case featureChat:
		return true
	case featureSearch:
		return selected == providerThirdParty || effective == providerGemini
	case featureImage:
		return effective == providerGemini ||
			effective == providerOpenAI
	case featureTTS:
		return effective == providerGemini || effective == providerOpenAI
	default:
		return false
	}
}

func (p *Plugin) providerCandidates(
	ctx context.Context,
	requested feature,
) []provider {
	type scoredProvider struct {
		value provider
		score int
		order int
	}
	scores := map[provider]map[feature]int{
		providerGemini: {
			featureChat: 9, featureSearch: 10, featureImage: 8, featureTTS: 7,
		},
		providerOpenAI: {
			featureChat: 10, featureSearch: 6, featureImage: 9, featureTTS: 9,
		},
		providerClaude: {
			featureChat: 10, featureSearch: 7, featureImage: 8, featureTTS: 5,
		},
		providerDeepSeek: {
			featureChat: 8, featureSearch: 6, featureImage: 6, featureTTS: 5,
		},
		providerGrok: {
			featureChat: 7, featureSearch: 6, featureImage: 5, featureTTS: 4,
		},
		providerThirdParty: {
			featureChat: 8, featureSearch: 7, featureImage: 7, featureTTS: 7,
		},
	}
	active := p.currentProvider(ctx)
	available := make([]scoredProvider, 0, len(providers))
	for order, candidate := range providers {
		if strings.TrimSpace(p.read(ctx, "key."+string(candidate), "")) == "" {
			continue
		}
		compat := candidate
		if candidate == providerThirdParty {
			compat = p.compatProvider(ctx)
		}
		if !supports(candidate, requested, compat) {
			continue
		}
		if _, err := p.baseURL(ctx, candidate); err != nil {
			continue
		}
		if _, err := p.model(ctx, candidate, requested); err != nil {
			continue
		}
		score := scores[candidate][requested]
		if candidate == providerThirdParty {
			if compatScore := scores[compat][requested]; compatScore > 0 {
				score = compatScore
			}
		}
		available = append(available, scoredProvider{
			value: candidate,
			score: score,
			order: order,
		})
	}
	sort.SliceStable(available, func(i, j int) bool {
		if available[i].value == active {
			return true
		}
		if available[j].value == active {
			return false
		}
		if available[i].score == available[j].score {
			return available[i].order < available[j].order
		}
		return available[i].score > available[j].score
	})
	result := make([]provider, 0, len(available))
	for _, item := range available {
		result = append(result, item.value)
	}
	return result
}

func (p *Plugin) read(ctx context.Context, key, defaultValue string) string {
	value, err := p.services.Storage.Get(ctx, "ai", key)
	if err != nil {
		return defaultValue
	}
	return string(value)
}

func (p *Plugin) write(ctx context.Context, key, value string) error {
	return p.services.Storage.Put(ctx, "ai", key, []byte(value))
}

func (p *Plugin) currentProvider(ctx context.Context) provider {
	if selected, ok := parseProvider(p.read(ctx, "provider", "")); ok {
		return selected
	}
	for _, candidate := range providers {
		if p.read(ctx, "key."+string(candidate), "") != "" {
			return candidate
		}
	}
	return providerGemini
}

func (p *Plugin) compatProvider(ctx context.Context) provider {
	value, ok := parseProvider(p.read(ctx, "thirdparty.compat", "openai"))
	if !ok || value == providerThirdParty {
		return providerOpenAI
	}
	return value
}

func (p *Plugin) apiKey(ctx context.Context, selected provider) (string, error) {
	key := strings.TrimSpace(p.read(ctx, "key."+string(selected), ""))
	if key == "" {
		return "", fmt.Errorf("尚未设置 %s API Key", selected)
	}
	return key, nil
}

func (p *Plugin) baseURL(ctx context.Context, selected provider) (string, error) {
	value := strings.TrimRight(
		strings.TrimSpace(p.read(ctx, "base_url."+string(selected), "")),
		"/",
	)
	if value == "" {
		value = defaultBaseURLs[selected]
	}
	if value == "" {
		return "", fmt.Errorf("尚未设置 %s Base URL", selected)
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Hostname() == "" || parsed.User != nil {
		return "", errors.New("Base URL 必须是有效的 HTTP(S) 地址")
	}
	return value, nil
}

func (p *Plugin) model(ctx context.Context, selected provider, requested feature) (string, error) {
	value := strings.TrimSpace(p.read(
		ctx,
		"model."+string(selected)+"."+string(requested),
		"",
	))
	if value == "" {
		value = defaultModels[selected][requested]
	}
	if value == "" {
		return "", fmt.Errorf(
			"%s 未配置 %s 模型，请使用 ai model set %s <模型名>",
			selected,
			requested,
			requested,
		)
	}
	return value, nil
}

func (p *Plugin) maxTokens(ctx context.Context) int {
	value, _ := strconv.Atoi(p.read(ctx, "max_tokens", "0"))
	if value < 0 {
		return 0
	}
	if value > 1_000_000 {
		return 1_000_000
	}
	return value
}

type chatMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func historyKey(chatID int64) string {
	return fmt.Sprintf("history.%d", chatID)
}

func (p *Plugin) history(ctx context.Context, chatID int64) []chatMessage {
	var result []chatMessage
	if p.read(ctx, "context", "off") != "on" {
		return result
	}
	encoded, err := p.services.Storage.Get(ctx, "ai", historyKey(chatID))
	if err != nil {
		encoded, _ = p.services.Storage.Get(ctx, "ai", "legacy.history")
	}
	_ = json.Unmarshal(
		encoded,
		&result,
	)
	if len(result) > 40 {
		result = result[len(result)-40:]
	}
	return result
}

func (p *Plugin) appendHistory(
	ctx context.Context,
	chatID int64,
	userText string,
	assistantText string,
) error {
	if p.read(ctx, "context", "off") != "on" {
		return nil
	}
	p.configMu.Lock()
	defer p.configMu.Unlock()
	history := p.history(ctx, chatID)
	history = append(
		history,
		chatMessage{Role: "user", Text: userText},
		chatMessage{Role: "assistant", Text: assistantText},
	)
	if len(history) > 40 {
		history = history[len(history)-40:]
	}
	body, err := json.Marshal(history)
	if err != nil {
		return err
	}
	return p.write(ctx, historyKey(chatID), string(body))
}

func (p *Plugin) prompts(ctx context.Context) map[string]string {
	result := make(map[string]string)
	_ = json.Unmarshal([]byte(p.read(ctx, "prompts", "{}")), &result)
	return result
}

func (p *Plugin) savePrompts(ctx context.Context, prompts map[string]string) error {
	body, err := json.Marshal(prompts)
	if err != nil {
		return err
	}
	return p.write(ctx, "prompts", string(body))
}

func (p *Plugin) systemPrompt(ctx context.Context, requested feature) string {
	name := p.read(ctx, "prompt.active."+string(requested), "")
	if value := p.prompts(ctx)[name]; strings.TrimSpace(value) != "" {
		return value
	}
	if requested == featureSearch {
		return "默认使用中文。直接回答问题；区分事实与推断，能提供来源时附上链接。"
	}
	return "默认使用中文。回答简洁、具体；信息不足时明确说明，不编造。"
}

func validateBaseURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil &&
		(parsed.Scheme == "https" || parsed.Scheme == "http") &&
		parsed.Hostname() != "" &&
		parsed.User == nil
}

func isMissing(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}
