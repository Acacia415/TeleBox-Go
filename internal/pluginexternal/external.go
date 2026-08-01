package pluginexternal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/pluginbridge"
	"github.com/Acacia415/TeleBox-Go/internal/pluginmarket"
	"github.com/Acacia415/TeleBox-Go/internal/pluginrpc"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type External struct {
	installed pluginmarket.Installed
	services  service.Container
	logger    *slog.Logger
	workDir   string

	mu      sync.Mutex
	command *exec.Cmd
	peer    *pluginrpc.Peer
	stdin   io.WriteCloser
	done    chan struct{}
	waitErr error
}

func New(
	installed pluginmarket.Installed,
	services service.Container,
	workRoot string,
) (*External, error) {
	if err := installed.Manifest.Validate(); err != nil {
		return nil, err
	}
	if installed.Executable == "" {
		return nil, errors.New("external plugin executable is required")
	}
	if strings.TrimSpace(workRoot) == "" {
		return nil, errors.New("external plugin work root is required")
	}
	logger := services.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &External{
		installed: installed,
		services:  services,
		logger:    logger.With("plugin", installed.Manifest.Name),
		workDir:   filepath.Join(workRoot, installed.Manifest.Name),
	}, nil
}

func (p *External) Metadata() plugin.Metadata {
	return plugin.Metadata{
		Name:        p.installed.Manifest.Name,
		Version:     strings.TrimPrefix(p.installed.Manifest.Version, "v"),
		Description: p.installed.Manifest.Description,
	}
}

func (p *External) Commands() []command.Definition {
	result := make([]command.Definition, 0, len(p.installed.Manifest.Commands))
	for _, specification := range p.installed.Manifest.Commands {
		specification := specification
		result = append(result, command.Definition{
			Name:        specification.Name,
			Aliases:     append([]string(nil), specification.Aliases...),
			Description: specification.Description,
			Usage:       append([]string(nil), specification.Usage...),
			HelpHTML:    specification.HelpHTML,
			OwnerOnly:   specification.OwnerOnly,
			Handler: func(ctx context.Context, request command.Request) error {
				return p.call(ctx, pluginbridge.MethodPluginHandle, pluginbridge.Invocation{
					Command: specification.Name,
					Request: request,
				}, nil)
			},
		})
	}
	return result
}

func (p *External) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.command != nil {
		if !channelClosed(p.done) {
			return nil
		}
		p.clearExitedLocked()
	}
	return p.startLocked(ctx)
}

func (p *External) Stop(ctx context.Context) error {
	p.mu.Lock()
	if p.command == nil {
		p.mu.Unlock()
		return nil
	}
	peer := p.peer
	stdin := p.stdin
	command := p.command
	done := p.done
	p.mu.Unlock()

	stopErr := peer.Call(ctx, pluginbridge.MethodPluginStop, struct{}{}, nil)
	_ = stdin.Close()
	select {
	case <-done:
	case <-ctx.Done():
		_ = command.Process.Kill()
		<-done
		stopErr = errors.Join(stopErr, ctx.Err())
	}

	p.mu.Lock()
	waitErr := p.waitErr
	p.command = nil
	p.peer = nil
	p.stdin = nil
	p.done = nil
	p.mu.Unlock()
	if errors.Is(stopErr, pluginrpc.ErrClosed) {
		stopErr = nil
	}
	if exitIsExpected(waitErr) {
		waitErr = nil
	}
	// Once shutdown has been requested, the process exit status is not an
	// application failure. systemd may signal the whole service cgroup before
	// the parent finishes the RPC stop handshake.
	if waitErr != nil {
		p.logger.Debug("plugin process exited during shutdown", "error", waitErr)
	}
	return stopErr
}

func (p *External) OnMessage(
	ctx context.Context,
	message telegram.Message,
) error {
	if !p.installed.Manifest.Listens {
		return nil
	}
	return p.call(ctx, pluginbridge.MethodPluginMessage, message, nil)
}

func (p *External) ListensToMessages() bool {
	return p.installed.Manifest.Listens
}

func (p *External) OnEditedMessage(
	ctx context.Context,
	message telegram.Message,
) error {
	if !p.installed.Manifest.ListensEdits {
		return nil
	}
	return p.call(ctx, pluginbridge.MethodPluginEditedMessage, message, nil)
}

func (p *External) ListensToEditedMessages() bool {
	return p.installed.Manifest.ListensEdits
}

func (p *External) call(
	ctx context.Context,
	method string,
	request any,
	target any,
) error {
	p.mu.Lock()
	if p.command == nil {
		if err := p.startLocked(ctx); err != nil {
			p.mu.Unlock()
			return err
		}
	} else if channelClosed(p.done) {
		p.logger.Warn("restarting plugin after process exit", "error", p.waitErr)
		p.clearExitedLocked()
		if err := p.startLocked(ctx); err != nil {
			p.mu.Unlock()
			return err
		}
	}
	peer := p.peer
	p.mu.Unlock()
	return peer.Call(ctx, method, request, target)
}

func (p *External) clearExitedLocked() {
	p.command = nil
	p.peer = nil
	p.stdin = nil
	p.done = nil
	p.waitErr = nil
}

func (p *External) startLocked(ctx context.Context) error {
	if err := os.MkdirAll(p.workDir, 0o700); err != nil {
		return fmt.Errorf("create plugin work directory: %w", err)
	}
	host, err := pluginbridge.NewHost(
		p.services,
		p.installed.Manifest,
		p.workDir,
	)
	if err != nil {
		return err
	}
	process := exec.Command(p.installed.Executable)
	process.Dir = p.installed.Directory
	process.Env = pluginEnvironment(
		p.installed.Manifest.Name,
		p.workDir,
		p.services.AssetsDir,
		p.services.LegacyAssetsDir,
	)
	stdin, err := process.StdinPipe()
	if err != nil {
		return fmt.Errorf("open plugin stdin: %w", err)
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("open plugin stdout: %w", err)
	}
	process.Stderr = &logWriter{logger: p.logger}
	if err := process.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("start plugin process: %w", err)
	}
	peer := pluginrpc.New(stdout, stdin, host.Handle)
	done := make(chan struct{})
	p.command = process
	p.peer = peer
	p.stdin = stdin
	p.done = done
	p.waitErr = nil
	go func() {
		waitErr := process.Wait()
		p.mu.Lock()
		if p.command == process {
			p.waitErr = waitErr
		}
		p.mu.Unlock()
		close(done)
	}()

	startCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := peer.Call(
		startCtx,
		pluginbridge.MethodPluginStart,
		struct{}{},
		nil,
	); err != nil {
		_ = process.Process.Kill()
		p.command = nil
		p.peer = nil
		p.stdin = nil
		p.done = nil
		return fmt.Errorf("initialize plugin process: %w", err)
	}
	return nil
}

func pluginEnvironment(name, workDir, assetsDir, legacyAssetsDir string) []string {
	result := make([]string, 0, len(os.Environ())+4)
	for _, item := range os.Environ() {
		key := item
		if index := strings.IndexByte(item, '='); index >= 0 {
			key = item[:index]
		}
		if strings.HasPrefix(strings.ToUpper(key), "TELEBOX_") {
			continue
		}
		result = append(result, item)
	}
	result = append(
		result,
		"TELEBOX_PLUGIN_NAME="+name,
		"TELEBOX_PLUGIN_WORKDIR="+workDir,
		"TELEBOX_PLUGIN_ASSETS_DIR="+assetsDir,
		"TELEBOX_PLUGIN_LEGACY_ASSETS_DIR="+legacyAssetsDir,
	)
	return result
}

func exitIsExpected(err error) bool {
	if err == nil {
		return true
	}
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 0
}

func channelClosed(done <-chan struct{}) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

type logWriter struct {
	logger *slog.Logger
	mu     sync.Mutex
	buffer strings.Builder
}

func (w *logWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	original := len(data)
	w.buffer.Write(data)
	for {
		value := w.buffer.String()
		index := strings.IndexByte(value, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSpace(value[:index])
		remaining := value[index+1:]
		w.buffer.Reset()
		w.buffer.WriteString(remaining)
		if line != "" {
			w.logger.Info("plugin process", "message", line)
		}
	}
	return original, nil
}

var _ plugin.Plugin = (*External)(nil)
var _ plugin.MessageListener = (*External)(nil)
