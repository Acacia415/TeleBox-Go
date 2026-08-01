package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/buildinfo"
	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/config"
	"github.com/Acacia415/TeleBox-Go/internal/dispatch"
	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/legacyconfig"
	"github.com/Acacia415/TeleBox-Go/internal/migration"
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/pluginmanager"
	"github.com/Acacia415/TeleBox-Go/internal/pluginmarket"
	coreplugin "github.com/Acacia415/TeleBox-Go/internal/plugins/core"
	"github.com/Acacia415/TeleBox-Go/internal/ratelimit"
	"github.com/Acacia415/TeleBox-Go/internal/scheduler"
	"github.com/Acacia415/TeleBox-Go/internal/selfupdate"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
	"github.com/Acacia415/TeleBox-Go/internal/usererror"
)

type App struct {
	config   config.Config
	logger   *slog.Logger
	client   telegram.Client
	router   *command.Router
	registry *plugin.Registry
	commands *dispatch.Pool
	limiter  *ratelimit.Limiter
	services service.Container
}

func New(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	logLevel service.LogLevelController,
	client telegram.Client,
	restart func(),
) (*App, error) {
	if logger == nil {
		return nil, errors.New("logger is required")
	}
	if client == nil {
		return nil, errors.New("telegram client is required")
	}
	router, err := command.NewRouter(cfg.Commands.Prefixes, cfg.Commands.OwnerIDs)
	if err != nil {
		return nil, fmt.Errorf("create command router: %w", err)
	}
	registry := plugin.NewRegistry(router)
	store, err := storage.Open(ctx, cfg.Storage.Path)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.LegacyAssetsPath, 0o700); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("create legacy asset directory: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.AssetsPath, 0o700); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("create active asset directory: %w", err)
	}
	quarantined, err := migration.QuarantineUnsafeActiveAssets(
		cfg.Storage.AssetsPath,
		cfg.Storage.LegacyAssetsPath,
	)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("quarantine unsafe active assets: %w", err)
	}
	if quarantined.QuarantinedFiles > 0 {
		logger.Info(
			"quarantined unsafe migrated assets",
			"files", quarantined.QuarantinedFiles,
			"bytes", quarantined.QuarantinedBytes,
			"directory", filepath.Join(
				cfg.Storage.LegacyAssetsPath,
				"_quarantine",
				"active-assets",
			),
		)
	}
	tools, err := toolrunner.New(cfg.Tools.MaxConcurrent)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("create tool runner: %w", err)
	}
	jobScheduler := scheduler.New(logger)
	httpClient, err := httpclient.New(httpclient.Config{
		Timeout:          time.Duration(cfg.HTTP.TimeoutSeconds) * time.Second,
		MaxConcurrent:    cfg.HTTP.MaxConcurrent,
		MaxResponseBytes: cfg.HTTP.MaxResponseBytes,
	})
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("create HTTP client: %w", err)
	}
	commandPool, err := dispatch.New(
		cfg.Commands.MaxConcurrent,
		cfg.Commands.QueueCapacity,
		logger,
	)
	if err != nil {
		httpClient.Close()
		_ = store.Close()
		return nil, fmt.Errorf("create command pool: %w", err)
	}
	safeTelegram := usererror.Wrap(client, logger)
	services := service.Container{
		Logger:          logger,
		Telegram:        safeTelegram,
		Storage:         store,
		Tools:           tools,
		Scheduler:       jobScheduler,
		AssetsDir:       cfg.Storage.AssetsPath,
		LegacyAssetsDir: cfg.Storage.LegacyAssetsPath,
		ConfigPath:      cfg.SourcePath,
		StoragePath:     cfg.Storage.Path,
		PluginsDir:      cfg.Plugins.Directory,
		SessionPath:     cfg.Telegram.SessionFile,
		HTTP:            httpClient,
		LogLevel:        logLevel,
		LogPath:         cfg.Logging.Path,
		Restart:         restart,
	}
	market, err := pluginmarket.New(pluginmarket.Config{
		Directory:       cfg.Plugins.Directory,
		CatalogURL:      cfg.Plugins.CatalogURL,
		MaxArchiveBytes: cfg.Plugins.MaxArchiveBytes,
	})
	if err != nil {
		httpClient.Close()
		_ = store.Close()
		return nil, fmt.Errorf("create plugin package manager: %w", err)
	}
	packages, err := pluginmanager.New(
		market,
		registry,
		services,
		filepath.Join(cfg.Storage.AssetsPath, "plugin-runtime"),
	)
	if err != nil {
		httpClient.Close()
		_ = store.Close()
		return nil, fmt.Errorf("create plugin controller: %w", err)
	}
	core := coreplugin.New(
		services,
		router,
		registry,
		packages,
		selfupdate.New(buildinfo.Version),
	)
	if err := registry.Add(core); err != nil {
		httpClient.Close()
		_ = store.Close()
		return nil, fmt.Errorf("register core plugin: %w", err)
	}
	if err := packages.LoadInstalled(); err != nil {
		logger.Error(
			"some installed plugins could not be loaded; continuing",
			"error", err,
		)
	}

	return &App{
		config:   cfg,
		logger:   logger,
		client:   safeTelegram,
		router:   router,
		registry: registry,
		commands: commandPool,
		limiter: ratelimit.New(
			time.Duration(cfg.Commands.PerSenderIntervalMS) * time.Millisecond,
		),
		services: services,
	}, nil
}

func (a *App) Run(ctx context.Context) (runErr error) {
	defer a.services.HTTP.Close()
	defer func() {
		if err := a.services.Storage.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close storage: %w", err))
		}
	}()
	if err := a.services.Scheduler.Start(ctx); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.services.Scheduler.Stop(shutdownCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("stop scheduler: %w", err))
		}
	}()
	a.restoreCoreSettings(ctx)
	if err := a.registry.Enable(ctx, "core"); err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.registry.Shutdown(shutdownCtx); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()

	for _, name := range a.desiredPlugins(ctx) {
		if name == "core" {
			continue
		}
		if err := a.registry.Enable(ctx, name); err != nil {
			a.logger.Error("enable plugin failed; continuing",
				"plugin", name,
				"error", err,
			)
		}
	}

	if err := a.commands.Start(ctx); err != nil {
		return fmt.Errorf("start command pool: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.commands.Stop(shutdownCtx); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()

	a.logger.Info("TeleBox starting",
		"plugins", len(a.registry.List()),
		"prefixes", a.config.Commands.Prefixes,
		"command_workers", a.config.Commands.MaxConcurrent,
	)
	err := a.client.Run(ctx, a.handleMessage)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (a *App) restoreCoreSettings(ctx context.Context) {
	encoded, err := a.services.Storage.Get(ctx, "core", "command_prefixes")
	switch {
	case err == nil:
		var prefixes []string
		if decodeErr := json.Unmarshal(encoded, &prefixes); decodeErr != nil {
			a.logger.Error("decode persisted command prefixes", "error", decodeErr)
		} else if setErr := a.router.SetPrefixes(prefixes); setErr != nil {
			a.logger.Error("apply persisted command prefixes", "error", setErr)
		}
	case errors.Is(err, storage.ErrNotFound):
	default:
		a.logger.Error("load persisted command prefixes", "error", err)
	}
	a.restoreCommandAliases(ctx)
	a.restoreLogLevel(ctx)
}

func (a *App) restoreCommandAliases(ctx context.Context) {
	const storageKey = "command_aliases"

	encoded, err := a.services.Storage.Get(ctx, "core", storageKey)
	switch {
	case err == nil:
		var aliases map[string]string
		if decodeErr := json.Unmarshal(encoded, &aliases); decodeErr != nil {
			a.logger.Error("decode persisted command aliases", "error", decodeErr)
			return
		}
		if setErr := a.router.SetUserAliases(aliases); setErr != nil {
			a.logger.Error("apply persisted command aliases", "error", setErr)
		}
		return
	case !errors.Is(err, storage.ErrNotFound):
		a.logger.Error("load persisted command aliases", "error", err)
		return
	}

	var aliases map[string]string
	for _, databasePath := range legacyconfig.CandidatePaths(
		a.config.Storage.AssetsPath,
		a.config.Storage.LegacyAssetsPath,
		"alias/alias.db",
	) {
		candidate, readErr := legacyconfig.ReadAliases(databasePath)
		if readErr != nil {
			a.logger.Error(
				"read legacy command aliases",
				"path", databasePath,
				"error", readErr,
			)
			continue
		}
		if len(candidate) > 0 {
			aliases = candidate
			break
		}
	}
	if len(aliases) == 0 {
		return
	}
	if err := a.router.SetUserAliases(aliases); err != nil {
		a.logger.Error("apply legacy command aliases", "error", err)
		return
	}
	encoded, err = json.Marshal(a.router.UserAliases())
	if err != nil {
		a.logger.Error("encode migrated command aliases", "error", err)
		return
	}
	if err := a.services.Storage.Put(ctx, "core", storageKey, encoded); err != nil {
		a.logger.Error("persist migrated command aliases", "error", err)
		return
	}
	a.logger.Info("migrated legacy command aliases", "count", len(aliases))
}

func (a *App) restoreLogLevel(ctx context.Context) {
	if a.services.LogLevel == nil {
		return
	}
	encoded, err := a.services.Storage.Get(ctx, "core", "log_level")
	if err != nil {
		if !errors.Is(err, storage.ErrNotFound) {
			a.logger.Error("load persisted log level", "error", err)
		}
		return
	}
	level, err := parseLogLevel(string(encoded))
	if err != nil {
		a.logger.Error("decode persisted log level", "error", err)
		return
	}
	a.services.LogLevel.Set(level)
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warning", "warn":
		return slog.LevelWarn, nil
	case "error", "err":
		return slog.LevelError, nil
	case "silent", "off":
		return slog.Level(100), nil
	default:
		return 0, fmt.Errorf("unsupported log level %q", value)
	}
}

func (a *App) desiredPlugins(ctx context.Context) []string {
	desired := make(map[string]bool)
	for _, name := range a.config.Plugins.Enabled {
		desired[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for _, name := range a.config.Plugins.Disabled {
		desired[strings.ToLower(strings.TrimSpace(name))] = false
	}

	states, err := a.services.Storage.PluginStates(ctx)
	if err != nil {
		a.logger.Error("load persisted plugin states; using config only", "error", err)
	} else {
		for _, state := range states {
			desired[strings.ToLower(strings.TrimSpace(state.Name))] = state.Enabled
		}
	}

	registered := make(map[string]struct{})
	for _, status := range a.registry.List() {
		registered[status.Metadata.Name] = struct{}{}
	}
	var enabled []string
	for name, isEnabled := range desired {
		if !isEnabled || name == "core" {
			continue
		}
		if _, exists := registered[name]; !exists {
			a.logger.Warn("configured plugin is not installed", "plugin", name)
			continue
		}
		enabled = append(enabled, name)
	}
	sort.Strings(enabled)
	return enabled
}

func (a *App) Services() service.Container {
	return a.services
}

func (a *App) Register(candidate plugin.Plugin) error {
	return a.registry.Add(candidate)
}

func (a *App) handleMessage(ctx context.Context, message telegram.Message) error {
	if message.Edited {
		listeners := a.registry.EditedMessageListeners()
		if len(listeners) == 0 {
			return nil
		}
		if err := a.commands.Submit(func(jobCtx context.Context) {
			a.dispatchEditedListeners(jobCtx, message, listeners)
		}); err != nil {
			a.logger.Warn(
				"command queue rejected edited message",
				"sender_id", message.SenderID,
				"chat_id", message.ChatID,
				"error", err,
			)
		}
		return nil
	}
	_, commandLike := a.router.Parse(message)
	listeners := a.registry.MessageListeners()
	if commandLike && !a.router.IsAuthorized(message) {
		a.logger.Debug(
			"ignored command from unauthorized sender",
			"sender_id", message.SenderID,
			"chat_id", message.ChatID,
		)
		commandLike = false
	}
	if !commandLike && len(listeners) == 0 {
		return nil
	}
	if commandLike && !a.router.IsOwner(message) &&
		!a.limiter.Allow(message.SenderID, time.Now()) {
		a.logger.Debug("command rate limited",
			"sender_id", message.SenderID,
			"chat_id", message.ChatID,
		)
		return nil
	}
	if err := a.commands.Submit(func(jobCtx context.Context) {
		a.dispatchListeners(jobCtx, message, listeners)
		if commandLike {
			a.dispatchMessage(jobCtx, message)
		}
	}); err != nil {
		a.logger.Warn("command queue rejected message",
			"sender_id", message.SenderID,
			"chat_id", message.ChatID,
			"error", err,
		)
	}
	return nil
}

func (a *App) dispatchEditedListeners(
	ctx context.Context,
	message telegram.Message,
	listeners []plugin.EditedListener,
) {
	for _, listener := range listeners {
		if err := listener.Handler.OnEditedMessage(ctx, message); err != nil {
			a.logger.Error(
				"plugin edited-message listener failed",
				"plugin", listener.Plugin,
				"sender_id", message.SenderID,
				"chat_id", message.ChatID,
				"error", err,
			)
		}
	}
}

func (a *App) dispatchListeners(
	ctx context.Context,
	message telegram.Message,
	listeners []plugin.Listener,
) {
	for _, listener := range listeners {
		if err := listener.Handler.OnMessage(ctx, message); err != nil {
			a.logger.Error(
				"plugin message listener failed",
				"plugin", listener.Plugin,
				"sender_id", message.SenderID,
				"chat_id", message.ChatID,
				"error", err,
			)
		}
	}
}

func (a *App) dispatchMessage(ctx context.Context, message telegram.Message) {
	result, err := a.router.Dispatch(ctx, message)
	switch {
	case err == nil:
		return
	case errors.Is(err, command.ErrPermissionDenied):
		a.logger.Warn("command denied",
			"plugin", result.Plugin,
			"command", result.Command,
			"sender_id", message.SenderID,
			"chat_id", message.ChatID,
		)
		if sendErr := a.respondCommandError(ctx, message, "⛔ 无权使用此命令"); sendErr != nil {
			a.logger.Error("send permission denial", "error", sendErr)
		}
		return
	default:
		a.logger.Error("command failed",
			"plugin", result.Plugin,
			"command", result.Command,
			"sender_id", message.SenderID,
			"chat_id", message.ChatID,
			"error", err,
		)
		if sendErr := a.respondCommandError(ctx, message, "❌ 命令执行失败"); sendErr != nil {
			a.logger.Error("send command failure", "error", sendErr)
		}
		// A single plugin error must not stop the MTProto update loop.
		return
	}
}

func (a *App) respondCommandError(
	ctx context.Context,
	message telegram.Message,
	text string,
) error {
	if message.Outgoing {
		_, err := a.client.EditText(ctx, message.ChatID, message.ID, text)
		return err
	}
	_, err := a.client.ReplyText(ctx, message.ChatID, message.ID, text)
	return err
}
