package plugin

import (
	"context"
	"errors"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type fakePlugin struct {
	name       string
	started    int
	stopped    int
	startError error
}

func (p *fakePlugin) Metadata() Metadata {
	return Metadata{Name: p.name, Version: "test"}
}

func (p *fakePlugin) Commands() []command.Definition {
	return []command.Definition{{
		Name:    p.name,
		Handler: func(context.Context, command.Request) error { return nil },
	}}
}

func (p *fakePlugin) Start(context.Context) error {
	p.started++
	return p.startError
}

func (p *fakePlugin) Stop(context.Context) error {
	p.stopped++
	return nil
}

func TestRegistryLifecycle(t *testing.T) {
	t.Parallel()

	router, err := command.NewRouter([]string{"."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(router)
	candidate := &fakePlugin{name: "test"}
	if err := registry.Add(candidate); err != nil {
		t.Fatal(err)
	}
	if err := registry.Enable(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}

	result, err := router.Dispatch(context.Background(), telegram.Message{Text: ".test"})
	if err != nil || !result.Matched {
		t.Fatalf("Dispatch() result = %+v, error = %v", result, err)
	}
	if err := registry.Disable(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	result, err = router.Dispatch(context.Background(), telegram.Message{Text: ".test"})
	if err != nil || result.Matched {
		t.Fatalf("disabled plugin still matched: %+v, error = %v", result, err)
	}
	if candidate.started != 1 || candidate.stopped != 1 {
		t.Fatalf("lifecycle counts = start:%d stop:%d", candidate.started, candidate.stopped)
	}
}

func TestRegistryRollsBackFailedStart(t *testing.T) {
	t.Parallel()

	router, err := command.NewRouter([]string{"."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(router)
	candidate := &fakePlugin{name: "broken", startError: errors.New("boom")}
	if err := registry.Add(candidate); err != nil {
		t.Fatal(err)
	}
	if err := registry.Enable(context.Background(), "broken"); err == nil {
		t.Fatal("Enable() error = nil, want failure")
	}
	status := registry.List()
	if len(status) != 1 || status[0].Enabled {
		t.Fatalf("status = %+v, want disabled", status)
	}
}
