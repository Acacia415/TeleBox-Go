package iplookup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

var (
	ipv4Candidate = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	domainPattern = regexp.MustCompile(
		`(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}\b`,
	)
)

type payload struct {
	IP      string `json:"ip"`
	Success bool   `json:"success"`
	Message string `json:"message"`
	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
	Flag    struct {
		Emoji string `json:"emoji"`
	} `json:"flag"`
	Connection struct {
		ASN int64  `json:"asn"`
		Org string `json:"org"`
		ISP string `json:"isp"`
	} `json:"connection"`
	Timezone struct {
		ID string `json:"id"`
	} `json:"timezone"`
	Security struct {
		Proxy   bool `json:"proxy"`
		VPN     bool `json:"vpn"`
		Tor     bool `json:"tor"`
		Hosting bool `json:"hosting"`
	} `json:"security"`
}

type Plugin struct {
	services service.Container
	resolve  func(context.Context, string) (string, error)
	endpoint string
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services: services,
		resolve:  resolveTarget,
		endpoint: "https://ipwho.is/",
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "ip",
		Version:     "0.1.0",
		Description: "查询 IP 地址或域名的地理与网络信息",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "ip",
		Description: "查询 IP 地址或域名",
		OwnerOnly:   true,
		Handler:     p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	query := strings.TrimSpace(request.RawArgs)
	if query == "" && request.Message.ReplyToID > 0 {
		messages, err := p.services.Telegram.GetMessages(
			ctx,
			request.Message.ChatID,
			[]int{request.Message.ReplyToID},
		)
		if err != nil {
			p.services.Logger.Warn("read replied message for IP lookup", "error", err)
		} else if len(messages) > 0 {
			query = extractTarget(messages[0].Text)
		}
	}
	if query == "" || strings.EqualFold(query, "help") || strings.EqualFold(query, "h") {
		return p.respondHTML(ctx, request, helpText(request.Prefix))
	}
	query = strings.Fields(query)[0]

	if err := p.respond(ctx, request, "⏳ 查询 "+query+"…"); err != nil {
		return err
	}
	resolved, err := p.resolve(ctx, query)
	if err != nil {
		p.services.Logger.Warn("IP target resolution failed", "query", query, "error", err)
		return p.respond(ctx, request, userFacingIPError(err))
	}
	data, err := p.lookup(ctx, resolved)
	if err != nil {
		p.services.Logger.Warn("IP lookup failed", "query", query, "ip", resolved, "error", err)
		return p.respond(ctx, request, userFacingIPError(err))
	}
	return p.respondHTML(ctx, request, formatPayload(query, data))
}

func (p *Plugin) lookup(ctx context.Context, address string) (payload, error) {
	endpoint := p.endpoint + url.PathEscape(address) + "?lang=zh-CN"
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		URL: endpoint,
		Headers: http.Header{
			"User-Agent": []string{"TeleBox-Go-IP/0.1"},
		},
	})
	if err != nil {
		return payload{}, fmt.Errorf("网络请求失败：%w", err)
	}

	var result payload
	if len(response.Body) > 0 {
		if err := json.Unmarshal(response.Body, &result); err != nil {
			return payload{}, fmt.Errorf("解析上游响应失败：%w", err)
		}
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return payload{}, errors.New("查询额度已用完")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if result.Message != "" {
			return payload{}, errors.New(result.Message)
		}
		return payload{}, fmt.Errorf("上游返回 HTTP %d", response.StatusCode)
	}
	if !result.Success {
		if result.Message == "" {
			result.Message = "IP 地址无效或属于保留地址段"
		}
		return payload{}, errors.New(result.Message)
	}
	return result, nil
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

func (p *Plugin) respondHTML(ctx context.Context, request command.Request, text string) error {
	if request.Message.Outgoing {
		_, err := telegram.EditHTML(
			ctx,
			p.services.Telegram,
			request.Message.ChatID,
			request.Message.ID,
			text,
		)
		return err
	}
	_, err := telegram.ReplyHTML(
		ctx,
		p.services.Telegram,
		request.Message.ChatID,
		request.Message.ID,
		text,
	)
	return err
}

func userFacingIPError(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "有效的 ipv4"),
		strings.Contains(message, "invalid"):
		return "❌ IP 或域名格式无效"
	case strings.Contains(message, "域名解析"),
		strings.Contains(message, "no such host"):
		return "❌ 无法解析该域名"
	case strings.Contains(message, "额度"),
		strings.Contains(message, "429"):
		return "⚠️ 查询额度已用完"
	default:
		return "❌ IP 信息查询失败"
	}
}

func resolveTarget(ctx context.Context, query string) (string, error) {
	query = strings.Trim(strings.TrimSpace(query), "[]")
	if address := net.ParseIP(query); address != nil {
		return address.String(), nil
	}
	domain := strings.ToLower(strings.TrimSuffix(query, "."))
	if len(domain) > 253 || !domainPattern.MatchString(domain) {
		return "", errors.New("请输入有效的 IPv4、IPv6 地址或域名")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, domain)
	if err != nil {
		return "", fmt.Errorf("域名解析失败：%w", err)
	}
	if len(addresses) == 0 {
		return "", errors.New("域名没有可用的 IP 地址")
	}
	for _, address := range addresses {
		if address.IP.To4() != nil {
			return address.IP.String(), nil
		}
	}
	return addresses[0].IP.String(), nil
}

func extractTarget(text string) string {
	if candidate := ipv4Candidate.FindString(text); candidate != "" {
		if net.ParseIP(candidate) != nil {
			return candidate
		}
	}
	for _, field := range strings.Fields(text) {
		candidate := strings.Trim(field, " \t\r\n<>()[]{}\"'`,;，。")
		if address := net.ParseIP(strings.Trim(candidate, "[]")); address != nil {
			return address.String()
		}
	}
	return domainPattern.FindString(text)
}

func formatPayload(query string, data payload) string {
	location := joinNonEmpty(" - ", data.Country, data.Region, data.City)
	if location == "" {
		location = "N/A"
	}
	if data.Flag.Emoji != "" {
		location = data.Flag.Emoji + " " + location
	}
	isp := fallback(data.Connection.ISP, "N/A")
	organization := fallback(data.Connection.Org, "N/A")
	asInfo := "N/A"
	if data.Connection.ASN > 0 {
		asInfo = fmt.Sprintf("AS%d", data.Connection.ASN)
	}
	lines := []string{
		"<b>🌍 IP 信息</b>",
		"",
		"• 查询目标：<code>" + html.EscapeString(query) + "</code>",
		"• IP 地址：<code>" + html.EscapeString(fallback(data.IP, "N/A")) + "</code>",
		"• 地理位置：" + html.EscapeString(location),
		"• ISP：" + html.EscapeString(isp),
		"• 组织：" + html.EscapeString(organization),
		"• AS 号：<code>" + html.EscapeString(asInfo) + "</code>",
	}
	if data.Timezone.ID != "" {
		lines = append(lines, "• 时区：<code>"+html.EscapeString(data.Timezone.ID)+"</code>")
	}
	var warnings []string
	if data.Security.Proxy || data.Security.VPN || data.Security.Tor {
		warnings = append(warnings, "此 IP 可能使用代理、VPN 或 Tor")
	}
	if data.Security.Hosting {
		warnings = append(warnings, "此 IP 可能属于数据中心")
	}
	if len(warnings) > 0 {
		lines = append(lines, "", "<b>⚠️ 风险标记</b>")
		for _, warning := range warnings {
			lines = append(lines, "• "+html.EscapeString(warning))
		}
	}
	if data.Connection.ASN > 0 {
		link := fmt.Sprintf("https://bgp.he.net/AS%d", data.Connection.ASN)
		lines = append(lines, "", "<a href=\""+link+"\">查看 BGP 信息</a>")
	}
	return strings.Join(lines, "\n")
}

func joinNonEmpty(separator string, values ...string) string {
	nonEmpty := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			nonEmpty = append(nonEmpty, value)
		}
	}
	return strings.Join(nonEmpty, separator)
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func helpText(prefix string) string {
	commandName := html.EscapeString(prefix + "ip")
	return "<b>🌍 IP 信息</b>\n\n" +
		"<b>用法</b>\n" +
		"• <code>" + commandName + " IP地址</code>\n" +
		"• <code>" + commandName + " 域名</code>\n" +
		"• 回复含有 IP 或域名的消息后发送 <code>" + commandName + "</code>\n\n" +
		"<b>示例</b>\n" +
		"<code>" + commandName + " 1.1.1.1</code>\n" +
		"<code>" + commandName + " example.com</code>"
}
