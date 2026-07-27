package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

var (
	ErrNotStarted = errors.New("scheduler is not started")
	ErrStopped    = errors.New("scheduler is stopped")
	ErrDuplicate  = errors.New("scheduled job already exists")
)

type JobFunc func(context.Context) error

type Schedule interface {
	Next(time.Time) time.Time
}

type IntervalSchedule struct {
	Interval time.Duration
}

func (s IntervalSchedule) Next(after time.Time) time.Time {
	if s.Interval <= 0 {
		return time.Time{}
	}
	return after.Add(s.Interval)
}

type job struct {
	name      string
	schedule  Schedule
	run       JobFunc
	immediate bool
	cancel    context.CancelFunc
	done      chan struct{}
}

type Scheduler struct {
	logger *slog.Logger

	mu      sync.Mutex
	started bool
	stopped bool
	ctx     context.Context
	cancel  context.CancelFunc
	jobs    map[string]*job
	wg      sync.WaitGroup
}

func New(logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		logger: logger,
		jobs:   make(map[string]*job),
	}
}

func (s *Scheduler) Start(parent context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return ErrStopped
	}
	if s.started {
		return nil
	}
	s.ctx, s.cancel = context.WithCancel(parent)
	s.started = true
	return nil
}

func (s *Scheduler) Every(name string, interval time.Duration, runImmediately bool, run JobFunc) error {
	if interval <= 0 {
		return errors.New("interval must be greater than zero")
	}
	return s.Add(name, IntervalSchedule{Interval: interval}, runImmediately, run)
}

func (s *Scheduler) Add(name string, schedule Schedule, runImmediately bool, run JobFunc) error {
	if name == "" {
		return errors.New("job name is required")
	}
	if schedule == nil {
		return errors.New("schedule is required")
	}
	if run == nil {
		return errors.New("job function is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.stopped:
		return ErrStopped
	case !s.started:
		return ErrNotStarted
	}
	if _, exists := s.jobs[name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicate, name)
	}

	jobCtx, cancel := context.WithCancel(s.ctx)
	item := &job{
		name:      name,
		schedule:  schedule,
		run:       run,
		immediate: runImmediately,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	s.jobs[name] = item
	s.wg.Add(1)
	go s.runJob(jobCtx, item)
	return nil
}

func (s *Scheduler) Remove(ctx context.Context, name string) error {
	s.mu.Lock()
	item, exists := s.jobs[name]
	if exists {
		delete(s.jobs, name)
		item.cancel()
	}
	s.mu.Unlock()
	if !exists {
		return nil
	}

	select {
	case <-item.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	s.jobs = make(map[string]*job)
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Scheduler) runJob(ctx context.Context, item *job) {
	defer s.wg.Done()
	defer close(item.done)

	if item.immediate {
		s.execute(ctx, item)
	}
	next := item.schedule.Next(time.Now())
	for !next.IsZero() {
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
			s.execute(ctx, item)
			next = item.schedule.Next(time.Now())
		}
	}
}

func (s *Scheduler) execute(ctx context.Context, item *job) {
	startedAt := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Error("scheduled job panicked",
				"job", item.name,
				"panic", recovered,
				"stack", string(debug.Stack()),
			)
		}
	}()
	if err := item.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Error("scheduled job failed",
			"job", item.name,
			"duration", time.Since(startedAt),
			"error", err,
		)
	}
}
