package command

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

func TestRouterDispatch(t *testing.T) {
	t.Parallel()

	router, err := NewRouter([]string{".", "!!"}, []int64{42})
	if err != nil {
		t.Fatal(err)
	}

	var got Request
	err = router.Register("core", Definition{
		Name:    "echo",
		Aliases: []string{"e"},
		Handler: func(_ context.Context, request Request) error {
			got = request
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := router.Dispatch(context.Background(), telegram.Message{
		SenderID: 42,
		Text:     "!!E hello   world",
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if !result.Matched || result.Plugin != "core" || result.Command != "echo" {
		t.Fatalf("Dispatch() result = %+v", result)
	}
	if got.Command != "e" || got.RawArgs != "hello   world" || !reflect.DeepEqual(got.Args, []string{"hello", "world"}) {
		t.Fatalf("request = %+v", got)
	}
}

func TestRouterOwnerOnly(t *testing.T) {
	t.Parallel()

	router, err := NewRouter([]string{"."}, []int64{42})
	if err != nil {
		t.Fatal(err)
	}
	err = router.Register("core", Definition{
		Name:      "secret",
		OwnerOnly: true,
		Handler:   func(context.Context, Request) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := router.Dispatch(context.Background(), telegram.Message{
		SenderID: 7,
		Text:     ".secret",
	})
	if !result.Matched || !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("Dispatch() result = %+v, error = %v", result, err)
	}
}

func TestRouterRejectsConflicts(t *testing.T) {
	t.Parallel()

	router, err := NewRouter([]string{"."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := func(context.Context, Request) error { return nil }
	if err := router.Register("one", Definition{Name: "ping", Handler: handler}); err != nil {
		t.Fatal(err)
	}
	if err := router.Register("two", Definition{Name: "other", Aliases: []string{"ping"}, Handler: handler}); !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("Register() error = %v, want ErrRouteConflict", err)
	}
}

func TestRouterCanReplacePrefixes(t *testing.T) {
	t.Parallel()

	router, err := NewRouter([]string{"."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.SetPrefixes([]string{"!!", "!"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := router.Parse(telegram.Message{Text: ".ping"}); ok {
		t.Fatal("old prefix still matched")
	}
	request, ok := router.Parse(telegram.Message{Text: "!!ping"})
	if !ok || request.Prefix != "!!" || request.Command != "ping" {
		t.Fatalf("Parse() = %+v, %v", request, ok)
	}
	if err := router.SetPrefixes([]string{"!", "!"}); err == nil {
		t.Fatal("SetPrefixes() accepted duplicate prefixes")
	}
}

func TestRouterPreservesHelpMetadata(t *testing.T) {
	t.Parallel()

	router, err := NewRouter([]string{"-"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.Register("plugin", Definition{
		Name:     "demo",
		Aliases:  []string{"D"},
		Usage:    []string{"  demo one  ", "", "demo two"},
		HelpHTML: "<b>guide</b>",
		Handler:  func(context.Context, Request) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	routes := router.List()
	if len(routes) != 1 {
		t.Fatalf("routes = %#v", routes)
	}
	if !reflect.DeepEqual(routes[0].Aliases, []string{"d"}) ||
		!reflect.DeepEqual(routes[0].Usage, []string{"demo one", "demo two"}) ||
		routes[0].HelpHTML != "<b>guide</b>" {
		t.Fatalf("help metadata = %#v", routes[0])
	}
}

func TestUserAliasRewritesCommandAndPreservesArguments(t *testing.T) {
	t.Parallel()

	router, err := NewRouter([]string{"-"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.SetUserAliases(map[string]string{
		"de":         "bd",
		"fast clean": "bd 20",
	}); err != nil {
		t.Fatal(err)
	}

	request, ok := router.Parse(telegram.Message{Text: "-DE 5"})
	if !ok || request.Command != "bd" || request.RawArgs != "5" ||
		!reflect.DeepEqual(request.Args, []string{"5"}) {
		t.Fatalf("single alias request = %#v, matched %v", request, ok)
	}

	request, ok = router.Parse(telegram.Message{Text: "-fast clean extra"})
	if !ok || request.Command != "bd" || request.RawArgs != "20 extra" ||
		!reflect.DeepEqual(request.Args, []string{"20", "extra"}) {
		t.Fatalf("multi-word alias request = %#v, matched %v", request, ok)
	}
}

func TestUserAliasUsesLongestMatch(t *testing.T) {
	t.Parallel()

	router, err := NewRouter([]string{"-"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.SetUserAliases(map[string]string{
		"do":      "ping",
		"do more": "status",
	}); err != nil {
		t.Fatal(err)
	}
	request, ok := router.Parse(telegram.Message{Text: "-do more now"})
	if !ok || request.Command != "status" || request.RawArgs != "now" {
		t.Fatalf("longest alias request = %#v, matched %v", request, ok)
	}
}

func TestUserAliasRejectsChainingAndReservedCommand(t *testing.T) {
	t.Parallel()

	router, err := NewRouter([]string{"-"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.SetUserAliases(map[string]string{
		"a": "b",
		"b": "ping",
	}); err == nil || !strings.Contains(err.Error(), "chained") {
		t.Fatalf("chained alias error = %v", err)
	}
	if err := router.SetUserAliases(map[string]string{
		"alias": "ping",
	}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved alias error = %v", err)
	}
}

func TestRouterDelegateRespectsChatAllowlist(t *testing.T) {
	t.Parallel()

	router, err := NewRouter([]string{"-"}, []int64{42})
	if err != nil {
		t.Fatal(err)
	}
	router.SetDelegates([]int64{7}, []int64{-100123})
	if err := router.Register("core", Definition{
		Name:      "secret",
		OwnerOnly: true,
		Handler:   func(context.Context, Request) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}

	result, err := router.Dispatch(context.Background(), telegram.Message{
		ID:       1,
		ChatID:   -100123,
		SenderID: 7,
		Text:     "-secret",
	})
	if !result.Matched || err != nil {
		t.Fatalf("allowed delegate result = %+v, error = %v", result, err)
	}

	result, err = router.Dispatch(context.Background(), telegram.Message{
		ID:       2,
		ChatID:   -100999,
		SenderID: 7,
		Text:     "-secret",
	})
	if !result.Matched || !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("blocked delegate result = %+v, error = %v", result, err)
	}
}

func TestRouterSuppressionConsumesExactMessageOnce(t *testing.T) {
	t.Parallel()

	router, err := NewRouter([]string{"-"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := router.Register("core", Definition{
		Name: "ping",
		Handler: func(context.Context, Request) error {
			calls++
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	message := telegram.Message{
		ID:       9,
		ChatID:   -100123,
		SenderID: 7,
		Text:     "-ping",
	}
	router.SuppressNext(message)

	result, err := router.Dispatch(context.Background(), message)
	if err != nil || result.Matched || calls != 0 {
		t.Fatalf("suppressed result = %+v, calls = %d, error = %v", result, calls, err)
	}
	result, err = router.Dispatch(context.Background(), message)
	if err != nil || !result.Matched || calls != 1 {
		t.Fatalf("second result = %+v, calls = %d, error = %v", result, calls, err)
	}

	other := message
	other.ID++
	result, err = router.Dispatch(context.Background(), other)
	if err != nil || !result.Matched || calls != 2 {
		t.Fatalf("other result = %+v, calls = %d, error = %v", result, calls, err)
	}
}
