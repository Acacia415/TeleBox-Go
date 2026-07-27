package cezi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/legacyconfig"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
)

const (
	defaultModel    = "llama-3.3-70b-versatile"
	defaultGroqBase = "https://api.groq.com/openai"
	apiKeyName      = "api_key"
	modelName       = "model"
	baseURLName     = "base_url"
)

const fortunePrompt = `汝乃“玄机子”，精测字、通易数、善拆解。回答须以文言为主，古雅玄妙，
总长约150至200字。严格使用以下分段：
【起卦】按字形、笔画起卦，述体用卦象。
【字解】运用拆字、加减笔、谐音、会意或象形。
【断曰】给出押韵或对仗的古风判词；若有凶兆须附化解。
【锦囊】从诗经、楚辞、唐宋诗词、论孟、老庄、周易、禅语或民谚中择一句，
不可机械重复“天行健”。不要解释任务，不要添加免责声明。`

var excludedChars = makeSet(
	"你我他她它您咱俺谁啥的了着过地得是在有和与或但而就也都还又再" +
		"不没无非别莫这那哪某各每些吗呢吧啊哦哈嗯呀噢嘛喔把被让给向从到" +
		"为以因用个只条张次下点一二三四五六七八九十百千万两几多少年月日" +
		"时分秒天周上下左右前后里外中内大小好坏对错能会可要想该应去来走" +
		"跑看说做吃喝睡",
)

type Plugin struct {
	services service.Container
}

func New(services service.Container) *Plugin {
	return &Plugin{services: services}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "cezi",
		Version:     "0.3.0",
		Description: "使用 Groq 生产模型进行测字解签",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "cezi",
		Description: "输入汉字或从回复消息随机抽字测字",
		Usage: []string{
			"cezi <汉字>",
			"cezi（回复消息，随机抽字）",
			"cezi apikey <Groq API Key>|clear",
			"cezi model <模型名>",
			"cezi baseurl <地址|clear>",
		},
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(ctx context.Context) error {
	return p.migrateLegacyConfig(ctx)
}

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if len(request.Args) > 0 {
		switch strings.ToLower(request.Args[0]) {
		case "help", "h":
			return p.respond(ctx, request, helpText(request.Prefix))
		case "apikey":
			return p.configureAPIKey(ctx, request)
		case "model":
			return p.configureModel(ctx, request)
		case "baseurl":
			return p.configureBaseURL(ctx, request)
		}
	}

	character, source, err := p.selectCharacter(ctx, request)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	if character == "" {
		return p.respond(ctx, request, helpText(request.Prefix))
	}
	sourceLabel := ""
	if source == "reply" {
		sourceLabel = "（从回复中随机抽取）"
	}
	if err := p.respond(
		ctx,
		request,
		fmt.Sprintf("⏳ 解字「%s」%s…", character, sourceLabel),
	); err != nil {
		return err
	}
	fortune, err := p.callGroq(ctx, character)
	if err != nil {
		p.services.Logger.Warn("cezi Groq request failed", "error", err)
		return p.respond(ctx, request, "🔮 测字算命\n\n❌ "+err.Error())
	}
	return p.respond(
		ctx,
		request,
		fmt.Sprintf("🔮 测字算命・%s%s\n\n%s", character, sourceLabel, cleanText(fortune)),
	)
}

func (p *Plugin) configureAPIKey(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		key, _ := p.readConfig(ctx, apiKeyName)
		if key == "" {
			return p.respond(ctx, request, "❌ 未设置 Groq API Key")
		}
		suffix := key
		if len(suffix) > 4 {
			suffix = suffix[len(suffix)-4:]
		}
		return p.respond(ctx, request, "🔑 当前 API Key：…"+suffix)
	}
	value := strings.TrimSpace(strings.Join(request.Args[1:], " "))
	if strings.EqualFold(value, "clear") {
		if err := p.services.Storage.Delete(ctx, "cezi", apiKeyName); err != nil {
			return p.respond(ctx, request, "❌ 清除 API Key 失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ Groq API Key 已清除")
	}
	if value == "" {
		return p.respond(ctx, request, "❌ API Key 不能为空")
	}
	if err := p.writeConfig(ctx, apiKeyName, value); err != nil {
		return p.respond(ctx, request, "❌ 保存 API Key 失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ Groq API Key 已保存")
}

func (p *Plugin) configureModel(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		model, _ := p.readConfig(ctx, modelName)
		if model == "" {
			model = defaultModel
		}
		return p.respond(ctx, request, "🤖 当前模型："+model)
	}
	model := strings.TrimSpace(request.Args[1])
	if model == "" || strings.ContainsAny(model, " \t\r\n") {
		return p.respond(ctx, request, "❌ 模型名称无效")
	}
	if err := p.writeConfig(ctx, modelName, model); err != nil {
		return p.respond(ctx, request, "❌ 保存模型失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 模型已设置："+model)
}

func (p *Plugin) configureBaseURL(ctx context.Context, request command.Request) error {
	if len(request.Args) == 1 {
		value, _ := p.readConfig(ctx, baseURLName)
		if value == "" {
			value = defaultGroqBase
		}
		return p.respond(ctx, request, "🌐 当前 Groq Base URL："+value)
	}
	value := strings.TrimRight(strings.TrimSpace(request.Args[1]), "/")
	if strings.EqualFold(value, "clear") {
		if err := p.services.Storage.Delete(ctx, "cezi", baseURLName); err != nil &&
			!errors.Is(err, storage.ErrNotFound) {
			return p.respond(ctx, request, "❌ 恢复默认地址失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已恢复 Groq 默认地址")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" {
		return p.respond(ctx, request, "❌ Base URL 必须是有效的 HTTP(S) 地址")
	}
	if err := p.writeConfig(ctx, baseURLName, value); err != nil {
		return p.respond(ctx, request, "❌ 保存 Base URL 失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ Groq Base URL 已保存")
}

func (p *Plugin) selectCharacter(
	ctx context.Context,
	request command.Request,
) (string, string, error) {
	if direct := firstChinese(strings.Join(request.Args, " ")); direct != "" {
		return direct, "input", nil
	}
	if request.Message.ReplyToID <= 0 {
		return "", "", nil
	}
	messages, err := p.services.Telegram.GetMessages(
		ctx,
		request.Message.ChatID,
		[]int{request.Message.ReplyToID},
	)
	if err != nil {
		return "", "", fmt.Errorf("读取回复消息失败：%w", err)
	}
	if len(messages) == 0 {
		return "", "", errors.New("未找到回复消息")
	}
	character, err := randomMeaningfulChinese(messages[0].Text)
	if err != nil {
		return "", "", err
	}
	return character, "reply", nil
}

func (p *Plugin) callGroq(ctx context.Context, character string) (string, error) {
	apiKey, err := p.readConfig(ctx, apiKeyName)
	if err != nil || apiKey == "" {
		return "", errors.New("未设置 Groq API Key，请使用 cezi apikey <密钥> 设置")
	}
	model, _ := p.readConfig(ctx, modelName)
	if model == "" {
		model = defaultModel
	}
	baseURL, _ := p.readConfig(ctx, baseURLName)
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = defaultGroqBase
	}
	body, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": fortunePrompt},
			{"role": "user", "content": fmt.Sprintf("请为我测这个字：“%s”", character)},
		},
		"max_tokens":  500,
		"temperature": 0.9,
	})
	if err != nil {
		return "", err
	}
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		Method: http.MethodPost,
		URL:    baseURL + "/v1/chat/completions",
		Headers: http.Header{
			"Authorization": []string{"Bearer " + apiKey},
			"Content-Type":  []string{"application/json"},
		},
		Body: body,
	})
	if err != nil {
		return "", fmt.Errorf("网络请求失败：%w", err)
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		return "", fmt.Errorf("解析 Groq 响应失败：%w", err)
	}
	if response.StatusCode != http.StatusOK {
		if payload.Error.Message != "" {
			return "", errors.New(payload.Error.Message)
		}
		return "", fmt.Errorf("Groq HTTP %d", response.StatusCode)
	}
	if len(payload.Choices) == 0 || strings.TrimSpace(payload.Choices[0].Message.Content) == "" {
		return "", errors.New("Groq 未返回测字内容")
	}
	return payload.Choices[0].Message.Content, nil
}

func (p *Plugin) readConfig(ctx context.Context, key string) (string, error) {
	value, err := p.services.Storage.Get(ctx, "cezi", key)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func (p *Plugin) writeConfig(ctx context.Context, key, value string) error {
	return p.services.Storage.Put(ctx, "cezi", key, []byte(value))
}

func (p *Plugin) migrateLegacyConfig(ctx context.Context) error {
	if p.services.AssetsDir == "" {
		return nil
	}
	values, err := legacyconfig.ReadSQLiteConfig(
		filepath.Join(p.services.AssetsDir, "cezi_config.db"),
	)
	if err != nil {
		return err
	}
	mapping := map[string]string{
		"cezi_api_key":  apiKeyName,
		"cezi_model":    modelName,
		"cezi_base_url": baseURLName,
	}
	imported := 0
	for oldKey, newKey := range mapping {
		value := strings.TrimSpace(values[oldKey])
		if value == "" {
			continue
		}
		if _, err := p.services.Storage.Get(ctx, "cezi", newKey); err == nil {
			continue
		} else if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if err := p.writeConfig(ctx, newKey, value); err != nil {
			return err
		}
		imported++
	}
	if imported > 0 {
		p.services.Logger.Info("migrated legacy cezi config", "keys", imported)
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

func firstChinese(text string) string {
	for _, value := range text {
		if isChinese(value) {
			return string(value)
		}
	}
	return ""
}

func randomMeaningfulChinese(text string) (string, error) {
	var candidates []rune
	for _, value := range text {
		if isChinese(value) {
			if _, excluded := excludedChars[value]; !excluded {
				candidates = append(candidates, value)
			}
		}
	}
	if len(candidates) == 0 {
		return "", errors.New("回复消息中没有可用于测字的汉字")
	}
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
	if err != nil {
		return "", fmt.Errorf("生成随机数失败：%w", err)
	}
	return string(candidates[index.Int64()]), nil
}

func isChinese(value rune) bool {
	return unicode.In(value, unicode.Han)
}

func cleanText(text string) string {
	text = strings.TrimPrefix(text, "\uFEFF")
	return strings.TrimSpace(strings.Map(func(value rune) rune {
		if value == '\n' || value == '\t' || value >= ' ' {
			return value
		}
		return -1
	}, text))
}

func makeSet(values string) map[rune]struct{} {
	result := make(map[rune]struct{}, len([]rune(values)))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func helpText(prefix string) string {
	return "🔮 测字算命\n\n用法：\n" +
		prefix + "cezi <汉字>\n" +
		"回复一条消息后使用 " + prefix + "cezi，可随机抽取有意义汉字\n\n配置：\n" +
		prefix + "cezi apikey <Groq API Key>\n" +
		prefix + "cezi apikey clear\n" +
		prefix + "cezi model <模型名>\n" +
		prefix + "cezi baseurl <地址|clear>\n\n默认模型：" + defaultModel
}
