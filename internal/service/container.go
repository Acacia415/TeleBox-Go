package service

import (
	"log/slog"

	"github.com/Acacia415/TeleBox-Go/internal/httpclient"
	"github.com/Acacia415/TeleBox-Go/internal/scheduler"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

// Container is the stable dependency surface provided to built-in and migrated
// plugins. Keep application wiring here and Telegram implementation details
// behind telegram.Client.
type Container struct {
	Logger    *slog.Logger
	Telegram  telegram.Client
	Storage   *storage.DB
	Tools     *toolrunner.Runner
	Scheduler *scheduler.Scheduler
	AssetsDir string
	HTTP      *httpclient.Client
}
