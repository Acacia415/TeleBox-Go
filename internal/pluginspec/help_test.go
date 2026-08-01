package pluginspec_test

import (
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/command"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/plugins/aban"
	"github.com/Acacia415/TeleBox-Go/plugins/ai"
	"github.com/Acacia415/TeleBox-Go/plugins/binlookup"
	"github.com/Acacia415/TeleBox-Go/plugins/bulkdelete"
	"github.com/Acacia415/TeleBox-Go/plugins/cezi"
	"github.com/Acacia415/TeleBox-Go/plugins/convert"
	"github.com/Acacia415/TeleBox-Go/plugins/dc"
	"github.com/Acacia415/TeleBox-Go/plugins/dig"
	"github.com/Acacia415/TeleBox-Go/plugins/eat"
	"github.com/Acacia415/TeleBox-Go/plugins/gif"
	"github.com/Acacia415/TeleBox-Go/plugins/ids"
	"github.com/Acacia415/TeleBox-Go/plugins/iplookup"
	"github.com/Acacia415/TeleBox-Go/plugins/isalive"
	"github.com/Acacia415/TeleBox-Go/plugins/jointime"
	"github.com/Acacia415/TeleBox-Go/plugins/nsticker"
	"github.com/Acacia415/TeleBox-Go/plugins/pmcaptcha"
	"github.com/Acacia415/TeleBox-Go/plugins/rate"
	repeatplugin "github.com/Acacia415/TeleBox-Go/plugins/repeat"
	"github.com/Acacia415/TeleBox-Go/plugins/search"
	"github.com/Acacia415/TeleBox-Go/plugins/speedlink"
	"github.com/Acacia415/TeleBox-Go/plugins/telegrambackup"
	"github.com/Acacia415/TeleBox-Go/plugins/trace"
	"github.com/Acacia415/TeleBox-Go/plugins/ytdlp"
	"github.com/Acacia415/TeleBox-Go/plugins/yvlu"
	"github.com/Acacia415/TeleBox-Go/plugins/zhijiao"
)

type commandProvider interface {
	Commands() []command.Definition
}

func TestEveryBusinessPluginProvidesDetailedHelp(t *testing.T) {
	services := service.Container{}
	plugins := map[string]commandProvider{
		"aban":            aban.New(services),
		"ai":              ai.New(services),
		"bin":             binlookup.New(services),
		"bulk_delete":     bulkdelete.New(services),
		"cezi":            cezi.New(services),
		"convert":         convert.New(services),
		"dc":              dc.New(services),
		"dig":             dig.New(services),
		"eat":             eat.New(services),
		"eatgif":          eat.NewGIF(services),
		"gif":             gif.New(services),
		"ids":             ids.New(services),
		"ip":              iplookup.New(services),
		"isalive":         isalive.New(services),
		"jointime":        jointime.New(services),
		"nsticker":        nsticker.New(services),
		"pmcaptcha":       pmcaptcha.New(services),
		"rate":            rate.New(services),
		"re":              repeatplugin.New(services),
		"search":          search.New(services),
		"speedlink":       speedlink.New(services),
		"telegram-backup": telegrambackup.New(services),
		"trace":           trace.New(services),
		"yt-dlp":          ytdlp.New(services),
		"yvlu":            yvlu.New(services),
		"zhijiao":         zhijiao.New(services),
	}
	for name, current := range plugins {
		definitions := current.Commands()
		hasDetailedHelp := false
		for _, definition := range definitions {
			if strings.TrimSpace(definition.HelpHTML) != "" {
				hasDetailedHelp = true
				break
			}
		}
		if !hasDetailedHelp {
			t.Errorf("%s has no detailed HelpHTML", name)
		}
	}
}
