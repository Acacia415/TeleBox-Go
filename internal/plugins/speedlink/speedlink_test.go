package speedlink

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseConnection(t *testing.T) {
	for _, test := range []struct {
		input string
		host  string
		port  int
	}{
		{"root@example.com:22", "example.com", 22},
		{"root@[2001:db8::1]:2222", "2001:db8::1", 2222},
	} {
		user, host, port, err := parseConnection(test.input)
		if err != nil {
			t.Fatal(err)
		}
		if user != "root" || host != test.host || port != test.port {
			t.Fatalf("connection = %q, %q, %d", user, host, port)
		}
	}
}

func TestCredentialRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	encrypted, err := encryptCredential(key, "very secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "very secret") {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := decryptCredential(key, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if got != "very secret" {
		t.Fatalf("decrypted = %q", got)
	}
}

func TestParseSpeedResult(t *testing.T) {
	got, err := parseSpeedResult(`notice
{"isp":"Example","server":{"id":1,"name":"Node","location":"Tokyo"},"interface":{"externalIp":"1.2.3.4","name":"eth0"},"ping":{"latency":10,"jitter":1},"download":{"bandwidth":125000},"upload":{"bandwidth":62500},"timestamp":"2026-07-26T00:00:00Z"}`)
	if err != nil {
		t.Fatal(err)
	}
	text := formatSpeedResult("Tokyo", got)
	for _, fragment := range []string{"Tokyo", "1.00 Mbps", "500.00 Kbps"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("result missing %q:\n%s", fragment, text)
		}
	}
}

func TestRedactHost(t *testing.T) {
	if got := redactHost("1.2.3.4"); strings.Contains(got, "1.2.3.4") {
		t.Fatalf("IP leaked: %q", got)
	}
	if got := redactHost("node.example.com"); got != "***.example.com" {
		t.Fatalf("hostname redaction = %q", got)
	}
}
