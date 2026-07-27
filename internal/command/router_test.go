package command

import (
	"context"
	"errors"
	"reflect"
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
