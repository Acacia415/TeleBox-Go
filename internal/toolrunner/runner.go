package toolrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

var ErrExecutableNotFound = errors.New("executable not found")

type Command struct {
	Name      string
	Args      []string
	Directory string
	Env       []string
	Timeout   time.Duration
	MaxOutput int
}

type Result struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	StdoutTruncated bool
	StderrTruncated bool
	Duration        time.Duration
}

type Runner struct {
	slots chan struct{}
}

func New(maxConcurrent int) (*Runner, error) {
	if maxConcurrent <= 0 {
		return nil, errors.New("maxConcurrent must be greater than zero")
	}
	return &Runner{slots: make(chan struct{}, maxConcurrent)}, nil
}

func (r *Runner) LookPath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrExecutableNotFound, name)
	}
	return path, nil
}

func (r *Runner) Run(ctx context.Context, request Command) (Result, error) {
	if request.Name == "" {
		return Result{}, errors.New("command name is required")
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}

	runCtx := ctx
	cancel := func() {}
	if request.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, request.Timeout)
	}
	defer cancel()

	maxOutput := request.MaxOutput
	if maxOutput <= 0 {
		maxOutput = 1 << 20
	}
	stdout := newLimitedBuffer(maxOutput)
	stderr := newLimitedBuffer(maxOutput)

	command := exec.CommandContext(runCtx, request.Name, request.Args...)
	command.Dir = request.Directory
	if len(request.Env) > 0 {
		command.Env = append(os.Environ(), request.Env...)
	}
	command.Stdout = stdout
	command.Stderr = stderr

	startedAt := time.Now()
	err := command.Run()
	result := Result{
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		ExitCode:        -1,
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
		Duration:        time.Since(startedAt),
	}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if runCtx.Err() != nil {
		return result, fmt.Errorf("run %q: %w", request.Name, runCtx.Err())
	}
	if err != nil {
		return result, fmt.Errorf("run %q: %w", request.Name, err)
	}
	return result, nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || originalLength > 0
		return originalLength, nil
	}
	if len(data) > remaining {
		b.truncated = true
		data = data[:remaining]
	}
	_, _ = b.buffer.Write(data)
	return originalLength, nil
}

func (b *limitedBuffer) String() string {
	return b.buffer.String()
}

func (b *limitedBuffer) Truncated() bool {
	return b.truncated
}
