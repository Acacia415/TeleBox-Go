package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/scheduler"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

type Storage interface {
	Put(context.Context, string, string, []byte) error
	Get(context.Context, string, string) ([]byte, error)
	Delete(context.Context, string, string) error
	SetPluginState(context.Context, storage.PluginState) error
	PluginStates(context.Context) ([]storage.PluginState, error)
	Close() error
}

type HTTPClient interface {
	Do(context.Context, httpclient.Request) (httpclient.Response, error)
	JSON(context.Context, httpclient.Request, any) (httpclient.Response, error)
	Close()
}

type ToolRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, toolrunner.Command) (toolrunner.Result, error)
}

type Scheduler interface {
	Start(context.Context) error
	Every(string, time.Duration, bool, scheduler.JobFunc) error
	Remove(context.Context, string) error
	Stop(context.Context) error
}

// Container is the stable dependency surface provided to built-in and migrated
// plugins. Keep application wiring here and Telegram implementation details
// behind telegram.Client.
type Container struct {
	Logger    *slog.Logger
	Telegram  telegram.Client
	Storage   Storage
	Tools     ToolRunner
	Scheduler Scheduler
	AssetsDir string
	HTTP      HTTPClient
}
