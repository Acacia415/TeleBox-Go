package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestSchedulerRunsAndRemovesJob(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	instance := New(logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := instance.Start(ctx); err != nil {
		t.Fatal(err)
	}

	called := make(chan struct{}, 2)
	if err := instance.Every("test", time.Hour, true, func(context.Context) error {
		called <- struct{}{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("job did not run immediately")
	}

	removeCtx, removeCancel := context.WithTimeout(context.Background(), time.Second)
	defer removeCancel()
	if err := instance.Remove(removeCtx, "test"); err != nil {
		t.Fatal(err)
	}
	if err := instance.Stop(removeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerRejectsInvalidLifecycle(t *testing.T) {
	t.Parallel()

	instance := New(nil)
	run := func(context.Context) error { return nil }
	if err := instance.Every("test", time.Second, false, run); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Every() error = %v, want ErrNotStarted", err)
	}
	if err := instance.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := instance.Every("test", time.Second, false, run); err != nil {
		t.Fatal(err)
	}
	if err := instance.Every("test", time.Second, false, run); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Every() error = %v, want ErrDuplicate", err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := instance.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if err := instance.Every("other", time.Second, false, run); !errors.Is(err, ErrStopped) {
		t.Fatalf("Every() after Stop error = %v, want ErrStopped", err)
	}
}
