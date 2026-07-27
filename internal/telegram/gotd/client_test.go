package gotd

import (
	"errors"
	"testing"

	"github.com/gotd/td/tg"

	teleboxtelegram "github.com/Acacia415/TeleBox-Go/internal/telegram"
)

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
