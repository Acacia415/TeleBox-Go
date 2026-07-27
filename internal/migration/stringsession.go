package migration

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	gotdcrypto "github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
)

type StringSessionFormat string

const (
	StringSessionGramJS   StringSessionFormat = "gramjs"
	StringSessionTelethon StringSessionFormat = "telethon"
)

// ParseStringSession converts a Telethon or GramJS/teleproto StringSession into
// gotd session data without touching the network. The auth key is never
// rendered or logged by this package.
func ParseStringSession(value string) (*session.Data, StringSessionFormat, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, "", errors.New("string session is empty")
	}

	// GramJS payloads with a 14-byte textual IPv4 address happen to have the
	// same decoded length as a Telethon IPv6 payload. Detect the explicit
	// GramJS address-length field before falling back to Telethon.
	if data, err := parseGramJSStringSession(value); err == nil {
		return data, StringSessionGramJS, nil
	}
	data, err := session.TelethonSession(value)
	if err != nil {
		return nil, "", fmt.Errorf("unsupported string session: %w", err)
	}
	return data, StringSessionTelethon, nil
}

func parseGramJSStringSession(value string) (*session.Data, error) {
	if len(value) < 2 {
		return nil, errors.New("GramJS string session is too short")
	}
	if value[0] != '1' {
		return nil, fmt.Errorf("unsupported GramJS string session version %q", value[0])
	}

	decoded, err := decodeBase64(value[1:])
	if err != nil {
		return nil, fmt.Errorf("decode GramJS string session: %w", err)
	}
	const fixedSize = 1 + 2 + 2 + 256
	if len(decoded) < fixedSize+1 {
		return nil, fmt.Errorf("decoded GramJS string session is too short: %d", len(decoded))
	}

	dcID := int(decoded[0])
	if dcID <= 0 {
		return nil, fmt.Errorf("invalid GramJS data center ID %d", dcID)
	}
	addressLength := int(binary.BigEndian.Uint16(decoded[1:3]))
	expectedLength := fixedSize + addressLength
	if addressLength <= 0 || expectedLength != len(decoded) {
		return nil, fmt.Errorf(
			"invalid GramJS server address length %d for payload length %d",
			addressLength,
			len(decoded),
		)
	}

	addressStart := 3
	addressEnd := addressStart + addressLength
	address := string(decoded[addressStart:addressEnd])
	if strings.TrimSpace(address) == "" || strings.ContainsAny(address, "\x00\r\n") {
		return nil, errors.New("invalid GramJS server address")
	}
	port := int(binary.BigEndian.Uint16(decoded[addressEnd : addressEnd+2]))
	if port <= 0 {
		return nil, fmt.Errorf("invalid GramJS server port %d", port)
	}

	authKeyBytes := decoded[addressEnd+2:]
	if len(authKeyBytes) != 256 {
		return nil, fmt.Errorf("invalid GramJS auth key length %d", len(authKeyBytes))
	}
	var authKey gotdcrypto.Key
	copy(authKey[:], authKeyBytes)
	authKeyID := authKey.WithID().ID

	host := address
	if parsed := net.ParseIP(address); parsed != nil {
		host = parsed.String()
	}
	return &session.Data{
		DC:        dcID,
		Addr:      net.JoinHostPort(host, strconv.Itoa(port)),
		AuthKey:   append([]byte(nil), authKey[:]...),
		AuthKeyID: append([]byte(nil), authKeyID[:]...),
	}, nil
}

func decodeBase64(value string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
