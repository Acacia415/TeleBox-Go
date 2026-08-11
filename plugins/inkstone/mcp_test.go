package inkstone

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
)

type scriptedHTTP struct {
	responses []httpclient.Response
	requests  []httpclient.Request
}

func (s *scriptedHTTP) Do(
	_ context.Context,
	request httpclient.Request,
) (httpclient.Response, error) {
	s.requests = append(s.requests, request)
	response := s.responses[0]
	s.responses = s.responses[1:]
	return response, nil
}

func (s *scriptedHTTP) JSON(
	ctx context.Context,
	request httpclient.Request,
	target any,
) (httpclient.Response, error) {
	response, err := s.Do(ctx, request)
	if err == nil {
		err = json.Unmarshal(response.Body, target)
	}
	return response, err
}

func (*scriptedHTTP) Close() {}

func TestNormalizeEndpoint(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"https://example.com":         "https://example.com/mcp",
		"https://example.com/":        "https://example.com/mcp",
		"https://example.com/custom/": "https://example.com/custom",
	}
	if _, err := normalizeEndpoint(""); err == nil ||
		!strings.Contains(err.Error(), "未配置") {
		t.Fatalf("empty endpoint error = %v", err)
	}
	for input, want := range tests {
		got, err := normalizeEndpoint(input)
		if err != nil || got != want {
			t.Errorf("normalizeEndpoint(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{
		"ftp://example.com/mcp",
		"https://user@example.com/mcp",
		"https://example.com/mcp?key=secret",
	} {
		if _, err := normalizeEndpoint(input); err == nil {
			t.Errorf("normalizeEndpoint(%q) accepted invalid URL", input)
		}
	}
}

func TestDecodeEnvelopeJSONAndSSE(t *testing.T) {
	t.Parallel()

	payload := `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`
	jsonEnvelope, err := decodeEnvelope([]byte(payload), "application/json")
	if err != nil || !strings.Contains(string(jsonEnvelope.Result), `"ok":true`) {
		t.Fatalf("JSON envelope = %+v, %v", jsonEnvelope, err)
	}
	sseEnvelope, err := decodeEnvelope(
		[]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n"+
			"event: message\ndata: "+payload+"\n\n"),
		"text/event-stream",
	)
	if err != nil || !strings.Contains(string(sseEnvelope.Result), `"ok":true`) {
		t.Fatalf("SSE envelope = %+v, %v", sseEnvelope, err)
	}
}

func TestDecodeToolErrorAndHTTPStatus(t *testing.T) {
	t.Parallel()

	err := decodeToolError([]mcpContent{{
		Type: "text",
		Text: `{"error":{"code":"revision_conflict","message":"This note was modified elsewhere","status":409}}`,
	}})
	toolErr, ok := err.(*mcpToolError)
	if !ok || toolErr.Status != http.StatusConflict ||
		!strings.Contains(toolErr.Message, "被其他客户端修改") {
		t.Fatalf("tool error = %#v", err)
	}
	if got := httpStatusError(http.StatusUnauthorized, nil).Error(); !strings.Contains(got, "密钥无效") {
		t.Fatalf("unauthorized error = %q", got)
	}
}

func TestStructuredToolResultShape(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"structuredContent":{"id":"abc","metadata":{"rev":2}}}`)
	var result mcpToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if string(result.StructuredContent) != `{"id":"abc","metadata":{"rev":2}}` {
		t.Fatalf("structured content = %s", result.StructuredContent)
	}
}

func TestMCPClientPerformsHandshakeAndToolCall(t *testing.T) {
	t.Parallel()

	httpClient := &scriptedHTTP{responses: []httpclient.Response{
		{
			StatusCode: http.StatusOK,
			Headers: http.Header{
				"Content-Type":   []string{"application/json"},
				"Mcp-Session-Id": []string{"session-1"},
			},
			Body: []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`),
		},
		{StatusCode: http.StatusAccepted, Headers: make(http.Header)},
		{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       []byte(`{"jsonrpc":"2.0","id":2,"result":{"structuredContent":{"data":{"notes":[]}}}}`),
		},
	}}
	client, err := newMCPClient(
		httpClient,
		"https://example.com/mcp",
		"ink_"+strings.Repeat("a", 43),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(httpClient.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(httpClient.requests))
	}
	for index, request := range httpClient.requests {
		if request.Headers.Get("Authorization") != "Bearer ink_"+strings.Repeat("a", 43) {
			t.Fatalf("request %d authorization header is missing", index)
		}
		if request.URL != "https://example.com/mcp" {
			t.Fatalf("request %d URL = %q", index, request.URL)
		}
	}
	if got := httpClient.requests[1].Headers.Get("Mcp-Session-Id"); got != "session-1" {
		t.Fatalf("notification session ID = %q", got)
	}
	if !strings.Contains(string(httpClient.requests[2].Body), `"name":"list_notes"`) {
		t.Fatalf("tool request = %s", httpClient.requests[2].Body)
	}
}

func TestMCPClientSearchesNotes(t *testing.T) {
	t.Parallel()

	id := "01k00000000000000000000000"
	httpClient := &scriptedHTTP{responses: []httpclient.Response{
		{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`),
		},
		{StatusCode: http.StatusAccepted, Headers: make(http.Header)},
		{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body: []byte(`{"jsonrpc":"2.0","id":2,"result":{"structuredContent":{"results":[` +
				`{"id":"` + id + `","title":"浩希","url":"https://inkstone.example.com/n/` + id + `"}]}}}`),
		},
	}}
	client, err := newMCPClient(
		httpClient,
		"https://inkstone.example.com/mcp",
		"ink_"+strings.Repeat("a", 43),
	)
	if err != nil {
		t.Fatal(err)
	}
	results, err := client.searchNotes(context.Background(), "浩希")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != id || results[0].Title != "浩希" {
		t.Fatalf("search results = %#v", results)
	}
	requestBody := string(httpClient.requests[2].Body)
	if !strings.Contains(requestBody, `"name":"search"`) ||
		!strings.Contains(requestBody, `"query":"浩希"`) {
		t.Fatalf("search tool request = %s", requestBody)
	}
}
