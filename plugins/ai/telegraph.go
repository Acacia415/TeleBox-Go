package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
)

const telegraphAPI = "https://api.telegra.ph"

type telegraphPost struct {
	URL       string `json:"url"`
	Path      string `json:"path"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
}

type telegraphResponse struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

func (p *Plugin) publishLongAnswer(
	ctx context.Context,
	question string,
	answer string,
) string {
	if p.read(ctx, "telegraph", "off") != "on" {
		return answer
	}
	limit, _ := strconv.Atoi(p.read(ctx, "telegraph.limit", "0"))
	if limit <= 0 || len([]rune(answer)) <= limit {
		return answer
	}
	title := "AI 回答"
	if question = strings.TrimSpace(question); question != "" {
		title = "AI 回答：" + truncateRunes(question, 50)
	}
	post, err := p.createTelegraphPage(ctx, title, answer)
	if err != nil {
		p.services.Logger.Warn("publish AI answer to Telegraph", "error", err)
		return answer
	}
	posts := p.telegraphPosts(ctx)
	id := strconv.FormatInt(time.Now().UnixMilli(), 10)
	posts[id] = post
	if err := p.saveTelegraphPosts(ctx, posts); err != nil {
		p.services.Logger.Warn("save Telegraph post", "error", err)
	}
	preview := truncateRunes(answer, 500)
	if len([]rune(answer)) > len([]rune(preview)) {
		preview += "…"
	}
	return preview + "\n\n📄 完整回答：" + post.URL
}

func (p *Plugin) createTelegraphPage(
	ctx context.Context,
	title string,
	text string,
) (telegraphPost, error) {
	token, err := p.telegraphToken(ctx)
	if err != nil {
		return telegraphPost{}, err
	}
	nodes, err := json.Marshal(telegraphNodes(text))
	if err != nil {
		return telegraphPost{}, err
	}
	if len(nodes) > 60<<10 {
		return telegraphPost{}, fmt.Errorf("Telegraph 内容超过 60 KiB 安全限制")
	}
	form := url.Values{
		"access_token":   []string{token},
		"title":          []string{truncateRunes(strings.TrimSpace(title), 256)},
		"author_name":    []string{"TeleBox-Go"},
		"content":        []string{string(nodes)},
		"return_content": []string{"false"},
	}
	var response telegraphResponse
	httpResponse, err := p.services.HTTP.JSON(ctx, httpclient.Request{
		Method:  http.MethodPost,
		URL:     telegraphAPI + "/createPage",
		Headers: formHeaders(),
		Body:    []byte(form.Encode()),
	}, &response)
	if err != nil {
		return telegraphPost{}, err
	}
	if httpResponse.StatusCode != http.StatusOK || !response.OK {
		return telegraphPost{}, fmt.Errorf(
			"Telegraph createPage: HTTP %d %s",
			httpResponse.StatusCode,
			response.Error,
		)
	}
	var result struct {
		URL  string `json:"url"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return telegraphPost{}, err
	}
	if result.URL == "" || result.Path == "" {
		return telegraphPost{}, fmt.Errorf("Telegraph 未返回文章地址")
	}
	return telegraphPost{
		URL:       result.URL,
		Path:      result.Path,
		Title:     title,
		CreatedAt: time.Now().Format(time.RFC3339),
	}, nil
}

func (p *Plugin) telegraphToken(ctx context.Context) (string, error) {
	if token := strings.TrimSpace(p.read(ctx, "telegraph.token", "")); token != "" {
		return token, nil
	}
	form := url.Values{
		"short_name":  []string{"TeleBox-Go"},
		"author_name": []string{"TeleBox-Go"},
	}
	var response telegraphResponse
	httpResponse, err := p.services.HTTP.JSON(ctx, httpclient.Request{
		Method:  http.MethodPost,
		URL:     telegraphAPI + "/createAccount",
		Headers: formHeaders(),
		Body:    []byte(form.Encode()),
	}, &response)
	if err != nil {
		return "", err
	}
	if httpResponse.StatusCode != http.StatusOK || !response.OK {
		return "", fmt.Errorf(
			"Telegraph createAccount: HTTP %d %s",
			httpResponse.StatusCode,
			response.Error,
		)
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return "", err
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("Telegraph 未返回访问令牌")
	}
	if err := p.write(ctx, "telegraph.token", result.AccessToken); err != nil {
		return "", err
	}
	return result.AccessToken, nil
}

func telegraphNodes(text string) []map[string]any {
	paragraphs := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
	nodes := make([]map[string]any, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		lines := strings.Split(paragraph, "\n")
		children := make([]any, 0, len(lines)*2)
		for index, line := range lines {
			if index > 0 {
				children = append(children, map[string]any{"tag": "br"})
			}
			children = append(children, line)
		}
		nodes = append(nodes, map[string]any{
			"tag":      "p",
			"children": children,
		})
	}
	return nodes
}

func (p *Plugin) telegraphPosts(ctx context.Context) map[string]telegraphPost {
	result := map[string]telegraphPost{}
	_ = json.Unmarshal([]byte(p.read(ctx, "telegraph.posts", "{}")), &result)
	return result
}

func (p *Plugin) saveTelegraphPosts(
	ctx context.Context,
	posts map[string]telegraphPost,
) error {
	body, err := json.Marshal(posts)
	if err != nil {
		return err
	}
	return p.write(ctx, "telegraph.posts", string(body))
}

func formHeaders() http.Header {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/x-www-form-urlencoded")
	return headers
}
