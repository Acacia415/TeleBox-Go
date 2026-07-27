package pluginruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/pluginbridge"
	"github.com/Acacia415/TeleBox-Go/internal/pluginrpc"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type Factory func(service.Container) plugin.Plugin

type runtimeState struct {
	plugin   plugin.Plugin
	services service.Container
	commands map[string]command.Definition

	mu      sync.Mutex
	started bool
}

func Run(factory Factory) error {
	if factory == nil {
		return errors.New("plugin factory is required")
	}
	name := strings.TrimSpace(os.Getenv("TELEBOX_PLUGIN_NAME"))
	workDir := strings.TrimSpace(os.Getenv("TELEBOX_PLUGIN_WORKDIR"))
	assetsDir := strings.TrimSpace(os.Getenv("TELEBOX_PLUGIN_ASSETS_DIR"))
	legacyAssetsDir := strings.TrimSpace(os.Getenv("TELEBOX_PLUGIN_LEGACY_ASSETS_DIR"))
	if name == "" || workDir == "" {
		return errors.New("plugin host environment is incomplete")
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})).With("plugin", name)

	var state *runtimeState
	ready := make(chan struct{})
	peer := pluginrpc.New(os.Stdin, os.Stdout, func(
		ctx context.Context,
		method string,
		raw json.RawMessage,
	) (any, error) {
		// The RPC reader starts immediately. The host can send PluginStart
		// before the factory and proxy services below have finished wiring the
		// runtime, so wait until state is published instead of racing it.
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if state == nil {
			return nil, errors.New("plugin runtime is not initialized")
		}
		return state.handle(ctx, method, raw)
	})
	services, err := pluginbridge.NewProxyServices(
		peer,
		name,
		workDir,
		assetsDir,
		legacyAssetsDir,
		logger,
	)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(services.AssetsDir, 0o700); err != nil {
		return fmt.Errorf("create plugin assets directory: %w", err)
	}
	candidate := factory(services)
	if candidate == nil {
		return errors.New("plugin factory returned nil")
	}
	metadata := candidate.Metadata()
	if metadata.Name != name {
		return fmt.Errorf(
			"plugin binary provides %q, host requested %q",
			metadata.Name,
			name,
		)
	}
	commands := make(map[string]command.Definition)
	for _, definition := range candidate.Commands() {
		commands[definition.Name] = definition
		for _, alias := range definition.Aliases {
			commands[alias] = definition
		}
	}
	state = &runtimeState{
		plugin:   candidate,
		services: services,
		commands: commands,
	}
	close(ready)

	<-peer.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = state.stop(shutdownCtx)
	err = peer.Err()
	if errors.Is(err, pluginrpc.ErrClosed) {
		return nil
	}
	return err
}

func (r *runtimeState) handle(
	ctx context.Context,
	method string,
	raw json.RawMessage,
) (any, error) {
	switch method {
	case pluginbridge.MethodPluginStart:
		return nil, r.start(ctx)
	case pluginbridge.MethodPluginStop:
		return nil, r.stop(ctx)
	case pluginbridge.MethodPluginHandle:
		var invocation pluginbridge.Invocation
		if err := json.Unmarshal(raw, &invocation); err != nil {
			return nil, err
		}
		r.mu.Lock()
		started := r.started
		r.mu.Unlock()
		if !started {
			return nil, errors.New("plugin is not started")
		}
		definition, exists := r.commands[invocation.Command]
		if !exists {
			return nil, fmt.Errorf("plugin command %q is not registered", invocation.Command)
		}
		return nil, definition.Handler(ctx, invocation.Request)
	case pluginbridge.MethodPluginMessage:
		var message telegram.Message
		if err := json.Unmarshal(raw, &message); err != nil {
			return nil, err
		}
		r.mu.Lock()
		started := r.started
		r.mu.Unlock()
		if !started {
			return nil, errors.New("plugin is not started")
		}
		listener, ok := r.plugin.(plugin.MessageListener)
		if !ok {
			return nil, errors.New("plugin does not listen to messages")
		}
		return nil, listener.OnMessage(ctx, message)
	default:
		return nil, &pluginrpc.RemoteError{
			Code:    "method_not_found",
			Message: method,
		}
	}
}

func (r *runtimeState) start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return nil
	}
	if err := r.services.Scheduler.Start(ctx); err != nil {
		return err
	}
	if err := r.plugin.Start(ctx); err != nil {
		_ = r.services.Scheduler.Stop(context.Background())
		return err
	}
	r.started = true
	return nil
}

func (r *runtimeState) stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return nil
	}
	pluginErr := r.plugin.Stop(ctx)
	schedulerErr := r.services.Scheduler.Stop(ctx)
	r.started = false
	return errors.Join(pluginErr, schedulerErr)
}
