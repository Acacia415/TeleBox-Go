package rate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type currencyKind string

const (
	fiat   currencyKind = "fiat"
	crypto currencyKind = "crypto"
)

type currency struct {
	Code string
	Name string
	Kind currencyKind
	ID   string
}

type cachedRate struct {
	value     float64
	updatedAt time.Time
	expiresAt time.Time
}

type Plugin struct {
	services service.Container
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]cachedRate
}

func New(services service.Container) *Plugin {
	return &Plugin{
		services: services,
		now:      time.Now,
		cache:    make(map[string]cachedRate),
	}
}

func (p *Plugin) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        "rate",
		Version:     "0.1.0",
		Description: "法币与主流加密货币汇率查询",
	}
}

func (p *Plugin) Commands() []command.Definition {
	return []command.Definition{
		{
			Name:        "rate",
			Description: "查询两种货币的汇率与数量换算",
			OwnerOnly:   true,
			Handler:     p.handle,
		},
	}
}

func (p *Plugin) Start(context.Context) error { return nil }

func (p *Plugin) Stop(context.Context) error { return nil }

func (p *Plugin) handle(ctx context.Context, request command.Request) error {
	if len(request.Args) == 0 || strings.EqualFold(request.Args[0], "help") ||
		strings.EqualFold(request.Args[0], "h") {
		return p.respondHTML(ctx, request, helpText(request.Prefix))
	}
	baseCode, quoteCode, amount, err := parseArgs(request.Args)
	if err != nil {
		return p.respondHTML(
			ctx,
			request,
			"<b>❌ 参数错误</b>\n"+html.EscapeString(err.Error())+"\n\n"+helpText(request.Prefix),
		)
	}
	base, ok := lookupCurrency(baseCode)
	if !ok {
		return p.respondHTML(ctx, request,
			"❌ 不支持 <code>"+html.EscapeString(strings.ToUpper(baseCode))+"</code>",
		)
	}
	quote, ok := lookupCurrency(quoteCode)
	if !ok {
		return p.respondHTML(ctx, request,
			"❌ 不支持 <code>"+html.EscapeString(strings.ToUpper(quoteCode))+"</code>",
		)
	}

	if err := p.respond(ctx, request, "⏳ 获取汇率…"); err != nil {
		return err
	}
	value, updatedAt, err := p.fetchRate(ctx, base, quote)
	if err != nil {
		p.services.Logger.Warn("rate lookup failed",
			"base", base.Code,
			"quote", quote.Code,
			"error", err,
		)
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "频率") {
			return p.respond(ctx, request, "⚠️ 汇率查询频率受限")
		}
		return p.respond(ctx, request, "❌ 汇率服务不可用")
	}
	return p.respondHTML(ctx, request, formatResult(base.Code, quote.Code, amount, value, updatedAt))
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

func (p *Plugin) fetchRate(ctx context.Context, base, quote currency) (float64, time.Time, error) {
	if base.Code == quote.Code {
		return 1, p.now(), nil
	}
	key := base.Code + "/" + quote.Code
	now := p.now()
	p.mu.Lock()
	if cached, ok := p.cache[key]; ok && now.Before(cached.expiresAt) {
		p.mu.Unlock()
		return cached.value, cached.updatedAt, nil
	}
	p.mu.Unlock()

	var (
		value     float64
		updatedAt time.Time
		err       error
	)
	switch {
	case base.Kind == fiat && quote.Kind == fiat:
		value, updatedAt, err = p.fetchFiatRate(ctx, base.Code, quote.Code)
	case base.Kind == crypto && quote.Kind == fiat:
		value, updatedAt, err = p.fetchCryptoFiat(ctx, base, quote.Code)
	case base.Kind == fiat && quote.Kind == crypto:
		var inverse float64
		inverse, updatedAt, err = p.fetchCryptoFiat(ctx, quote, base.Code)
		if err == nil {
			if inverse == 0 {
				err = errors.New("上游返回零汇率")
			} else {
				value = 1 / inverse
			}
		}
	case base.Kind == crypto && quote.Kind == crypto:
		value, updatedAt, err = p.fetchCryptoCross(ctx, base, quote)
	default:
		err = errors.New("不支持的货币组合")
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, time.Time{}, errors.New("上游返回无效汇率")
	}

	p.mu.Lock()
	p.cache[key] = cachedRate{
		value:     value,
		updatedAt: updatedAt,
		expiresAt: now.Add(5 * time.Minute),
	}
	p.mu.Unlock()
	return value, updatedAt, nil
}

func (p *Plugin) fetchFiatRate(ctx context.Context, base, quote string) (float64, time.Time, error) {
	query := url.Values{
		"base":    []string{strings.ToUpper(base)},
		"symbols": []string{strings.ToUpper(quote)},
	}
	endpoint := "https://api.frankfurter.dev/v1/latest?" + query.Encode()
	var payload struct {
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	response, err := p.services.HTTP.JSON(ctx, httpclient.Request{
		URL: endpoint,
	}, &payload)
	if err != nil {
		return 0, time.Time{}, err
	}
	if response.StatusCode != http.StatusOK {
		return 0, time.Time{}, fmt.Errorf("Frankfurter HTTP %d", response.StatusCode)
	}
	value := payload.Rates[strings.ToUpper(quote)]
	updatedAt := p.now()
	if parsed, err := time.Parse("2006-01-02", payload.Date); err == nil {
		updatedAt = parsed
	}
	return value, updatedAt, nil
}

func (p *Plugin) fetchCryptoFiat(
	ctx context.Context,
	coin currency,
	quote string,
) (float64, time.Time, error) {
	query := url.Values{
		"ids":                     []string{coin.ID},
		"vs_currencies":           []string{strings.ToLower(quote)},
		"include_last_updated_at": []string{"true"},
	}
	endpoint := "https://api.coingecko.com/api/v3/simple/price?" + query.Encode()
	var payload map[string]map[string]json.Number
	response, err := p.services.HTTP.JSON(ctx, httpclient.Request{URL: endpoint}, &payload)
	if err != nil {
		return 0, time.Time{}, err
	}
	if response.StatusCode != http.StatusOK {
		return 0, time.Time{}, fmt.Errorf("CoinGecko HTTP %d", response.StatusCode)
	}
	entry := payload[coin.ID]
	value, err := numberFloat(entry[strings.ToLower(quote)])
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("CoinGecko 未返回 %s/%s", coin.Code, quote)
	}
	updatedAt := p.now()
	if timestamp, err := numberFloat(entry["last_updated_at"]); err == nil && timestamp > 0 {
		updatedAt = time.Unix(int64(timestamp), 0)
	}
	return value, updatedAt, nil
}

func (p *Plugin) fetchCryptoCross(
	ctx context.Context,
	base currency,
	quote currency,
) (float64, time.Time, error) {
	query := url.Values{
		"ids":                     []string{base.ID + "," + quote.ID},
		"vs_currencies":           []string{"usd"},
		"include_last_updated_at": []string{"true"},
	}
	endpoint := "https://api.coingecko.com/api/v3/simple/price?" + query.Encode()
	var payload map[string]map[string]json.Number
	response, err := p.services.HTTP.JSON(ctx, httpclient.Request{URL: endpoint}, &payload)
	if err != nil {
		return 0, time.Time{}, err
	}
	if response.StatusCode != http.StatusOK {
		return 0, time.Time{}, fmt.Errorf("CoinGecko HTTP %d", response.StatusCode)
	}
	baseUSD, baseErr := numberFloat(payload[base.ID]["usd"])
	quoteUSD, quoteErr := numberFloat(payload[quote.ID]["usd"])
	if baseErr != nil || quoteErr != nil || quoteUSD == 0 {
		return 0, time.Time{}, errors.New("CoinGecko 未返回完整的美元基准价")
	}
	updatedAt := p.now()
	if timestamp, err := numberFloat(payload[base.ID]["last_updated_at"]); err == nil && timestamp > 0 {
		updatedAt = time.Unix(int64(timestamp), 0)
	}
	return baseUSD / quoteUSD, updatedAt, nil
}

func numberFloat(number json.Number) (float64, error) {
	if number == "" {
		return 0, errors.New("number is missing")
	}
	return strconv.ParseFloat(string(number), 64)
}

func parseArgs(args []string) (base, quote string, amount float64, err error) {
	amount = 1
	var currencies []string
	for _, argument := range args {
		normalized := normalizeCode(argument)
		if number, parseErr := strconv.ParseFloat(normalized, 64); parseErr == nil {
			if math.IsNaN(number) || math.IsInf(number, 0) || number <= 0 {
				return "", "", 0, errors.New("数量必须是大于零的有限数字")
			}
			amount = number
			continue
		}
		currencies = append(currencies, normalized)
	}
	if len(currencies) == 0 {
		return "", "", 0, errors.New("至少需要一种货币")
	}
	if len(currencies) > 2 {
		return "", "", 0, errors.New("最多只能指定两种货币")
	}
	base = currencies[0]
	quote = "usd"
	if len(currencies) == 2 {
		quote = currencies[1]
	}
	return base, quote, amount, nil
}

func normalizeCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if alias, ok := aliases[value]; ok {
		return alias
	}
	return value
}

func lookupCurrency(code string) (currency, bool) {
	code = normalizeCode(code)
	if info, ok := cryptoCurrencies[code]; ok {
		info.Code = strings.ToUpper(code)
		info.Kind = crypto
		return info, true
	}
	if _, ok := fiatCurrencies[code]; ok {
		return currency{
			Code: strings.ToUpper(code),
			Name: strings.ToUpper(code),
			Kind: fiat,
		}, true
	}
	return currency{}, false
}

func formatResult(base, quote string, amount, rate float64, updatedAt time.Time) string {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	return fmt.Sprintf(
		"<b>💱 汇率换算</b>\n\n"+
			"<code>%s %s</code> ≈ <code>%s %s</code>\n\n"+
			"• 汇率：<code>1 %s = %s %s</code>\n"+
			"• 更新：<code>%s</code>",
		formatAmount(amount),
		html.EscapeString(base),
		formatAmount(amount*rate),
		html.EscapeString(quote),
		html.EscapeString(base),
		formatRate(rate),
		html.EscapeString(quote),
		updatedAt.In(shanghai).Format("2006-01-02 15:04:05"),
	)
}

func formatAmount(value float64) string {
	if value >= 1 {
		return strconv.FormatFloat(value, 'f', 2, 64)
	}
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func formatRate(value float64) string {
	switch {
	case value >= 1:
		return strconv.FormatFloat(value, 'f', 2, 64)
	case value >= 0.01:
		return strconv.FormatFloat(value, 'f', 4, 64)
	case value >= 0.0001:
		return strconv.FormatFloat(value, 'f', 6, 64)
	default:
		return strconv.FormatFloat(value, 'e', 2, 64)
	}
}

func helpText(prefix string) string {
	commandName := html.EscapeString(prefix + "rate")
	return "<b>💱 汇率换算</b>\n\n" +
		"<b>用法</b>\n" +
		"<code>" + commandName + " 基准币 [目标币] [数量]</code>\n\n" +
		"<b>示例</b>\n" +
		"<code>" + commandName + " BTC CNY</code>\n" +
		"<code>" + commandName + " USD CNY 100</code>\n" +
		"<code>" + commandName + " CNY USDT 7000</code>"
}

var aliases = map[string]string{
	"bitcoin":  "btc",
	"ethereum": "eth",
	"tether":   "usdt",
	"rmb":      "cny",
	"yuan":     "cny",
	"cnh":      "cny",
}

var cryptoCurrencies = map[string]currency{
	"btc":   {ID: "bitcoin", Name: "Bitcoin"},
	"eth":   {ID: "ethereum", Name: "Ethereum"},
	"usdt":  {ID: "tether", Name: "Tether"},
	"bnb":   {ID: "binancecoin", Name: "BNB"},
	"sol":   {ID: "solana", Name: "Solana"},
	"usdc":  {ID: "usd-coin", Name: "USD Coin"},
	"xrp":   {ID: "ripple", Name: "XRP"},
	"doge":  {ID: "dogecoin", Name: "Dogecoin"},
	"ton":   {ID: "the-open-network", Name: "Toncoin"},
	"ada":   {ID: "cardano", Name: "Cardano"},
	"shib":  {ID: "shiba-inu", Name: "Shiba Inu"},
	"avax":  {ID: "avalanche-2", Name: "Avalanche"},
	"trx":   {ID: "tron", Name: "TRON"},
	"dot":   {ID: "polkadot", Name: "Polkadot"},
	"link":  {ID: "chainlink", Name: "Chainlink"},
	"matic": {ID: "matic-network", Name: "Polygon"},
	"wbtc":  {ID: "wrapped-bitcoin", Name: "Wrapped Bitcoin"},
	"ltc":   {ID: "litecoin", Name: "Litecoin"},
	"bch":   {ID: "bitcoin-cash", Name: "Bitcoin Cash"},
	"uni":   {ID: "uniswap", Name: "Uniswap"},
	"atom":  {ID: "cosmos", Name: "Cosmos"},
	"etc":   {ID: "ethereum-classic", Name: "Ethereum Classic"},
	"xlm":   {ID: "stellar", Name: "Stellar"},
	"okb":   {ID: "okb", Name: "OKB"},
	"icp":   {ID: "internet-computer", Name: "Internet Computer"},
	"fil":   {ID: "filecoin", Name: "Filecoin"},
	"hbar":  {ID: "hedera-hashgraph", Name: "Hedera"},
	"ldo":   {ID: "lido-dao", Name: "Lido DAO"},
	"crv":   {ID: "curve-dao-token", Name: "Curve DAO Token"},
	"arb":   {ID: "arbitrum", Name: "Arbitrum"},
}

var fiatCurrencies = func() map[string]struct{} {
	values := strings.Fields(`
		usd eur gbp jpy cny cad aud chf nzd sek nok dkk isk pln czk huf ron bgn
		rsd bam mkd all rub uah byn mdl try gel amd azn brl mxn ars cop pen clp
		uyu pyg bob ves gyd srd ttd jmd bbd bsd bzd crc gtq hnl nio pab dop htg
		cup sgd hkd krw inr thb myr php idr vnd lak khr mmk bnd twd mop fjd pgk
		sbd vuv top wst lkr pkr bdt npr btn mvr afn kzt uzs kgs tjs tmt zar ils
		aed sar qar kwd bhd omr jod lbp syp iqd irr yer egp mad dzd tnd lyd sdg
		etb ern djf sos ngn ghs xof sll lrd gmd gnf cve kes ugx tzs rwf bif mzn
		mwk zmw zwl mga mur scr kmf xaf cdf aoa stn bwp nad szl lsl
	`)
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}()
