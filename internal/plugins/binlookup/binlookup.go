package binlookup

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

const cacheTTL = 30 * 24 * time.Hour

var nonDigit = regexp.MustCompile(`\D`)

type payload struct {
	Number struct {
		Length int   `json:"length"`
		Luhn   *bool `json:"luhn"`
	} `json:"number"`
	Scheme  string `json:"scheme"`
	Type    string `json:"type"`
	Brand   string `json:"brand"`
	Prepaid *bool  `json:"prepaid"`
	Country struct {
		Alpha2   string `json:"alpha2"`
		Name     string `json:"name"`
		Emoji    string `json:"emoji"`
		Currency string `json:"currency"`
	} `json:"country"`
	Bank struct {
		Name  string `json:"name"`
		URL   string `json:"url"`
		Phone string `json:"phone"`
		City  string `json:"city"`
	} `json:"bank"`
}

type cacheEntry struct {
	FetchedAt time.Time `json:"fetched_at"`
	Payload   payload   `json:"payload"`
}

type Plugin struct {
	services service.Container
	now      func() time.Time
}

func New(services service.Container) *Plugin {
	return &Plugin{services: services, now: time.Now}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "bin",
		Version:     "0.1.0",
		Description: "查询银行卡 BIN/IIN 发行信息",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:        "bin",
		Description: "查询 6–8 位银行卡 BIN/IIN",
		OwnerOnly:   true,
		Handler:     p.handle,
	}}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 || strings.EqualFold(request.Args[0], "help") ||
		strings.EqualFold(request.Args[0], "h") {
		return p.respondHTML(ctx, request, helpText(request.Prefix))
	}
	number := nonDigit.ReplaceAllString(request.Args[0], "")
	if len(number) < 6 || len(number) > 8 {
		return p.respond(ctx, request, "❌ BIN 必须是 6–8 位数字")
	}
	if err := p.respond(ctx, request, "⏳ 查询 BIN "+number+"…"); err != nil {
		return err
	}

	data, cached, err := p.lookup(ctx, number)
	if err != nil {
		p.services.Logger.Warn("BIN lookup failed", "bin", number, "error", err)
		message := "❌ BIN 查询失败"
		switch {
		case strings.Contains(err.Error(), "未找到"):
			message = "❌ 未找到该 BIN"
		case strings.Contains(err.Error(), "频率"),
			strings.Contains(err.Error(), "429"):
			message = "⚠️ BIN 查询频率受限"
		}
		return p.respond(ctx, request, message)
	}
	text := formatPayload(number, data)
	if cached {
		text += "\n\n<i>本地缓存</i>"
	}
	return p.respondHTML(ctx, request, text)
}

func (p *Plugin) lookup(ctx context.Context, number string) (payload, bool, error) {
	if raw, err := p.services.Storage.Get(ctx, "bin", "lookup/"+number); err == nil {
		var entry cacheEntry
		if json.Unmarshal(raw, &entry) == nil && p.now().Before(entry.FetchedAt.Add(cacheTTL)) {
			return entry.Payload, true, nil
		}
	}

	response, err := p.services.HTTP.Do(ctx, httpclient.Request{
		URL: "https://lookup.binlist.net/" + number,
		Headers: http.Header{
			"Accept-Version": []string{"3"},
		},
	})
	if err != nil {
		return payload{}, false, fmt.Errorf("BIN 查询失败：%w", err)
	}
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return payload{}, false, fmt.Errorf("未找到 BIN：%s", number)
	case http.StatusTooManyRequests:
		return payload{}, false, fmt.Errorf("BIN 查询频率受限")
	default:
		return payload{}, false, fmt.Errorf("BIN 上游返回 HTTP %d", response.StatusCode)
	}
	var result payload
	if err := json.Unmarshal(response.Body, &result); err != nil {
		return payload{}, false, fmt.Errorf("解析 BIN 查询结果失败：%w", err)
	}

	entry := cacheEntry{FetchedAt: p.now().UTC(), Payload: result}
	if encoded, err := json.Marshal(entry); err == nil {
		if err := p.services.Storage.Put(ctx, "bin", "lookup/"+number, encoded); err != nil {
			p.services.Logger.Warn("cache BIN lookup", "bin", number, "error", err)
		}
	}
	return result, false, nil
}

func (p *Plugin) respond(ctx context.Context, request command.Request, text string) error {
	if request.Message.Outgoing {
		_, err := p.services.Telegram.EditText(ctx, request.Message.ChatID, request.Message.ID, text)
		return err
	}
	_, err := p.services.Telegram.ReplyText(ctx, request.Message.ChatID, request.Message.ID, text)
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

func formatPayload(number string, data payload) string {
	scheme := schemeName(data.Scheme)
	cardType := typeName(data.Type)
	level := "—"
	if match := regexp.MustCompile(`(?i)BUSINESS|CORPORATE|PLATINUM|GOLD|CLASSIC|SIGNATURE|INFINITE|WORLD|PREMIUM`).
		FindString(data.Brand); match != "" {
		level = strings.ToUpper(match)
	}
	prepaid := "未知"
	if data.Prepaid != nil {
		if *data.Prepaid {
			prepaid = "✓"
		} else {
			prepaid = "×"
		}
	}
	business := "×"
	if regexp.MustCompile(`(?i)BUSINESS|CORPORATE|COMMERCIAL`).MatchString(data.Brand) {
		business = "✓"
	}
	country := strings.TrimSpace(strings.ReplaceAll(data.Country.Name, " (Province of China)", ""))
	if strings.EqualFold(country, "Taiwan, Province of China") {
		country = "Taiwan"
	}
	if country == "" {
		country = "未知"
	}
	if data.Country.Emoji != "" {
		country = data.Country.Emoji + " " + country
	}
	bank := normalizeBank(data.Bank.Name)
	return fmt.Sprintf(
		"<b>💳 BIN 信息</b>\n\n"+
			"<b>卡片</b>\n"+
			"• 卡头：<code>%s</code>\n"+
			"• 品牌：%s\n"+
			"• 类型：%s\n"+
			"• 等级：%s\n\n"+
			"<b>发行方</b>\n"+
			"• 国家：%s\n"+
			"• 货币：%s\n"+
			"• 银行：%s\n\n"+
			"• 预付卡：%s\n"+
			"• 商业卡：%s",
		html.EscapeString(number),
		html.EscapeString(scheme),
		html.EscapeString(cardType),
		html.EscapeString(level),
		html.EscapeString(country),
		html.EscapeString(currencyName(data.Country.Currency)),
		html.EscapeString(bank),
		prepaid,
		business,
	)
}

func schemeName(value string) string {
	switch strings.ToLower(value) {
	case "visa":
		return "Visa"
	case "mastercard":
		return "Master Card"
	case "amex":
		return "American Express"
	case "unionpay":
		return "UnionPay"
	case "":
		return "N/A"
	default:
		value = strings.ToLower(value)
		return strings.ToUpper(value[:1]) + value[1:]
	}
}

func typeName(value string) string {
	switch strings.ToLower(value) {
	case "credit":
		return "贷记"
	case "debit":
		return "借记"
	case "charge":
		return "签账"
	case "prepaid":
		return "预付"
	case "":
		return "未知"
	default:
		return value
	}
}

func normalizeBank(value string) string {
	if strings.TrimSpace(value) == "" {
		return "N/A"
	}
	value = strings.ToUpper(value)
	value = regexp.MustCompile(`\bCOMPANY LIMITED\b`).ReplaceAllString(value, "CO., LTD.")
	value = regexp.MustCompile(`\bLIMITED\b`).ReplaceAllString(value, "LTD.")
	value = regexp.MustCompile(`\)\s*LTD\.`).ReplaceAllString(value, "), LTD.")
	return value
}

func currencyName(value string) string {
	names := map[string]string{
		"USD": "美元", "TWD": "新台币", "CNY": "人民币", "HKD": "港币",
		"EUR": "欧元", "JPY": "日元", "GBP": "英镑", "AUD": "澳元",
		"CAD": "加元", "SGD": "新加坡元",
	}
	value = strings.ToUpper(value)
	if name, ok := names[value]; ok {
		return name
	}
	if value == "" {
		return "未知"
	}
	return value
}

func helpText(prefix string) string {
	commandName := html.EscapeString(prefix + "bin")
	return "<b>💳 BIN 查询</b>\n\n" +
		"<b>用法</b>\n" +
		"<code>" + commandName + " 卡头6-8位</code>\n\n" +
		"<b>示例</b>\n" +
		"<code>" + commandName + " 415042</code>\n\n" +
		"<i>数据源：Binlist</i>"
}
