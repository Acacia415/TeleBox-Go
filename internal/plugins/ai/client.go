package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
)

type imageInput struct {
	MIME string
	Data []byte
}

type generationResult struct {
	Text string
	Data []byte
	MIME string
	URL  string
}

type requestConfig struct {
	Selected provider
	Protocol provider
	BaseURL  string
	APIKey   string
	Model    string
	Feature  feature
}

func (p *Plugin) requestConfig(
	ctx context.Context,
	requested feature,
) (requestConfig, error) {
	selected := p.currentProvider(ctx)
	protocol := selected
	if selected == providerThirdParty {
		protocol = p.compatProvider(ctx)
	}
	if !supports(selected, requested, protocol) {
		return requestConfig{}, fmt.Errorf("%s 不支持 %s 功能", selected, requested)
	}
	key, err := p.apiKey(ctx, selected)
	if err != nil {
		return requestConfig{}, err
	}
	baseURL, err := p.baseURL(ctx, selected)
	if err != nil {
		return requestConfig{}, err
	}
	model, err := p.model(ctx, selected, requested)
	if err != nil {
		return requestConfig{}, err
	}
	return requestConfig{
		Selected: selected,
		Protocol: protocol,
		BaseURL:  baseURL,
		APIKey:   key,
		Model:    model,
		Feature:  requested,
	}, nil
}

func (p *Plugin) generateText(
	ctx context.Context,
	requested feature,
	prompt string,
	history []chatMessage,
	image *imageInput,
) (generationResult, requestConfig, error) {
	cfg, err := p.requestConfig(ctx, requested)
	if err != nil {
		return generationResult{}, cfg, err
	}
	system := p.systemPrompt(ctx, requested)
	switch cfg.Protocol {
	case providerGemini:
		result, err := p.geminiText(ctx, cfg, system, prompt, history, image)
		return result, cfg, err
	case providerClaude:
		result, err := p.claudeText(ctx, cfg, system, prompt, history, image)
		return result, cfg, err
	default:
		result, err := p.openAIText(ctx, cfg, system, prompt, history, image)
		return result, cfg, err
	}
}

func (p *Plugin) generateImage(
	ctx context.Context,
	prompt string,
) (generationResult, requestConfig, error) {
	cfg, err := p.requestConfig(ctx, featureImage)
	if err != nil {
		return generationResult{}, cfg, err
	}
	if cfg.Protocol == providerGemini {
		result, err := p.geminiMedia(ctx, cfg, prompt, false, "")
		return result, cfg, err
	}
	result, err := p.openAIImage(ctx, cfg, prompt)
	return result, cfg, err
}

func (p *Plugin) generateSpeech(
	ctx context.Context,
	text string,
) (generationResult, requestConfig, error) {
	cfg, err := p.requestConfig(ctx, featureTTS)
	if err != nil {
		return generationResult{}, cfg, err
	}
	voice := p.read(ctx, "tts_voice", defaultVoice(cfg.Protocol))
	if cfg.Protocol == providerGemini {
		result, err := p.geminiMedia(ctx, cfg, text, true, voice)
		return result, cfg, err
	}
	result, err := p.openAISpeech(ctx, cfg, text, voice)
	return result, cfg, err
}

func (p *Plugin) geminiText(
	ctx context.Context,
	cfg requestConfig,
	system string,
	prompt string,
	history []chatMessage,
	image *imageInput,
) (generationResult, error) {
	contents := make([]map[string]any, 0, len(history)+1)
	for _, message := range history {
		role := message.Role
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role": role,
			"parts": []map[string]any{{
				"text": message.Text,
			}},
		})
	}
	parts := []map[string]any{{"text": prompt}}
	if image != nil {
		parts = append(parts, map[string]any{
			"inlineData": map[string]string{
				"mimeType": image.MIME,
				"data":     base64.StdEncoding.EncodeToString(image.Data),
			},
		})
	}
	contents = append(contents, map[string]any{"role": "user", "parts": parts})
	body := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{"text": system}},
		},
		"contents": contents,
	}
	if maxTokens := p.maxTokens(ctx); maxTokens > 0 {
		body["generationConfig"] = map[string]any{
			"maxOutputTokens": maxTokens,
		}
	}
	if cfg.Feature == featureSearch {
		body["tools"] = []map[string]any{{"googleSearch": map[string]any{}}}
	}
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := p.doJSON(
		ctx,
		http.MethodPost,
		cfg.BaseURL+"/v1beta/models/"+url.PathEscape(cfg.Model)+":generateContent",
		geminiHeaders(cfg.APIKey),
		body,
		&response,
	); err != nil {
		return generationResult{}, err
	}
	var text strings.Builder
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			text.WriteString(part.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return generationResult{}, errors.New("Gemini 未返回文本")
	}
	return generationResult{Text: strings.TrimSpace(text.String())}, nil
}

func (p *Plugin) openAIText(
	ctx context.Context,
	cfg requestConfig,
	system string,
	prompt string,
	history []chatMessage,
	image *imageInput,
) (generationResult, error) {
	messages := []map[string]any{{"role": "system", "content": system}}
	for _, message := range history {
		messages = append(messages, map[string]any{
			"role":    message.Role,
			"content": message.Text,
		})
	}
	var content any = prompt
	if image != nil {
		content = []map[string]any{
			{"type": "text", "text": prompt},
			{
				"type": "image_url",
				"image_url": map[string]string{
					"url": "data:" + image.MIME + ";base64," +
						base64.StdEncoding.EncodeToString(image.Data),
				},
			},
		}
	}
	messages = append(messages, map[string]any{"role": "user", "content": content})
	body := map[string]any{
		"model":    cfg.Model,
		"messages": messages,
		"stream":   false,
	}
	if maxTokens := p.maxTokens(ctx); maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := p.doJSON(
		ctx,
		http.MethodPost,
		cfg.BaseURL+"/v1/chat/completions",
		bearerHeaders(cfg.APIKey),
		body,
		&response,
	); err != nil {
		return generationResult{}, err
	}
	if len(response.Choices) == 0 {
		return generationResult{}, errors.New("接口未返回聊天结果")
	}
	text := contentText(response.Choices[0].Message.Content)
	if text == "" {
		return generationResult{}, errors.New("接口返回了空文本")
	}
	return generationResult{Text: text}, nil
}

func (p *Plugin) claudeText(
	ctx context.Context,
	cfg requestConfig,
	system string,
	prompt string,
	history []chatMessage,
	image *imageInput,
) (generationResult, error) {
	messages := make([]map[string]any, 0, len(history)+1)
	for _, message := range history {
		messages = append(messages, map[string]any{
			"role":    message.Role,
			"content": message.Text,
		})
	}
	var content any = prompt
	if image != nil {
		content = []map[string]any{
			{
				"type": "image",
				"source": map[string]string{
					"type":       "base64",
					"media_type": image.MIME,
					"data":       base64.StdEncoding.EncodeToString(image.Data),
				},
			},
			{"type": "text", "text": prompt},
		}
	}
	messages = append(messages, map[string]any{"role": "user", "content": content})
	maxTokens := p.maxTokens(ctx)
	if maxTokens == 0 {
		maxTokens = 4096
	}
	body := map[string]any{
		"model":      cfg.Model,
		"system":     system,
		"messages":   messages,
		"max_tokens": maxTokens,
	}
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	headers := http.Header{
		"Content-Type":      []string{"application/json"},
		"x-api-key":         []string{cfg.APIKey},
		"anthropic-version": []string{"2023-06-01"},
	}
	if err := p.doJSON(
		ctx,
		http.MethodPost,
		cfg.BaseURL+"/v1/messages",
		headers,
		body,
		&response,
	); err != nil {
		return generationResult{}, err
	}
	var text strings.Builder
	for _, item := range response.Content {
		if item.Type == "text" {
			text.WriteString(item.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return generationResult{}, errors.New("Claude 未返回文本")
	}
	return generationResult{Text: strings.TrimSpace(text.String())}, nil
}

func (p *Plugin) openAIImage(
	ctx context.Context,
	cfg requestConfig,
	prompt string,
) (generationResult, error) {
	body := map[string]any{
		"model":  cfg.Model,
		"prompt": prompt,
		"n":      1,
		"size":   "1024x1024",
	}
	var response struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := p.doJSON(
		ctx,
		http.MethodPost,
		cfg.BaseURL+"/v1/images/generations",
		bearerHeaders(cfg.APIKey),
		body,
		&response,
	); err != nil {
		return generationResult{}, err
	}
	if len(response.Data) == 0 {
		return generationResult{}, errors.New("图片接口未返回结果")
	}
	if response.Data[0].B64JSON != "" {
		data, err := base64.StdEncoding.DecodeString(response.Data[0].B64JSON)
		if err != nil {
			return generationResult{}, fmt.Errorf("解析图片数据：%w", err)
		}
		return generationResult{Data: data, MIME: "image/png"}, nil
	}
	if response.Data[0].URL == "" {
		return generationResult{}, errors.New("图片接口未返回 URL 或图片数据")
	}
	return generationResult{URL: response.Data[0].URL, MIME: "image/png"}, nil
}

func (p *Plugin) openAISpeech(
	ctx context.Context,
	cfg requestConfig,
	text string,
	voice string,
) (generationResult, error) {
	body, err := json.Marshal(map[string]any{
		"model":           cfg.Model,
		"input":           text,
		"voice":           voice,
		"response_format": "mp3",
	})
	if err != nil {
		return generationResult{}, err
	}
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		Method:  http.MethodPost,
		URL:     cfg.BaseURL + "/v1/audio/speech",
		Headers: bearerHeaders(cfg.APIKey),
		Body:    body,
	})
	if err != nil {
		return generationResult{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return generationResult{}, apiError(response.StatusCode, response.Body)
	}
	if len(response.Body) == 0 {
		return generationResult{}, errors.New("语音接口返回了空文件")
	}
	return generationResult{
		Data: response.Body,
		MIME: firstNonEmpty(response.Headers.Get("Content-Type"), "audio/mpeg"),
	}, nil
}

func (p *Plugin) geminiMedia(
	ctx context.Context,
	cfg requestConfig,
	text string,
	speech bool,
	voice string,
) (generationResult, error) {
	generationConfig := map[string]any{}
	if speech {
		generationConfig["responseModalities"] = []string{"AUDIO"}
		generationConfig["speechConfig"] = map[string]any{
			"voiceConfig": map[string]any{
				"prebuiltVoiceConfig": map[string]string{"voiceName": voice},
			},
		}
	} else {
		generationConfig["responseModalities"] = []string{"TEXT", "IMAGE"}
	}
	body := map[string]any{
		"contents": []map[string]any{{
			"role":  "user",
			"parts": []map[string]string{{"text": text}},
		}},
		"generationConfig": generationConfig,
	}
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text       string `json:"text"`
					InlineData *struct {
						MIME string `json:"mimeType"`
						Data string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := p.doJSON(
		ctx,
		http.MethodPost,
		cfg.BaseURL+"/v1beta/models/"+url.PathEscape(cfg.Model)+":generateContent",
		geminiHeaders(cfg.APIKey),
		body,
		&response,
	); err != nil {
		return generationResult{}, err
	}
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil || part.InlineData.Data == "" {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
			if err != nil {
				return generationResult{}, fmt.Errorf("解析 Gemini 媒体：%w", err)
			}
			return generationResult{
				Data: data,
				MIME: part.InlineData.MIME,
				Text: part.Text,
			}, nil
		}
	}
	return generationResult{}, errors.New("Gemini 未返回媒体数据")
}

func (p *Plugin) downloadGenerated(
	ctx context.Context,
	rawURL string,
) ([]byte, string, error) {
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		Method: http.MethodGet,
		URL:    rawURL,
	})
	if err != nil {
		return nil, "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", apiError(response.StatusCode, response.Body)
	}
	return response.Body, response.Headers.Get("Content-Type"), nil
}

func (p *Plugin) doJSON(
	ctx context.Context,
	method string,
	endpoint string,
	headers http.Header,
	body any,
	target any,
) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		Method:  method,
		URL:     endpoint,
		Headers: headers,
		Body:    encoded,
	})
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return apiError(response.StatusCode, response.Body)
	}
	if err := json.Unmarshal(response.Body, target); err != nil {
		return fmt.Errorf("解析 AI 接口响应：%w", err)
	}
	return nil
}

func bearerHeaders(apiKey string) http.Header {
	return http.Header{
		"Authorization": []string{"Bearer " + apiKey},
		"Content-Type":  []string{"application/json"},
	}
}

func geminiHeaders(apiKey string) http.Header {
	return http.Header{
		"x-goog-api-key": []string{apiKey},
		"Content-Type":   []string{"application/json"},
	}
}

func apiError(status int, body []byte) error {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &payload)
	message := firstNonEmpty(payload.Error.Message, payload.Message, http.StatusText(status))
	if len([]rune(message)) > 500 {
		message = string([]rune(message)[:500]) + "…"
	}
	return fmt.Errorf("AI API HTTP %d：%s", status, message)
}

func contentText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		var result strings.Builder
		for _, item := range typed {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := entry["text"].(string); ok {
				result.WriteString(text)
			}
		}
		return strings.TrimSpace(result.String())
	default:
		return ""
	}
}

func defaultVoice(protocol provider) string {
	if protocol == providerGemini {
		return "Kore"
	}
	return "alloy"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
