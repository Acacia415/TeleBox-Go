package rate

import (
	"strings"
	"testing"
	"time"
)

func TestParseArgs(t *testing.T) {
	t.Parallel()

	base, quote, amount, err := parseArgs([]string{"CNY", "7000", "tether"})
	if err != nil {
		t.Fatal(err)
	}
	if base != "cny" || quote != "usdt" || amount != 7000 {
		t.Fatalf("parseArgs() = %q, %q, %v", base, quote, amount)
	}
	if _, _, _, err := parseArgs([]string{"BTC", "-1"}); err == nil {
		t.Fatal("parseArgs() accepted negative amount")
	}
}

func TestLookupCurrencyAndFormatting(t *testing.T) {
	t.Parallel()

	cny, ok := lookupCurrency("rmb")
	if !ok || cny.Kind != fiat || cny.Code != "CNY" {
		t.Fatalf("lookupCurrency(rmb) = %+v, %v", cny, ok)
	}
	btc, ok := lookupCurrency("bitcoin")
	if !ok || btc.Kind != crypto || btc.ID != "bitcoin" {
		t.Fatalf("lookupCurrency(bitcoin) = %+v, %v", btc, ok)
	}
	text := formatResult("BTC", "CNY", 0.5, 500000, time.Unix(0, 0))
	for _, expected := range []string{"0.500000 BTC", "250000.00 CNY", "1 BTC = 500000.00 CNY"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("formatResult() = %q, missing %q", text, expected)
		}
	}
}

func TestFiatValueSupportsOriginalUpstreamShapes(t *testing.T) {
	t.Parallel()

	for _, payload := range []map[string]any{
		{"rates": map[string]any{"CNY": 7.25}},
		{"data": map[string]any{"rates": map[string]any{"CNY": "7.25"}}},
		{"usd": map[string]any{"cny": 7.25}},
	} {
		value, ok := fiatValue(payload, "usd", "CNY")
		if !ok || value != 7.25 {
			t.Fatalf("fiatValue(%#v) = %v, %v", payload, value, ok)
		}
	}
}
