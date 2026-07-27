package plugin

import (
	"context"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type Metadata struct {
	Name        string
	Version     string
	Description string
}

type Plugin interface {
	Metadata() Metadata
	Commands() []command.Definition
	Start(context.Context) error
	Stop(context.Context) error
}

// MessageListener is an optional capability for plugins that react to ordinary
// incoming messages in addition to registered commands.
type MessageListener interface {
	OnMessage(context.Context, telegram.Message) error
}

type Listener struct {
	Plugin  string
	Handler MessageListener
}

type Status struct {
	Metadata Metadata
	Enabled  bool
}
