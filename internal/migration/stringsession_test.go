package migration

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"net"
	"testing"
)

func TestParseGramJSStringSession(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x5a}, 256)
	address := "149.154.167.50"
	payload := make([]byte, 0, 1+2+len(address)+2+len(key))
	payload = append(payload, 2)
	addressLength := make([]byte, 2)
	binary.BigEndian.PutUint16(addressLength, uint16(len(address)))
	payload = append(payload, addressLength...)
	payload = append(payload, address...)
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, 443)
	payload = append(payload, port...)
	payload = append(payload, key...)
	encoded := "1" + base64.StdEncoding.EncodeToString(payload)

	data, format, err := ParseStringSession(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if format != StringSessionGramJS {
		t.Fatalf("format = %q, want gramjs", format)
	}
	if data.DC != 2 || data.Addr != net.JoinHostPort(address, "443") {
		t.Fatalf("session data = DC:%d Addr:%q", data.DC, data.Addr)
	}
	if !bytes.Equal(data.AuthKey, key) || len(data.AuthKeyID) != 8 {
		t.Fatal("auth key was not converted correctly")
	}
}

func TestParseStringSessionRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	if _, _, err := ParseStringSession("1not-base64"); err == nil {
		t.Fatal("ParseStringSession() accepted malformed input")
	}
}
