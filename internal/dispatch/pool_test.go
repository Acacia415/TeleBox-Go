package dispatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolBoundsQueueAndRecoversPanics(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool, err := New(1, 1, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	if err := pool.Submit(func(context.Context) {
		close(started)
		<-release
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := pool.Submit(func(context.Context) { panic("expected") }); err != nil {
		t.Fatal(err)
	}
	if err := pool.Submit(func(context.Context) {}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("Submit() error = %v, want ErrQueueFull", err)
	}
	close(release)

	var ran atomic.Bool
	deadline := time.Now().Add(time.Second)
	for !ran.Load() && time.Now().Before(deadline) {
		err := pool.Submit(func(context.Context) { ran.Store(true) })
		if err != nil && !errors.Is(err, ErrQueueFull) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if !ran.Load() {
		t.Fatal("worker did not continue after panic")
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := pool.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if err := pool.Submit(func(context.Context) {}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Submit() after Stop = %v, want ErrNotRunning", err)
	}
}
