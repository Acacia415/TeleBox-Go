package dig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const defaultDNSServer = "1.1.1.1:53"

type query struct {
	Name       string
	Type       uint16
	Server     string
	Short      bool
	Network    string
	TypeString string
}

type record struct {
	Name  string
	Type  string
	Value string
	TTL   uint32
}

type Plugin struct {
	services service.Container
	exchange func(context.Context, query) ([]record, error)
}

func New(services service.Container) *Plugin {
	result := &Plugin{services: services}
	result.exchange = result.exchangeDNS
	return result
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "dig",
		Version:     "0.2.0",
		Description: "使用纯 Go DNS 客户端查询常见记录",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "dig",
		Description: "查询 DNS 记录",
		Usage: []string{
			"dig <域名> [记录类型] [@DNS服务器] [+short] [+tcp]",
		},
		OwnerOnly: true,
		Handler:   p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 || strings.EqualFold(request.Args[0], "help") {
		return p.respondHTML(ctx, request, helpText(request.Prefix))
	}
	parsed, err := parseArgs(request.Args)
	if err != nil {
		return p.respond(ctx, request, "❌ "+err.Error())
	}
	if err := p.respond(ctx, request, "⏳ DNS 查询中…"); err != nil {
		return err
	}
	records, err := p.exchange(ctx, parsed)
	if err != nil {
		p.services.Logger.Warn(
			"DNS query failed",
			"name", parsed.Name,
			"type", parsed.TypeString,
			"server", parsed.Server,
			"error", err,
		)
		return p.respond(ctx, request, userFacingDNSError(err))
	}
	return p.respondHTML(ctx, request, p.formatRecords(ctx, parsed, records))
}

func (p *Plugin) exchangeDNS(ctx context.Context, parsed query) ([]record, error) {
	name := parsed.Name
	if parsed.Type == dns.TypePTR {
		if address := net.ParseIP(strings.Trim(name, "[]")); address != nil {
			reversed, err := dns.ReverseAddr(address.String())
			if err != nil {
				return nil, fmt.Errorf("create reverse lookup: %w", err)
			}
			name = reversed
		}
	}
	message := new(dns.Msg)
	message.SetQuestion(dns.Fqdn(name), parsed.Type)
	message.SetEdns0(1232, true)
	client := &dns.Client{
		Net:     parsed.Network,
		Timeout: 15 * time.Second,
	}
	response, _, err := client.ExchangeContext(ctx, message, parsed.Server)
	if err != nil {
		return nil, err
	}
	if response.Truncated && parsed.Network == "udp" {
		client.Net = "tcp"
		response, _, err = client.ExchangeContext(ctx, message, parsed.Server)
		if err != nil {
			return nil, err
		}
	}
	if response.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("DNS %s", dns.RcodeToString[response.Rcode])
	}
	result := make([]record, 0, len(response.Answer))
	for _, answer := range response.Answer {
		result = append(result, portableRecord(answer))
	}
	return result, nil
}

func portableRecord(answer dns.RR) record {
	header := answer.Header()
	result := record{
		Name: strings.TrimSuffix(header.Name, "."),
		Type: dns.TypeToString[header.Rrtype],
		TTL:  header.Ttl,
	}
	switch value := answer.(type) {
	case *dns.A:
		result.Value = value.A.String()
	case *dns.AAAA:
		result.Value = value.AAAA.String()
	case *dns.CNAME:
		result.Value = strings.TrimSuffix(value.Target, ".")
	case *dns.MX:
		result.Value = fmt.Sprintf("%d %s", value.Preference, strings.TrimSuffix(value.Mx, "."))
	case *dns.NS:
		result.Value = strings.TrimSuffix(value.Ns, ".")
	case *dns.PTR:
		result.Value = strings.TrimSuffix(value.Ptr, ".")
	case *dns.TXT:
		result.Value = strings.Join(value.Txt, " ")
	case *dns.SRV:
		result.Value = fmt.Sprintf(
			"%d %d %d %s",
			value.Priority,
			value.Weight,
			value.Port,
			strings.TrimSuffix(value.Target, "."),
		)
	case *dns.CAA:
		result.Value = fmt.Sprintf("%d %s %s", value.Flag, value.Tag, value.Value)
	case *dns.SOA:
		result.Value = fmt.Sprintf(
			"%s %s %d %d %d %d %d",
			strings.TrimSuffix(value.Ns, "."),
			strings.TrimSuffix(value.Mbox, "."),
			value.Serial,
			value.Refresh,
			value.Retry,
			value.Expire,
			value.Minttl,
		)
	default:
		prefix := fmt.Sprintf("%s\t%d\tIN\t%s\t", header.Name, header.Ttl, result.Type)
		result.Value = strings.TrimPrefix(answer.String(), prefix)
	}
	return result
}

func parseArgs(args []string) (query, error) {
	result := query{
		Type:       dns.TypeA,
		TypeString: "A",
		Server:     defaultDNSServer,
		Network:    "udp",
	}
	for index := 0; index < len(args); index++ {
		arg := strings.TrimSpace(args[index])
		if arg == "" {
			continue
		}
		switch {
		case strings.HasPrefix(arg, "@"):
			server, err := normalizeServer(strings.TrimPrefix(arg, "@"))
			if err != nil {
				return query{}, err
			}
			result.Server = server
		case strings.EqualFold(arg, "+short"):
			result.Short = true
		case strings.EqualFold(arg, "+tcp"):
			result.Network = "tcp"
		case strings.EqualFold(arg, "-t"):
			if index+1 >= len(args) {
				return query{}, errors.New("-t 后缺少记录类型")
			}
			index++
			if err := setRecordType(&result, args[index]); err != nil {
				return query{}, err
			}
		case strings.HasPrefix(arg, "+"):
			return query{}, fmt.Errorf("不支持选项 %s", arg)
		default:
			upper := strings.ToUpper(arg)
			if _, ok := dns.StringToType[upper]; ok && result.Name != "" {
				if err := setRecordType(&result, upper); err != nil {
					return query{}, err
				}
			} else if result.Name == "" {
				result.Name = arg
			} else {
				return query{}, fmt.Errorf("无法识别参数 %s", arg)
			}
		}
	}
	if result.Name == "" {
		return query{}, errors.New("缺少域名或 IP 地址")
	}
	return result, nil
}

func setRecordType(result *query, value string) error {
	value = strings.ToUpper(strings.TrimSpace(value))
	recordType, ok := dns.StringToType[value]
	if !ok {
		return fmt.Errorf("不支持记录类型 %s", value)
	}
	switch recordType {
	case dns.TypeA, dns.TypeAAAA, dns.TypeMX, dns.TypeCNAME, dns.TypeTXT,
		dns.TypeNS, dns.TypeSOA, dns.TypePTR, dns.TypeSRV, dns.TypeCAA:
	default:
		return fmt.Errorf("不支持记录类型 %s", value)
	}
	result.Type = recordType
	result.TypeString = value
	return nil
}

func normalizeServer(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("DNS 服务器不能为空")
	}
	if host, port, err := net.SplitHostPort(value); err == nil {
		if host == "" || port == "" {
			return "", errors.New("DNS 服务器地址无效")
		}
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return "", errors.New("DNS 服务器端口无效")
		}
		return value, nil
	}
	value = strings.Trim(value, "[]")
	return net.JoinHostPort(value, "53"), nil
}

func (p *Plugin) formatRecords(ctx context.Context, parsed query, records []record) string {
	header := fmt.Sprintf(
		"<b>🌐 DNS 解析</b>\n\n• 查询：<code>%s</code>\n• 类型：<code>%s</code>",
		html.EscapeString(parsed.Name),
		html.EscapeString(parsed.TypeString),
	)
	if len(records) == 0 {
		return header + "\n\n⚪ 没有记录"
	}
	limit := len(records)
	if limit > 3 {
		limit = 3
	}
	lines := []string{header}
	for _, item := range records[:limit] {
		icon := recordIcon(item.Type)
		line := fmt.Sprintf(
			"\n%s <code>%s</code>\n• %s：<code>%s</code>\n• TTL：<code>%ds</code>",
			icon,
			html.EscapeString(item.Name),
			html.EscapeString(item.Type),
			html.EscapeString(item.Value),
			item.TTL,
		)
		if net.ParseIP(item.Value) != nil {
			if location := p.lookupLocation(ctx, item.Value); location != "" {
				line += "\n• 位置：" + html.EscapeString(location)
			}
		}
		lines = append(lines, line)
	}
	if len(records) > limit {
		lines = append(lines, fmt.Sprintf("\n<i>另有 %d 条记录未显示</i>", len(records)-limit))
	}
	return strings.Join(lines, "\n")
}

func (p *Plugin) lookupLocation(ctx context.Context, address string) string {
	endpoint := "https://ipwho.is/" + url.PathEscape(address) +
		"?lang=zh-CN&fields=success,country,region,city,connection.asn"
	response, err := p.services.HTTP.Do(ctx, httpclient.Request{URL: endpoint})
	if err != nil || response.StatusCode != http.StatusOK {
		return ""
	}
	var payload struct {
		Success    bool   `json:"success"`
		Country    string `json:"country"`
		Region     string `json:"region"`
		City       string `json:"city"`
		Connection struct {
			ASN int64 `json:"asn"`
		} `json:"connection"`
	}
	if json.Unmarshal(response.Body, &payload) != nil || !payload.Success {
		return ""
	}
	location := joinNonEmpty("-", payload.Country, payload.Region, payload.City)
	if payload.Connection.ASN > 0 {
		location += fmt.Sprintf("-AS%d", payload.Connection.ASN)
	}
	return strings.TrimPrefix(location, "-")
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

func userFacingDNSError(err error) string {
	message := strings.ToUpper(err.Error())
	switch {
	case strings.Contains(message, "NXDOMAIN"):
		return "❌ 域名不存在"
	case strings.Contains(message, "TIMEOUT"),
		strings.Contains(message, "DEADLINE"):
		return "❌ DNS 服务器无响应"
	case strings.Contains(message, "REFUSED"):
		return "⛔ DNS 服务器拒绝查询"
	default:
		return "❌ DNS 查询失败"
	}
}

func recordIcon(recordType string) string {
	switch recordType {
	case "A", "AAAA":
		return "📍"
	case "CNAME":
		return "🔗"
	case "MX":
		return "📧"
	case "NS":
		return "🌐"
	case "TXT":
		return "📝"
	default:
		return "📋"
	}
}

func joinNonEmpty(separator string, values ...string) string {
	var result []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return strings.Join(result, separator)
}

func helpText(prefix string) string {
	commandName := html.EscapeString(prefix + "dig")
	return "<b>🌐 DNS 查询</b>\n\n" +
		"<b>用法</b>\n" +
		"<code>" + commandName + " 域名 [类型] [@DNS服务器] [+short] [+tcp]</code>\n\n" +
		"<b>记录类型</b>\n" +
		"<code>A AAAA MX CNAME TXT NS SOA PTR SRV CAA</code>\n\n" +
		"<b>示例</b>\n" +
		"<code>" + commandName + " example.com A</code>\n" +
		"<code>" + commandName + " example.com MX @8.8.8.8</code>"
}
