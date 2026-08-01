package gotd

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestNewRequiresAuthenticatorForPhoneLogin(t *testing.T) {
	t.Parallel()

	_, err := New(Config{
		APIID:       1,
		APIHash:     "hash",
		SessionFile: t.TempDir() + "/session.json",
		LoginMode:   "phone",
	})
	if err == nil ||
		err.Error() != "phone login requested but no phone authenticator is configured" {
		t.Fatalf("New() error = %v", err)
	}

	_, err = New(Config{
		APIID:       1,
		APIHash:     "hash",
		SessionFile: t.TempDir() + "/session.json",
		LoginMode:   "phone",
		PhoneAuth: auth.Constant(
			"+8613812345678",
			"password",
			auth.CodeAuthenticatorFunc(func(context.Context, *tg.AuthSentCode) (string, error) {
				return "12345", nil
			}),
		),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
}

func TestPeerIDUsesTDLibConvention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		peer tg.PeerClass
		want int64
	}{
		{name: "user", peer: &tg.PeerUser{UserID: 42}, want: 42},
		{name: "chat", peer: &tg.PeerChat{ChatID: 42}, want: -42},
		{name: "channel", peer: &tg.PeerChannel{ChannelID: 42}, want: -1000000000042},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := peerID(test.peer)
			if !ok || got != test.want {
				t.Fatalf("peerID() = %d, %v; want %d, true", got, ok, test.want)
			}
		})
	}
}

func TestCachedInputUserKeepsAccessHash(t *testing.T) {
	t.Parallel()

	client := &Client{peerCache: map[int64]tg.InputPeerClass{
		42: &tg.InputPeerUser{UserID: 42, AccessHash: 4200},
	}}
	input, ok := client.cachedInputUser(42)
	if !ok {
		t.Fatal("cachedInputUser() did not find the update entity")
	}
	user, ok := input.(*tg.InputUser)
	if !ok || user.UserID != 42 || user.AccessHash != 4200 {
		t.Fatalf("cachedInputUser() = %#v, want user 42 with access hash 4200", input)
	}
}

func TestResolvePrivateUserUsesCachedPeer(t *testing.T) {
	t.Parallel()

	want := &tg.InputPeerUser{UserID: 42, AccessHash: 4200}
	client := &Client{peerCache: map[int64]tg.InputPeerClass{42: want}}
	got, err := client.resolvePrivateUser(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolvePrivateUser() = %#v, want cached peer %#v", got, want)
	}
}

func TestHandleMessageUsesCachedPeerWhenUpdateOmitsEntities(t *testing.T) {
	t.Parallel()

	received := make(chan teleboxtelegram.Message, 1)
	client := &Client{
		peerCache: map[int64]tg.InputPeerClass{
			42: &tg.InputPeerUser{UserID: 42, AccessHash: 4200},
		},
		handler: func(_ context.Context, message teleboxtelegram.Message) error {
			received <- message
			return nil
		},
	}
	raw := &tg.Message{
		ID:      7,
		PeerID:  &tg.PeerUser{UserID: 42},
		FromID:  &tg.PeerUser{UserID: 42},
		Message: "12",
	}
	if err := client.handleMessage(
		context.Background(),
		tg.Entities{},
		raw,
		false,
	); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-received:
		if message.ChatID != 42 || message.SenderID != 42 || message.Text != "12" {
			t.Fatalf("received message = %+v", message)
		}
	default:
		t.Fatal("cached repeated private message was not delivered")
	}
}

func TestDeviceAppVersion(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"v0.2.2": "0.2.2",
		"0.2.2":  "0.2.2",
		"":       "dev",
	}
	for input, want := range tests {
		if got := deviceAppVersion(input); got != want {
			t.Fatalf("deviceAppVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestInputPeerRequiresAccessHashEntities(t *testing.T) {
	t.Parallel()

	userEntity := &tg.User{ID: 7}
	userEntity.SetAccessHash(70)
	channelEntity := &tg.Channel{ID: 8}
	channelEntity.SetAccessHash(80)
	entities := tg.Entities{
		Users: map[int64]*tg.User{
			7: userEntity,
		},
		Channels: map[int64]*tg.Channel{
			8: channelEntity,
		},
	}

	userPeer, err := inputPeer(entities, &tg.PeerUser{UserID: 7})
	if err != nil {
		t.Fatal(err)
	}
	user, ok := userPeer.(*tg.InputPeerUser)
	if !ok || user.UserID != 7 || user.AccessHash != 70 {
		t.Fatalf("user peer = %#v", userPeer)
	}

	channelPeer, err := inputPeer(entities, &tg.PeerChannel{ChannelID: 8})
	if err != nil {
		t.Fatal(err)
	}
	channel, ok := channelPeer.(*tg.InputPeerChannel)
	if !ok || channel.ChannelID != 8 || channel.AccessHash != 80 {
		t.Fatalf("channel peer = %#v", channelPeer)
	}

	_, err = inputPeer(tg.Entities{}, &tg.PeerChannel{ChannelID: 9})
	if !errors.Is(err, teleboxtelegram.ErrPeerNotResolved) {
		t.Fatalf("inputPeer() error = %v, want ErrPeerNotResolved", err)
	}
}
