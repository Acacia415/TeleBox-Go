package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
)

func (p *Plugin) handleSettings(ctx context.Context, request command.Request) error {
	selected := p.currentProvider(ctx)
	compat := p.compatProvider(ctx)
	baseURL, _ := p.baseURL(ctx, selected)
	var result strings.Builder
	result.WriteString("⚙️ 模型配置\n\n")
	result.WriteString("当前服务商：" + string(selected) + "\n")
	if selected == providerThirdParty {
		result.WriteString("兼容协议：" + string(compat) + "\n")
	}
	result.WriteString("Base URL：" + censorURL(baseURL) + "\n")
	result.WriteString("API Key：" + maskSecret(p.read(ctx, "key."+string(selected), "")) + "\n")
	for _, requested := range []feature{
		featureChat,
		featureSearch,
		featureImage,
		featureTTS,
	} {
		model, _ := p.model(ctx, selected, requested)
		if model == "" {
			model = "不支持/未配置"
		}
		result.WriteString(string(requested) + "：" + model + "\n")
	}
	result.WriteString("TTS 音色：" +
		p.read(ctx, "tts_voice", defaultVoice(compat)) + "\n")
	result.WriteString("上下文：" + p.read(ctx, "context", "off") + "\n")
	result.WriteString("最大 Token：" + p.read(ctx, "max_tokens", "0") + "\n")
	result.WriteString("折叠引用：" + p.read(ctx, "collapse", "off") + "\n")
	result.WriteString("Telegraph：" + p.read(ctx, "telegraph", "off"))
	return p.finishText(ctx, request, result.String(), 0)
}

func (p *Plugin) handleStatus(ctx context.Context, request command.Request) error {
	selected := p.currentProvider(ctx)
	var result strings.Builder
	result.WriteString("📊 模型服务状态\n\n")
	for _, candidate := range providers {
		marker := "○"
		if p.read(ctx, "key."+string(candidate), "") != "" {
			marker = "●"
		}
		active := ""
		if candidate == selected {
			active = "（当前）"
		}
		result.WriteString(fmt.Sprintf(
			"%s %-10s %s%s\n",
			marker,
			candidate,
			map[bool]string{true: "已配置", false: "未配置"}[p.read(ctx, "key."+string(candidate), "") != ""],
			active,
		))
	}
	return p.respond(ctx, request, result.String())
}

func (p *Plugin) handleSelect(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) != 1 {
		return p.respond(ctx, request, "❌ 用法：ai select <服务商>")
	}
	selected, ok := parseProvider(args[0])
	if !ok {
		return p.respond(ctx, request,
			"❌ 支持：gemini、openai、claude、deepseek、grok、thirdparty")
	}
	if p.read(ctx, "key."+string(selected), "") == "" {
		return p.respond(ctx, request,
			"❌ 尚未设置 "+string(selected)+" API Key")
	}
	if err := p.write(ctx, "provider", string(selected)); err != nil {
		return p.respond(ctx, request, "❌ 保存服务商失败："+err.Error())
	}
	detail := p.applyDefaultModels(ctx, selected, false)
	return p.respond(ctx, request,
		"✅ 已切换到 "+string(selected)+"\n\n"+detail)
}

func (p *Plugin) handleAPIKey(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) < 2 {
		return p.respond(ctx, request,
			"❌ 用法：ai apikey <服务商> <密钥|clear>")
	}
	selected, ok := parseProvider(args[0])
	if !ok {
		return p.respond(ctx, request, "❌ 不支持的服务商")
	}
	key := strings.TrimSpace(strings.Join(args[1:], " "))
	if strings.EqualFold(key, "clear") {
		if err := p.services.Storage.Delete(
			ctx,
			"ai",
			"key."+string(selected),
		); err != nil && !isMissing(err) {
			return p.respond(ctx, request, "❌ 清除 API Key 失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已清除 "+string(selected)+" API Key")
	}
	if len(key) < 8 || strings.ContainsAny(key, " \t\r\n") {
		return p.respond(ctx, request, "❌ API Key 格式无效")
	}
	if err := p.write(ctx, "key."+string(selected), key); err != nil {
		return p.respond(ctx, request, "❌ 保存 API Key 失败："+err.Error())
	}
	if p.read(ctx, "provider", "") == "" {
		_ = p.write(ctx, "provider", string(selected))
	}
	return p.respond(ctx, request,
		"✅ 已保存 "+string(selected)+" API Key："+maskSecret(key))
}

func (p *Plugin) handleBaseURL(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) != 2 {
		return p.respond(ctx, request,
			"❌ 用法：ai baseurl <服务商> <URL|clear>")
	}
	selected, ok := parseProvider(args[0])
	if !ok {
		return p.respond(ctx, request, "❌ 不支持的服务商")
	}
	key := "base_url." + string(selected)
	value := strings.TrimRight(strings.TrimSpace(args[1]), "/")
	if strings.EqualFold(value, "clear") {
		if err := p.services.Storage.Delete(ctx, "ai", key); err != nil && !isMissing(err) {
			return p.respond(ctx, request, "❌ 清除 Base URL 失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已恢复服务商默认 Base URL")
	}
	if !validateBaseURL(value) {
		return p.respond(ctx, request, "❌ Base URL 必须是有效的 HTTP(S) 地址")
	}
	if err := p.write(ctx, key, value); err != nil {
		return p.respond(ctx, request, "❌ 保存 Base URL 失败："+err.Error())
	}
	return p.respond(ctx, request,
		"✅ 已保存 "+string(selected)+" Base URL："+censorURL(value))
}

func (p *Plugin) handleThirdParty(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) != 2 || !strings.EqualFold(args[0], "compat") {
		return p.respond(ctx, request,
			"❌ 用法：ai thirdparty compat <gemini|openai|claude|deepseek|grok>")
	}
	compat, ok := parseProvider(args[1])
	if !ok || compat == providerThirdParty {
		return p.respond(ctx, request, "❌ 不支持的第三方兼容协议")
	}
	if err := p.write(ctx, "thirdparty.compat", string(compat)); err != nil {
		return p.respond(ctx, request, "❌ 保存兼容协议失败："+err.Error())
	}
	return p.respond(ctx, request,
		"✅ 第三方接口兼容协议已设为 "+string(compat))
}

func (p *Plugin) handleModel(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) == 0 {
		return p.respond(ctx, request,
			"❌ 用法：ai model <list|set|auto>")
	}
	selected := p.currentProvider(ctx)
	switch strings.ToLower(args[0]) {
	case "list":
		var result strings.Builder
		result.WriteString("🧠 " + string(selected) + " 模型配置\n\n")
		for _, requested := range []feature{
			featureChat,
			featureSearch,
			featureImage,
			featureTTS,
		} {
			model, _ := p.model(ctx, selected, requested)
			if model == "" {
				model = "未配置"
			}
			result.WriteString(string(requested) + "：" + model + "\n")
		}
		return p.respond(ctx, request, result.String())
	case "set":
		if len(args) < 3 {
			return p.respond(ctx, request,
				"❌ 用法：ai model set <chat|search|image|tts> <模型名>")
		}
		requested, ok := parseFeature(args[1])
		if !ok {
			return p.respond(ctx, request, "❌ 模型类型无效")
		}
		model := strings.TrimSpace(strings.Join(args[2:], " "))
		if model == "" || strings.ContainsAny(model, "\r\n") {
			return p.respond(ctx, request, "❌ 模型名称无效")
		}
		if err := p.write(
			ctx,
			"model."+string(selected)+"."+string(requested),
			model,
		); err != nil {
			return p.respond(ctx, request, "❌ 保存模型失败："+err.Error())
		}
		return p.respond(ctx, request,
			"✅ "+string(requested)+" 模型已设为 "+model)
	case "auto", "automatch":
		if selected != providerThirdParty {
			return p.respond(ctx, request, p.applyDefaultModels(ctx, selected, true))
		}
		if err := p.respond(ctx, request, "⏳ 读取第三方模型列表…"); err != nil {
			return err
		}
		models, err := p.fetchModels(ctx, selected)
		if err != nil {
			return p.respond(ctx, request, "❌ 自动匹配失败："+friendlyError(err))
		}
		assigned := autoAssignModels(models)
		if len(assigned) == 0 {
			return p.respond(ctx, request, "❌ 未找到可识别的模型")
		}
		var lines []string
		for requested, model := range assigned {
			if err := p.write(
				ctx,
				"model."+string(selected)+"."+string(requested),
				model,
			); err != nil {
				return p.respond(ctx, request, "❌ 保存模型失败："+err.Error())
			}
			lines = append(lines, string(requested)+"："+model)
		}
		sort.Strings(lines)
		return p.respond(ctx, request,
			"✅ 已自动匹配第三方模型\n\n"+strings.Join(lines, "\n"))
	default:
		return p.respond(ctx, request,
			"❌ 用法：ai model <list|set|auto>")
	}
}

func (p *Plugin) handleModelShortcut(
	ctx context.Context,
	request command.Request,
	commandName string,
	model string,
) error {
	requested, _ := parseFeature(strings.TrimSuffix(commandName, "model"))
	model = strings.TrimSpace(model)
	if model == "" {
		current, err := p.model(ctx, p.currentProvider(ctx), requested)
		if err != nil {
			return p.respond(ctx, request, "❌ "+err.Error())
		}
		return p.respond(ctx, request, string(requested)+" 模型："+current)
	}
	return p.handleModel(ctx, request, []string{"set", string(requested), model})
}

func (p *Plugin) handleVoice(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	protocol := p.currentProvider(ctx)
	if protocol == providerThirdParty {
		protocol = p.compatProvider(ctx)
	}
	if len(args) == 0 {
		return p.respond(ctx, request,
			"TTS 音色："+p.read(ctx, "tts_voice", defaultVoice(protocol)))
	}
	if strings.EqualFold(args[0], "list") {
		if protocol == providerGemini {
			return p.respond(ctx, request,
				"Gemini TTS 音色：\nAchernar, Achird, Algenib, Algieba, Alnilam, "+
					"Aoede, Autonoe, Callirrhoe, Charon, Despina, Enceladus, Erinome, "+
					"Fenrir, Gacrux, Iapetus, Kore, Laomedeia, Leda, Orus, Puck, "+
					"Pulcherrima, Rasalgethi, Sadachbia, Sadaltager, Schedar, Sulafat, "+
					"Umbriel, Vindemiatrix, Zephyr, Zubenelgenubi")
		}
		return p.respond(ctx, request,
			"OpenAI 兼容 TTS 音色：alloy, ash, ballad, coral, echo, fable, "+
				"nova, onyx, sage, shimmer, verse")
	}
	voice := strings.TrimSpace(strings.Join(args, " "))
	if strings.ContainsAny(voice, " \t\r\n") || len(voice) > 80 {
		return p.respond(ctx, request, "❌ 音色名称无效")
	}
	if err := p.write(ctx, "tts_voice", voice); err != nil {
		return p.respond(ctx, request, "❌ 保存音色失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ TTS 音色已设为 "+voice)
}

func (p *Plugin) handleMaxTokens(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) == 0 {
		return p.respond(ctx, request,
			"最大输出 Token："+p.read(ctx, "max_tokens", "0"))
	}
	value, err := strconv.Atoi(args[0])
	if err != nil || value < 0 || value > 1_000_000 {
		return p.respond(ctx, request, "❌ Token 必须是 0 到 1000000 的整数")
	}
	if err := p.write(ctx, "max_tokens", strconv.Itoa(value)); err != nil {
		return p.respond(ctx, request, "❌ 保存 Token 设置失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ 最大输出 Token 已设为 "+strconv.Itoa(value))
}

func (p *Plugin) handleContext(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) == 0 {
		return p.respond(ctx, request,
			"上下文记忆："+p.read(ctx, "context", "off"))
	}
	switch strings.ToLower(args[0]) {
	case "on", "off":
		if err := p.write(ctx, "context", strings.ToLower(args[0])); err != nil {
			return p.respond(ctx, request, "❌ 保存上下文设置失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 上下文记忆已"+map[bool]string{
			true:  "开启",
			false: "关闭",
		}[strings.EqualFold(args[0], "on")])
	case "clear":
		err := p.services.Storage.Delete(
			ctx,
			"ai",
			historyKey(request.Message.ChatID),
		)
		if err != nil && !isMissing(err) {
			return p.respond(ctx, request, "❌ 清除上下文失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 当前聊天的对话上下文已清除")
	case "show":
		history := p.history(ctx, request.Message.ChatID)
		if len(history) == 0 {
			return p.respond(ctx, request, "当前聊天没有对话上下文")
		}
		var result strings.Builder
		result.WriteString("🧠 当前聊天上下文\n\n")
		for _, message := range history {
			label := "用户"
			if message.Role == "assistant" {
				label = "AI"
			}
			result.WriteString(label + "：" + truncateRunes(message.Text, 500) + "\n\n")
		}
		return p.finishText(ctx, request, result.String(), 0)
	default:
		return p.respond(ctx, request,
			"❌ 用法：ai context <on|off|show|clear>")
	}
}

func (p *Plugin) handlePrompt(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) == 0 {
		return p.respond(ctx, request,
			"❌ 用法：ai prompt <add|del|list|set|show>")
	}
	p.configMu.Lock()
	defer p.configMu.Unlock()
	prompts := p.prompts(ctx)
	switch strings.ToLower(args[0]) {
	case "add":
		if len(args) < 3 {
			return p.respond(ctx, request,
				"❌ 用法：ai prompt add <名称> <内容>")
		}
		name := args[1]
		content := strings.TrimSpace(strings.Join(args[2:], " "))
		if !validPromptName(name) || content == "" || len([]rune(content)) > 10000 {
			return p.respond(ctx, request, "❌ 提示词名称或内容无效")
		}
		prompts[name] = content
		if err := p.savePrompts(ctx, prompts); err != nil {
			return p.respond(ctx, request, "❌ 保存提示词失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已保存系统提示词 "+name)
	case "del":
		if len(args) != 2 {
			return p.respond(ctx, request, "❌ 用法：ai prompt del <名称>")
		}
		if _, exists := prompts[args[1]]; !exists {
			return p.respond(ctx, request, "❌ 提示词不存在")
		}
		delete(prompts, args[1])
		if err := p.savePrompts(ctx, prompts); err != nil {
			return p.respond(ctx, request, "❌ 删除提示词失败："+err.Error())
		}
		return p.respond(ctx, request, "✅ 已删除系统提示词 "+args[1])
	case "list":
		if len(prompts) == 0 {
			return p.respond(ctx, request, "尚未保存系统提示词")
		}
		names := make([]string, 0, len(prompts))
		for name := range prompts {
			names = append(names, name)
		}
		sort.Strings(names)
		return p.respond(ctx, request, "系统提示词：\n"+strings.Join(names, "\n"))
	case "set":
		if len(args) != 3 {
			return p.respond(ctx, request,
				"❌ 用法：ai prompt set <chat|search|tts> <名称>")
		}
		requested, ok := parseFeature(args[1])
		if !ok || (requested != featureChat &&
			requested != featureSearch &&
			requested != featureTTS) {
			return p.respond(ctx, request, "❌ 提示词类型无效")
		}
		if _, exists := prompts[args[2]]; !exists {
			return p.respond(ctx, request, "❌ 提示词不存在")
		}
		if err := p.write(
			ctx,
			"prompt.active."+string(requested),
			args[2],
		); err != nil {
			return p.respond(ctx, request, "❌ 设置提示词失败："+err.Error())
		}
		return p.respond(ctx, request,
			"✅ "+string(requested)+" 已使用提示词 "+args[2])
	case "show":
		if len(args) != 2 {
			return p.respond(ctx, request, "❌ 用法：ai prompt show <名称>")
		}
		content, exists := prompts[args[1]]
		if !exists {
			return p.respond(ctx, request, "❌ 提示词不存在")
		}
		return p.finishText(
			ctx,
			request,
			"系统提示词 "+args[1]+"：\n\n"+content,
			0,
		)
	default:
		return p.respond(ctx, request,
			"❌ 用法：ai prompt <add|del|list|set|show>")
	}
}

func (p *Plugin) handleToggle(
	ctx context.Context,
	request command.Request,
	key string,
	args []string,
) error {
	if len(args) == 0 {
		return p.respond(ctx, request, key+"："+p.read(ctx, key, "off"))
	}
	value := strings.ToLower(args[0])
	if value != "on" && value != "off" {
		return p.respond(ctx, request, "❌ 值必须是 on 或 off")
	}
	if err := p.write(ctx, key, value); err != nil {
		return p.respond(ctx, request, "❌ 保存设置失败："+err.Error())
	}
	return p.respond(ctx, request, "✅ "+key+" 已设为 "+value)
}

func (p *Plugin) handleTelegraph(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) == 0 {
		return p.respond(ctx, request,
			"Telegraph："+p.read(ctx, "telegraph", "off")+
				"\n长回答在 Go 版本中会自动安全分段发送。")
	}
	if strings.EqualFold(args[0], "limit") {
		if len(args) != 2 {
			return p.respond(ctx, request, "❌ 用法：ai telegraph limit <字符数>")
		}
		limit, err := strconv.Atoi(args[1])
		if err != nil || limit < 0 || limit > 100000 {
			return p.respond(ctx, request, "❌ 字符数必须是 0 到 100000")
		}
		if err := p.write(ctx, "telegraph.limit", strconv.Itoa(limit)); err != nil {
			return p.respond(ctx, request, "❌ 保存限制失败："+err.Error())
		}
		return p.respond(ctx, request,
			"✅ Telegraph 字符限制已保存；Go 版本仍优先使用消息分段")
	}
	if strings.EqualFold(args[0], "list") {
		return p.respond(ctx, request,
			"Go 版本不在 Telegraph 保存访问令牌或页面清单；长回答自动分段发送。")
	}
	if strings.EqualFold(args[0], "del") {
		return p.respond(ctx, request, "当前没有本地 Telegraph 页面记录")
	}
	return p.handleToggle(ctx, request, "telegraph", args)
}

func (p *Plugin) handleConfig(
	ctx context.Context,
	request command.Request,
	args []string,
) error {
	if len(args) != 1 || !strings.EqualFold(args[0], "default") {
		return p.respond(ctx, request, "❌ 用法：ai config default")
	}
	keys := []string{
		"provider",
		"thirdparty.compat",
		"tts_voice",
		"max_tokens",
		"context",
		"collapse",
		"telegraph",
		"telegraph.limit",
		"prompts",
		"prompt.active.chat",
		"prompt.active.search",
		"prompt.active.tts",
		historyKey(request.Message.ChatID),
	}
	for _, candidate := range providers {
		keys = append(
			keys,
			"key."+string(candidate),
			"base_url."+string(candidate),
		)
		for _, requested := range []feature{
			featureChat,
			featureSearch,
			featureImage,
			featureTTS,
		} {
			keys = append(
				keys,
				"model."+string(candidate)+"."+string(requested),
			)
		}
	}
	for _, key := range keys {
		if err := p.services.Storage.Delete(ctx, "ai", key); err != nil && !isMissing(err) {
			return p.respond(ctx, request, "❌ 重置配置失败："+err.Error())
		}
	}
	return p.respond(ctx, request, "✅ 模型配置已恢复默认")
}

func (p *Plugin) applyDefaultModels(
	ctx context.Context,
	selected provider,
	force bool,
) string {
	models := defaultModels[selected]
	if len(models) == 0 {
		return "第三方服务商需使用 ai model set 或 ai model auto 配置模型。"
	}
	var updated []string
	for requested, model := range models {
		key := "model." + string(selected) + "." + string(requested)
		if !force && p.read(ctx, key, "") != "" {
			continue
		}
		if err := p.write(ctx, key, model); err == nil {
			updated = append(updated, string(requested)+"："+model)
		}
	}
	sort.Strings(updated)
	if len(updated) == 0 {
		return "已保留现有模型配置。"
	}
	return "已匹配模型：\n" + strings.Join(updated, "\n")
}

func (p *Plugin) fetchModels(
	ctx context.Context,
	selected provider,
) ([]string, error) {
	cfgProtocol := selected
	if selected == providerThirdParty {
		cfgProtocol = p.compatProvider(ctx)
	}
	key, err := p.apiKey(ctx, selected)
	if err != nil {
		return nil, err
	}
	baseURL, err := p.baseURL(ctx, selected)
	if err != nil {
		return nil, err
	}
	endpoint := baseURL + "/v1/models"
	headers := bearerHeaders(key)
	if cfgProtocol == providerGemini {
		endpoint = baseURL + "/v1beta/models"
		headers = geminiHeaders(key)
	}
	response, err := p.services.HTTP.Do(ctx, httpclientRequest(
		http.MethodGet,
		endpoint,
		headers,
		nil,
	))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, apiError(response.StatusCode, response.Body)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		return nil, fmt.Errorf("解析模型列表：%w", err)
	}
	var result []string
	for _, item := range payload.Data {
		if item.ID != "" {
			result = append(result, item.ID)
		}
	}
	for _, item := range payload.Models {
		name := strings.TrimPrefix(item.Name, "models/")
		if name != "" {
			result = append(result, name)
		}
	}
	return result, nil
}

func autoAssignModels(models []string) map[feature]string {
	result := make(map[feature]string)
	for _, model := range models {
		lower := strings.ToLower(model)
		if result[featureTTS] == "" &&
			(strings.Contains(lower, "tts") || strings.Contains(lower, "speech")) {
			result[featureTTS] = model
		}
		if result[featureImage] == "" &&
			(strings.Contains(lower, "image") ||
				strings.Contains(lower, "dall-e") ||
				strings.Contains(lower, "imagen")) {
			result[featureImage] = model
		}
		if result[featureChat] == "" &&
			!strings.Contains(lower, "embedding") &&
			!strings.Contains(lower, "moderation") &&
			!strings.Contains(lower, "tts") &&
			!strings.Contains(lower, "speech") &&
			!strings.Contains(lower, "image") {
			result[featureChat] = model
			result[featureSearch] = model
		}
	}
	return result
}

func httpclientRequest(
	method string,
	endpoint string,
	headers http.Header,
	body []byte,
) httpclient.Request {
	return httpclient.Request{
		Method: method, URL: endpoint, Headers: headers, Body: body,
	}
}

func validPromptName(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" &&
		len([]rune(value)) <= 64 &&
		!strings.ContainsAny(value, " \t\r\n")
}

func maskSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "未设置"
	}
	runes := []rune(value)
	if len(runes) <= 8 {
		return "••••••••"
	}
	return string(runes[:4]) + "…" + string(runes[len(runes)-4:])
}

func censorURL(value string) string {
	if value == "" {
		return "默认"
	}
	index := strings.Index(value, "://")
	if index < 0 {
		return value
	}
	return value[:index+3] + "***"
}
