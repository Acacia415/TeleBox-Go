package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gotd/td/telegram/auth"
	"rsc.io/qr"
)

var _ auth.UserAuthenticator = (*terminalAuthenticator)(nil)

func TestTerminalAuthenticatorPhoneAndCode(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	authenticator := newTerminalAuthenticator(
		strings.NewReader("123\n+8613812345678\nabc\n12345\n"),
		&output,
	)

	phone, err := authenticator.Phone(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if phone != "+8613812345678" {
		t.Fatalf("Phone() = %q", phone)
	}
	code, err := authenticator.Code(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != "12345" {
		t.Fatalf("Code() = %q", code)
	}
	if !strings.Contains(output.String(), "手机号格式不正确") ||
		!strings.Contains(output.String(), "验证码应为数字") {
		t.Fatalf("validation messages missing from %q", output.String())
	}
}

func TestTerminalAuthenticatorPasswordFallbackPreservesSpaces(t *testing.T) {
	t.Parallel()

	authenticator := newTerminalAuthenticator(
		strings.NewReader("  password with spaces  \n"),
		&bytes.Buffer{},
	)
	password, err := authenticator.Password(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if password != "  password with spaces  " {
		t.Fatalf("Password() = %q", password)
	}
}

func TestRenderTerminalQRCodeIncludesQuietZoneAndReset(t *testing.T) {
	t.Parallel()

	code, err := qr.Encode("tg://login?token=test", qr.M)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	renderTerminalQRCode(&output, code)

	rendered := output.String()
	if !strings.Contains(rendered, "\x1b[30;47m") ||
		!strings.Contains(rendered, "\x1b[0m") {
		t.Fatalf("terminal colors missing from rendered QR")
	}
	if lines := strings.Count(rendered, "\n"); lines != (code.Size+8+1)/2 {
		t.Fatalf("rendered line count = %d", lines)
	}
}
