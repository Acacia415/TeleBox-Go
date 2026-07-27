package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

var (
	ErrNotRunning = errors.New("command pool is not running")
	ErrQueueFull  = errors.New("command queue is full")
)

type Job func(context.Context)

// Pool keeps slow plugin commands away from the MTProto update loop. The queue
// is bounded so a burst cannot grow process memory without limit.
type Pool struct {
	logger  *slog.Logger
	workers int
	queue   chan Job

	mu        sync.Mutex
	started   bool
	accepting bool
	cancel    context.CancelFunc
	done      chan struct{}
	wg        sync.WaitGroup
}

func New(workers, queueCapacity int, logger *slog.Logger) (*Pool, error) {
	if workers <= 0 {
		return nil, errors.New("workers must be greater than zero")
	}
	if queueCapacity < workers {
		return nil, errors.New("queue capacity must be at least the worker count")
	}
	if logger == nil {
		return nil, errors.New("logger is required")
	}
	return &Pool{
		logger:  logger,
		workers: workers,
		queue:   make(chan Job, queueCapacity),
		done:    make(chan struct{}),
	}, nil
}

func (p *Pool) Start(parent context.Context) error {
	if parent == nil {
		return errors.New("parent context is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return errors.New("command pool has already been started")
	}
	runCtx, cancel := context.WithCancel(parent)
	p.started = true
	p.accepting = true
	p.cancel = cancel
	for workerID := 1; workerID <= p.workers; workerID++ {
		p.wg.Add(1)
		go p.worker(runCtx, workerID)
	}
	go func() {
		p.wg.Wait()
		close(p.done)
	}()
	return nil
}

func (p *Pool) Submit(job Job) error {
	if job == nil {
		return errors.New("job is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.accepting {
		return ErrNotRunning
	}
	select {
	case p.queue <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

func (p *Pool) Stop(ctx context.Context) error {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return nil
	}
	if p.accepting {
		p.accepting = false
		p.cancel()
	}
	done := p.done
	p.mu.Unlock()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop command pool: %w", ctx.Err())
	}
}

func (p *Pool) worker(ctx context.Context, workerID int) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-p.queue:
			p.runJob(ctx, workerID, job)
		}
	}
}

func (p *Pool) runJob(ctx context.Context, workerID int, job Job) {
	defer func() {
		if recovered := recover(); recovered != nil {
			p.logger.Error("command handler panicked",
				"worker", workerID,
				"panic", recovered,
			)
		}
	}()
	job(ctx)
}
