package inkstone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/service"
)

const (
	mcpProtocolVersion = "2025-06-18"
	mcpClientVersion   = "0.2.5"
)

type mcpClient struct {
	http     service.HTTPClient
	endpoint string
	apiKey   string
	nextID   atomic.Int64
}

type mcpRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type mcpEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	IsError           bool            `json:"isError,omitempty"`
	Content           []mcpContent    `json:"content,omitempty"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
}

type mcpToolError struct {
	Code    string
	Message string
	Status  int
}

func (e *mcpToolError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "Inkstone 未完成请求"
}

type fetchedNote struct {
	ID        string
	Title     string
	Text      string
	Rev       int
	Truncated bool
}

type editedNote struct {
	ID    string
	Title string
	Rev   int
}

type noteSearchResult struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type editRequest struct {
	OperationID string
	NoteID      string
	ExpectedRev int
	Operation   string
	Text        string
	OldText     string
}

func newMCPClient(httpClient service.HTTPClient, endpoint, apiKey string) (*mcpClient, error) {
	if httpClient == nil {
		return nil, errors.New("HTTP 服务不可用")
	}
	normalized, err := normalizeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	apiKey = strings.TrimSpace(apiKey)
	if !strings.HasPrefix(apiKey, "ink_") || len(apiKey) < 40 || len(apiKey) > 128 {
		return nil, errors.New("INKSTONE_API_KEY 未配置或格式不正确")
	}
	return &mcpClient{
		http:     httpClient,
		endpoint: normalized,
		apiKey:   apiKey,
	}, nil
}

func normalizeEndpoint(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("INKSTONE_MCP_URL 未配置")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("INKSTONE_MCP_URL 必须是有效的 HTTP(S) 地址")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("INKSTONE_MCP_URL 不能包含账号、查询参数或锚点")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/mcp"
	}
	return parsed.String(), nil
}

func (c *mcpClient) fetchNote(ctx context.Context, noteID string) (fetchedNote, error) {
	structured, err := c.callTool(ctx, "fetch", map[string]any{"id": noteID})
	if err != nil {
		return fetchedNote{}, err
	}
	var payload struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Text     string `json:"text"`
		Metadata struct {
			Rev       int  `json:"rev"`
			Truncated bool `json:"truncated"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(structured, &payload); err != nil {
		return fetchedNote{}, fmt.Errorf("解析 Inkstone 笔记失败：%w", err)
	}
	if payload.ID == "" || payload.Metadata.Rev < 1 {
		return fetchedNote{}, errors.New("Inkstone 返回的笔记信息不完整")
	}
	return fetchedNote{
		ID:        payload.ID,
		Title:     payload.Title,
		Text:      payload.Text,
		Rev:       payload.Metadata.Rev,
		Truncated: payload.Metadata.Truncated,
	}, nil
}

func (c *mcpClient) searchNotes(
	ctx context.Context,
	query string,
) ([]noteSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("请输入笔记标题或关键词")
	}
	structured, err := c.callTool(ctx, "search", map[string]any{
		"query": query,
		"mode":  "auto",
	})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Results []noteSearchResult `json:"results"`
	}
	if err := json.Unmarshal(structured, &payload); err != nil {
		return nil, fmt.Errorf("解析 Inkstone 搜索结果失败：%w", err)
	}
	results := make([]noteSearchResult, 0, len(payload.Results))
	seen := make(map[string]struct{}, len(payload.Results))
	for _, item := range payload.Results {
		item.ID = strings.ToLower(strings.TrimSpace(item.ID))
		item.Title = strings.TrimSpace(item.Title)
		if !noteIDPattern.MatchString(item.ID) || item.Title == "" {
			continue
		}
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		results = append(results, item)
		if len(results) == 10 {
			break
		}
	}
	return results, nil
}

func (c *mcpClient) readFullNote(
	ctx context.Context,
	note fetchedNote,
	maxChars int,
) (string, int, error) {
	if !note.Truncated {
		return note.Text, note.Rev, nil
	}
	if maxChars < 1 {
		maxChars = 512_000
	}
	var content strings.Builder
	characters := 0
	cursor := ""
	rev := note.Rev
	for {
		arguments := map[string]any{
			"note_id":   note.ID,
			"max_chars": 40_000,
		}
		if cursor != "" {
			arguments["cursor"] = cursor
		}
		structured, err := c.callTool(ctx, "read_note", arguments)
		if err != nil {
			return "", 0, err
		}
		var payload struct {
			Data struct {
				Rev        int    `json:"rev"`
				Content    string `json:"content"`
				HasMore    bool   `json:"has_more"`
				NextCursor string `json:"next_cursor"`
			} `json:"data"`
		}
		if err := json.Unmarshal(structured, &payload); err != nil {
			return "", 0, fmt.Errorf("解析 Inkstone 正文失败：%w", err)
		}
		if payload.Data.Rev < 1 {
			return "", 0, errors.New("Inkstone 返回的正文信息不完整")
		}
		if rev != payload.Data.Rev {
			return "", 0, errors.New("读取期间笔记已被修改，请重新执行命令")
		}
		characters += utf8.RuneCountInString(payload.Data.Content)
		if characters > maxChars {
			return "", 0, fmt.Errorf("笔记超过 %d 个字符，暂不支持月份区块写入", maxChars)
		}
		content.WriteString(payload.Data.Content)
		if !payload.Data.HasMore {
			break
		}
		if payload.Data.NextCursor == "" || payload.Data.NextCursor == cursor {
			return "", 0, errors.New("Inkstone 正文游标无效")
		}
		cursor = payload.Data.NextCursor
	}
	return content.String(), rev, nil
}

func (c *mcpClient) editNote(ctx context.Context, input editRequest) (editedNote, error) {
	arguments := map[string]any{
		"operation_id": input.OperationID,
		"note_id":      input.NoteID,
		"expected_rev": input.ExpectedRev,
		"operation":    input.Operation,
		"text":         input.Text,
	}
	if input.OldText != "" {
		arguments["old_text"] = input.OldText
	}
	structured, err := c.callTool(ctx, "edit_note", arguments)
	if err != nil {
		return editedNote{}, err
	}
	var payload struct {
		Data struct {
			Note struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				Rev   int    `json:"rev"`
			} `json:"note"`
		} `json:"data"`
	}
	if err := json.Unmarshal(structured, &payload); err != nil {
		return editedNote{}, fmt.Errorf("解析 Inkstone 写入结果失败：%w", err)
	}
	if payload.Data.Note.ID == "" || payload.Data.Note.Rev < 1 {
		return editedNote{}, errors.New("Inkstone 返回的写入结果不完整")
	}
	return editedNote{
		ID:    payload.Data.Note.ID,
		Title: payload.Data.Note.Title,
		Rev:   payload.Data.Note.Rev,
	}, nil
}

func (c *mcpClient) test(ctx context.Context) error {
	structured, err := c.callTool(ctx, "list_notes", map[string]any{
		"view":  "recent",
		"limit": 1,
	})
	if err != nil {
		return err
	}
	if !json.Valid(structured) {
		return errors.New("Inkstone 返回了无效数据")
	}
	return nil
}

func (c *mcpClient) callTool(
	ctx context.Context,
	name string,
	arguments map[string]any,
) (json.RawMessage, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		result, err := c.callToolOnce(ctx, name, arguments)
		if err == nil {
			return result, nil
		}
		lastErr = err
		var toolErr *mcpToolError
		if errors.As(err, &toolErr) || ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

func (c *mcpClient) callToolOnce(
	ctx context.Context,
	name string,
	arguments map[string]any,
) (json.RawMessage, error) {
	sessionID, err := c.initialize(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.notifyInitialized(ctx, sessionID); err != nil {
		return nil, err
	}
	result, _, err := c.rpc(ctx, sessionID, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	}, true)
	if err != nil {
		return nil, err
	}
	var toolResult mcpToolResult
	if err := json.Unmarshal(result, &toolResult); err != nil {
		return nil, fmt.Errorf("解析 Inkstone MCP 结果失败：%w", err)
	}
	if toolResult.IsError {
		return nil, decodeToolError(toolResult.Content)
	}
	if len(toolResult.StructuredContent) > 0 && string(toolResult.StructuredContent) != "null" {
		return toolResult.StructuredContent, nil
	}
	for _, item := range toolResult.Content {
		if item.Type == "text" && json.Valid([]byte(item.Text)) {
			return json.RawMessage(item.Text), nil
		}
	}
	return nil, errors.New("Inkstone MCP 未返回结构化数据")
}

func (c *mcpClient) initialize(ctx context.Context) (string, error) {
	_, headers, err := c.rpc(ctx, "", "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    "TeleBox-Go Inkstone",
			"version": mcpClientVersion,
		},
	}, true)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(headers.Get("Mcp-Session-Id")), nil
}

func (c *mcpClient) notifyInitialized(ctx context.Context, sessionID string) error {
	_, _, err := c.rpc(ctx, sessionID, "notifications/initialized", map[string]any{}, false)
	return err
}

func (c *mcpClient) rpc(
	ctx context.Context,
	sessionID string,
	method string,
	params any,
	wantResponse bool,
) (json.RawMessage, http.Header, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	if wantResponse {
		payload["id"] = c.nextID.Add(1)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	headers := http.Header{
		"Authorization":        []string{"Bearer " + c.apiKey},
		"Content-Type":         []string{"application/json"},
		"Accept":               []string{"application/json, text/event-stream"},
		"MCP-Protocol-Version": []string{mcpProtocolVersion},
	}
	if sessionID != "" {
		headers.Set("Mcp-Session-Id", sessionID)
	}
	response, err := c.http.Do(ctx, httpclient.Request{
		Method:  http.MethodPost,
		URL:     c.endpoint,
		Headers: headers,
		Body:    body,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("连接 Inkstone 失败：%w", err)
	}
	if response.StatusCode == http.StatusAccepted && !wantResponse {
		return nil, response.Headers, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.Headers, httpStatusError(response.StatusCode, response.Body)
	}
	if !wantResponse && len(bytes.TrimSpace(response.Body)) == 0 {
		return nil, response.Headers, nil
	}
	envelope, err := decodeEnvelope(response.Body, response.Headers.Get("Content-Type"))
	if err != nil {
		return nil, response.Headers, err
	}
	if envelope.Error != nil {
		return nil, response.Headers, fmt.Errorf(
			"Inkstone MCP 错误 %d：%s",
			envelope.Error.Code,
			strings.TrimSpace(envelope.Error.Message),
		)
	}
	if wantResponse && len(envelope.Result) == 0 {
		return nil, response.Headers, errors.New("Inkstone MCP 未返回结果")
	}
	return envelope.Result, response.Headers, nil
}

func decodeEnvelope(body []byte, contentType string) (mcpEnvelope, error) {
	values := [][]byte{bytes.TrimSpace(body)}
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		values = sseData(body)
	}
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		var envelope mcpEnvelope
		if err := json.Unmarshal(value, &envelope); err == nil &&
			envelope.JSONRPC == "2.0" &&
			(len(envelope.ID) > 0 || len(envelope.Result) > 0 || envelope.Error != nil) {
			return envelope, nil
		}
	}
	return mcpEnvelope{}, errors.New("Inkstone MCP 返回格式无法识别")
}

func sseData(body []byte) [][]byte {
	blocks := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n\n")
	result := make([][]byte, 0, len(blocks))
	for _, block := range blocks {
		var data []string
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if len(data) > 0 {
			result = append(result, []byte(strings.Join(data, "\n")))
		}
	}
	return result
}

func decodeToolError(content []mcpContent) error {
	for _, item := range content {
		if item.Type != "text" {
			continue
		}
		var payload struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Status  int    `json:"status"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(item.Text), &payload) == nil && payload.Error.Message != "" {
			return &mcpToolError{
				Code:    payload.Error.Code,
				Message: localizeMCPMessage(payload.Error.Message),
				Status:  payload.Error.Status,
			}
		}
		if strings.TrimSpace(item.Text) != "" {
			return &mcpToolError{Message: localizeMCPMessage(item.Text)}
		}
	}
	return &mcpToolError{Message: "Inkstone 未完成请求"}
}

func httpStatusError(status int, body []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return &mcpToolError{Status: status, Message: "Inkstone API 密钥无效或已撤销"}
	case http.StatusForbidden:
		return &mcpToolError{Status: status, Message: "Inkstone MCP 未启用或当前密钥权限不足"}
	case http.StatusNotFound:
		return &mcpToolError{Status: status, Message: "Inkstone MCP 地址不存在，请检查 INKSTONE_MCP_URL"}
	case http.StatusTooManyRequests:
		return &mcpToolError{Status: status, Message: "Inkstone 请求过于频繁，请稍后重试"}
	}
	message := strings.TrimSpace(string(body))
	if len(message) > 240 {
		message = message[:240]
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return &mcpToolError{
		Status:  status,
		Message: "Inkstone HTTP " + strconv.Itoa(status) + "：" + message,
	}
}

func localizeMCPMessage(value string) string {
	value = strings.TrimSpace(value)
	switch {
	case strings.Contains(value, "Note not found"):
		return "未找到指定的 Inkstone 笔记，请检查笔记 ID"
	case strings.Contains(value, "MCP writes are disabled"):
		return "Inkstone 未开放写入权限，请在“设置 → MCP”中启用"
	case strings.Contains(value, "OAuth scope required: notes:write"):
		return "当前 API 密钥没有写入权限，请重新创建密钥"
	case strings.Contains(value, "This note was modified elsewhere"):
		return "笔记刚刚被其他客户端修改"
	case strings.Contains(value, "operation_id was already used"):
		return "该 Telegram 消息已经用不同内容写入过此笔记"
	case value == "":
		return "Inkstone 未完成请求"
	default:
		return value
	}
}
