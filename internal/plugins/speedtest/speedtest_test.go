package speedtest

import (
	"strings"
	"testing"
)

func TestParseAndFormatResult(t *testing.T) {
	input := `notice
	{"isp":"Example","server":{"id":42,"name":"Node","location":"Tokyo"},"interface":{"externalIp":"2001:db8::1","name":"eth0"},"ping":{"latency":12.5,"jitter":1.25},"download":{"bandwidth":12500000,"bytes":50000000},"upload":{"bandwidth":6250000,"bytes":25000000},"timestamp":"2026-07-26T10:00:00Z","result":{"url":"https://www.speedtest.net/result/1"}}`
	got, err := parseResult(input)
	if err != nil {
		t.Fatal(err)
	}
	text := formatResult(got)
	for _, fragment := range []string{"100.00 Mbps", "50.00 Mbps", "IPv6", "42 - Node - Tokyo"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("formatted result missing %q:\n%s", fragment, text)
		}
	}
}

func TestFormatUnits(t *testing.T) {
	if got := formatBandwidth(125000); got != "1.00 Mbps" {
		t.Fatalf("bandwidth = %q", got)
	}
	if got := formatBytes(1500000); got != "1.50 MB" {
		t.Fatalf("bytes = %q", got)
	}
}

func TestRejectsIncompleteResult(t *testing.T) {
	if _, err := parseResult(`{"server":{"id":0}}`); err == nil {
		t.Fatal("incomplete result was accepted")
	}
}
