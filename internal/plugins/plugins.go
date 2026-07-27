package plugins

import (
	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/aban"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/ai"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/binlookup"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/bulkdelete"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/cezi"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/convert"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/dc"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/dig"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/eat"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/gif"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/ids"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/iplookup"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/isalive"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/jointime"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/musicbot"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/nsticker"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/rate"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/repeat"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/search"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/speedlink"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/speedtest"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/telegrambackup"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/trace"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/ytdlp"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/yvlu"
	"github.com/Acacia415/TeleBox-Go/internal/plugins/zhijiao"
	"github.com/Acacia415/TeleBox-Go/internal/service"
)

// Builtins is the compile-time plugin catalog. Only plugins present in the
// user's full backup are added here as their Go ports are completed.
func Builtins(services service.Container) []plugin.Plugin {
	return []plugin.Plugin{
		aban.New(services),
		ai.New(services),
		binlookup.New(services),
		bulkdelete.New(services),
		cezi.New(services),
		convert.New(services),
		dc.New(services),
		dig.New(services),
		eat.New(services),
		eat.NewGIF(services),
		gif.New(services),
		ids.New(services),
		iplookup.New(services),
		isalive.New(services),
		jointime.New(services),
		musicbot.New(services),
		nsticker.New(services),
		rate.New(services),
		repeat.New(services),
		search.New(services),
		speedlink.New(services),
		speedtest.New(services),
		telegrambackup.New(services),
		trace.New(services),
		ytdlp.New(services),
		yvlu.New(services),
		zhijiao.New(services),
	}
}
