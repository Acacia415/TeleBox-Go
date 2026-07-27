package binlookup

import (
	"strings"
	"testing"
)

func TestFormatPayload(t *testing.T) {
	t.Parallel()

	prepaid := false
	data := payload{
		Scheme:  "mastercard",
		Type:    "credit",
		Brand:   "World Business",
		Prepaid: &prepaid,
	}
	data.Country.Name = "Taiwan, Province of China"
	data.Country.Emoji = "🇹🇼"
	data.Country.Currency = "TWD"
	data.Bank.Name = "Example Company Limited"
	text := formatPayload("123456", data)
	for _, expected := range []string{
		"Master Card",
		"贷记",
		"WORLD",
		"🇹🇼 Taiwan",
		"新台币",
		"EXAMPLE CO., LTD.",
		"商业卡：✓",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("formatPayload() = %q, missing %q", text, expected)
		}
	}
}

func TestParseBincheck(t *testing.T) {
	t.Parallel()

	got := parseBincheck([]byte(
		`<meta property="og:description" content="Visa - Example Bank - Taiwan">`,
	))
	if got.Scheme != "visa" || got.Bank != "Example Bank" || got.Country != "Taiwan" {
		t.Fatalf("parseBincheck() = %#v", got)
	}
}
