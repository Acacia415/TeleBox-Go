package pmcaptcha

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

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
	mu                sync.Mutex
	nextID            int
	quarantined       map[int64]bool
	blocked           map[int64]bool
	deleted           [][]int
	reported          []int64
	operations        []string
	unarchiveOnSend   bool
	unarchiveOnDelete bool
}

func (c *testTelegram) SendText(
	_ context.Context,
	chatID int64,
	_ string,
) (telegram.SentMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	c.operations = append(c.operations, "send_text")
	if c.unarchiveOnSend {
		c.quarantined[chatID] = false
	}
	return telegram.SentMessage{ChatID: chatID, MessageID: c.nextID}, nil
}

func (c *testTelegram) DeleteMessages(
	_ context.Context,
	chatID int64,
	messageIDs []int,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, append([]int(nil), messageIDs...))
	c.operations = append(c.operations, "delete_messages")
	if c.unarchiveOnDelete {
		c.quarantined[chatID] = false
	}
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
	c.operations = append(
		c.operations,
		fmt.Sprintf("quarantine:%t", enabled),
	)
	return nil
}

func (c *testTelegram) BlockUser(_ context.Context, userID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked[userID] = true
	c.operations = append(c.operations, "block")
	return nil
}

func (c *testTelegram) UnblockUser(_ context.Context, userID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked[userID] = false
	return nil
}

func (c *testTelegram) ReportSpam(_ context.Context, userID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reported = append(c.reported, userID)
	c.operations = append(c.operations, "report")
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
	p := New(service.Container{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Telegram: client,
		Storage:  store,
	})
	p.state.Config.Silent = true
	p.runCtx, p.cancel = context.WithCancel(context.Background())
	t.Cleanup(p.cancel)
	return p, client
}

func TestPunishBanRestoresArchiveAfterFailureNotice(t *testing.T) {
	t.Parallel()
	p, client := newGuardTestPlugin(t)
	const userID = int64(44)
	client.unarchiveOnSend = true

	err := p.punish(
		context.Background(),
		userID,
		Config{Action: "ban", Report: true, Silent: false},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if !client.blocked[userID] {
		t.Fatal("action=ban did not block the user")
	}
	if !client.quarantined[userID] {
		t.Fatal("action=ban did not restore the final archive state")
	}
	if len(client.reported) != 1 || client.reported[0] != userID {
		t.Fatalf("reported users = %v", client.reported)
	}
	want := []string{"send_text", "report", "block", "quarantine:true"}
	if fmt.Sprint(client.operations) != fmt.Sprint(want) {
		t.Fatalf("operations = %v, want %v", client.operations, want)
	}
}

func TestPunishActionsAreLoggedWithoutDetailedLogs(t *testing.T) {
	t.Parallel()
	p, _ := newGuardTestPlugin(t)
	var output bytes.Buffer
	p.services.Logger = slog.New(slog.NewTextHandler(&output, nil))

	if err := p.punish(
		context.Background(),
		45,
		Config{Action: "ban", Report: true, Silent: true},
		true,
	); err != nil {
		t.Fatal(err)
	}

	logs := output.String()
	for _, operation := range []string{
		"report_spam",
		"block_user",
		"final_archive",
	} {
		if !strings.Contains(logs, "operation="+operation) {
			t.Fatalf("logs do not contain %q: %s", operation, logs)
		}
	}
}

func TestArchiveRetriesRestoreDelayedFinalState(t *testing.T) {
	t.Parallel()
	p, client := newGuardTestPlugin(t)
	const userID = int64(46)

	client.mu.Lock()
	client.quarantined[userID] = false
	client.mu.Unlock()
	p.scheduleArchiveRetries(
		userID,
		[]time.Duration{time.Millisecond, 2 * time.Millisecond},
	)

	deadline := time.Now().Add(time.Second)
	for {
		client.mu.Lock()
		archived := client.quarantined[userID]
		operations := append([]string(nil), client.operations...)
		client.mu.Unlock()
		if archived {
			if !containsString(operations, "quarantine:true") {
				t.Fatalf("operations = %v", operations)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("archive retry did not converge: %v", operations)
		}
		time.Sleep(time.Millisecond)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
