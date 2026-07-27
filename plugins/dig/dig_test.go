package dig

import (
	"testing"

	"github.com/miekg/dns"
)

func TestParseArgs(t *testing.T) {
	got, err := parseArgs([]string{"example.com", "MX", "@8.8.8.8", "+tcp"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "example.com" || got.Type != dns.TypeMX ||
		got.Server != "8.8.8.8:53" || got.Network != "tcp" {
		t.Fatalf("parsed query = %+v", got)
	}
}

func TestParseArgsRejectsUnsupportedOption(t *testing.T) {
	if _, err := parseArgs([]string{"example.com", "+trace"}); err == nil {
		t.Fatal("+trace was accepted")
	}
}

func TestPortableRecord(t *testing.T) {
	answer, err := dns.NewRR("example.com. 300 IN MX 10 mail.example.com.")
	if err != nil {
		t.Fatal(err)
	}
	got := portableRecord(answer)
	if got.Name != "example.com" || got.Type != "MX" ||
		got.Value != "10 mail.example.com" || got.TTL != 300 {
		t.Fatalf("portable record = %+v", got)
	}
}
