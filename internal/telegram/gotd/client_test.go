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
