package iplookup

import (
	"context"
	"strings"
	"testing"
)

func TestExtractTarget(t *testing.T) {
	tests := map[string]string{
		"IPv4":       "地址是 8.8.8.8，供测试",
		"IPv6":       "try 2001:4860:4860::8888 now",
		"domain":     "visit docs.example.com/path",
		"invalid IP": "bad 999.999.999.999",
	}
	want := map[string]string{
		"IPv4":   "8.8.8.8",
		"IPv6":   "2001:4860:4860::8888",
		"domain": "docs.example.com",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if got := extractTarget(input); got != want[name] {
				t.Fatalf("extractTarget(%q) = %q, want %q", input, got, want[name])
			}
		})
	}
}

func TestResolveTargetAcceptsLiteralIP(t *testing.T) {
	got, err := resolveTarget(context.Background(), "[2001:4860:4860::8888]")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2001:4860:4860::8888" {
		t.Fatalf("resolved address = %q", got)
	}
}

func TestFormatPayload(t *testing.T) {
	var data payload
	data.Success = true
	data.IP = "8.8.8.8"
	data.Country = "美国"
	data.Region = "加利福尼亚州"
	data.City = "山景城"
	data.Flag.Emoji = "🇺🇸"
	data.Connection.ASN = 15169
	data.Connection.ISP = "Google LLC"
	data.Connection.Org = "Google LLC"
	data.Timezone.ID = "America/Los_Angeles"

	got := formatPayload("dns.google", data)
	for _, fragment := range []string{
		"dns.google",
		"8.8.8.8",
		"美国 - 加利福尼亚州 - 山景城",
		"AS15169",
		"https://bgp.he.net/AS15169",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("formatted result missing %q:\n%s", fragment, got)
		}
	}
}
