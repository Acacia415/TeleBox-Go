package pmcaptcha

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type testStorage struct {
	service.Storage
	mu     sync.Mutex
	values map[string][]byte
}

func (s *testStorage) Get(
	_ context.Context,
	namespace string,
	key string,
) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[namespace+"/"+key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *testStorage) Put(
	_ context.Context,
	namespace string,
	key string,
	value []byte,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[namespace+"/"+key] = append([]byte(nil), value...)
	return nil
}

type testTelegram struct {
	telegram.Client
	mu          sync.Mutex
	nextID      int
	quarantined map[int64]bool
	blocked     map[int64]bool
	deleted     [][]int
}

func (c *testTelegram) SendText(
	_ context.Context,
	chatID int64,
	_ string,
) (telegram.SentMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return telegram.SentMessage{ChatID: chatID, MessageID: c.nextID}, nil
}

func (c *testTelegram) DeleteMessages(
	_ context.Context,
	_ int64,
	messageIDs []int,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, append([]int(nil), messageIDs...))
	return nil
}

func (c *testTelegram) SetPrivateChatQuarantined(
	_ context.Context,
	userID int64,
	enabled bool,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.quarantined[userID] = enabled
	return nil
}

func (c *testTelegram) BlockUser(_ context.Context, userID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked[userID] = true
	return nil
}

func (c *testTelegram) UnblockUser(_ context.Context, userID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked[userID] = false
	return nil
}

func (c *testTelegram) ReportSpam(context.Context, int64) error {
	return nil
}

func (c *testTelegram) DeletePrivateHistory(context.Context, int64) error {
	return nil
}

func newGuardTestPlugin(t *testing.T) (*Plugin, *testTelegram) {
	t.Helper()
	client := &testTelegram{
		quarantined: make(map[int64]bool),
		blocked:     make(map[int64]bool),
	}
	store := &testStorage{values: make(map[string][]byte)}
	p := New(service.Container{Telegram: client, Storage: store})
	p.state.Config.Silent = true
	p.runCtx, p.cancel = context.WithCancel(context.Background())
	t.Cleanup(p.cancel)
	return p, client
}

func TestMathChallengeCompletesAndPersists(t *testing.T) {
	t.Parallel()
	p, client := newGuardTestPlugin(t)
	const userID = int64(42)

	if err := p.startChallenge(
		context.Background(),
		userID,
		"math",
		true,
	); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	challenge, exists := p.state.Challenges[verifiedKey(userID)]
	p.mu.Unlock()
	if !exists || challenge.Type != "math" || len(challenge.MessageIDs) != 1 {
		t.Fatalf("challenge = %+v, exists=%t", challenge, exists)
	}
	if err := p.verifyChallenge(
		context.Background(),
		telegram.Message{
			ID:       99,
			ChatID:   userID,
			SenderID: userID,
			Text:     fmt.Sprintf("答案：%d", challenge.Answer),
		},
		challenge,
	); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	_, verified := p.state.Verified[verifiedKey(userID)]
	_, pending := p.state.Challenges[verifiedKey(userID)]
	stats := p.state.Stats
	p.mu.Unlock()
	if !verified || pending || stats.Passed != 1 {
		t.Fatalf(
			"verified=%t pending=%t stats=%+v",
			verified,
			pending,
			stats,
		)
	}
	client.mu.Lock()
	quarantined := client.quarantined[userID]
	client.mu.Unlock()
	if quarantined {
		t.Fatal("successful user remained quarantined")
	}
}

func TestStickerChallengeRejectsPlainText(t *testing.T) {
	t.Parallel()
	p, client := newGuardTestPlugin(t)
	p.state.Config.Action = "none"
	const userID = int64(43)

	if err := p.startChallenge(
		context.Background(),
		userID,
		"sticker",
		true,
	); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	challenge := p.state.Challenges[verifiedKey(userID)]
	p.mu.Unlock()
	if err := p.verifyChallenge(
		context.Background(),
		telegram.Message{
			ID:       100,
			ChatID:   userID,
			SenderID: userID,
			Text:     "这不是贴纸",
		},
		challenge,
	); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	_, pending := p.state.Challenges[verifiedKey(userID)]
	stats := p.state.Stats
	p.mu.Unlock()
	if pending || stats.Banned != 1 {
		t.Fatalf("pending=%t stats=%+v", pending, stats)
	}
	client.mu.Lock()
	quarantined := client.quarantined[userID]
	client.mu.Unlock()
	if quarantined {
		t.Fatal("action=none user remained quarantined")
	}
}

func TestImageCaptchaResultMustBeOutgoing(t *testing.T) {
	t.Parallel()

	plugin := &Plugin{imageBotID: 42}
	if plugin.isImageCaptchaMessage(telegram.Message{
		ViaBotID: 42,
		Outgoing: false,
	}) {
		t.Fatal("incoming inline message accepted as an image captcha result")
	}
	if !plugin.isImageCaptchaMessage(telegram.Message{
		ViaBotID: 42,
		Outgoing: true,
	}) {
		t.Fatal("outgoing image captcha result was rejected")
	}
}

func TestStartDoesNotRequireTelegramTransport(t *testing.T) {
	t.Parallel()

	p := New(service.Container{Storage: &testStorage{
		values: make(map[string][]byte),
	}})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start() required Telegram before its transport was ready: %v", err)
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
