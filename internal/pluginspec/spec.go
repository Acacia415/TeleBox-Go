package pluginspec

import (
	"fmt"
	"sort"
	"strings"
)

const modulePath = "github.com/Acacia415/TeleBox-Go"

type Spec struct {
	Name        string
	Package     string
	Constructor string
	SourceDir   string
	MinHost     string
}

var catalog = []Spec{
	{Name: "aban", Package: modulePath + "/plugins/aban", Constructor: "New", SourceDir: "plugins/aban"},
	{Name: "ai", Package: modulePath + "/plugins/ai", Constructor: "New", SourceDir: "plugins/ai"},
	{Name: "bin", Package: modulePath + "/plugins/binlookup", Constructor: "New", SourceDir: "plugins/binlookup"},
	{Name: "bulk_delete", Package: modulePath + "/plugins/bulkdelete", Constructor: "New", SourceDir: "plugins/bulkdelete"},
	{Name: "cezi", Package: modulePath + "/plugins/cezi", Constructor: "New", SourceDir: "plugins/cezi"},
	{Name: "convert", Package: modulePath + "/plugins/convert", Constructor: "New", SourceDir: "plugins/convert"},
	{Name: "dc", Package: modulePath + "/plugins/dc", Constructor: "New", SourceDir: "plugins/dc"},
	{Name: "dig", Package: modulePath + "/plugins/dig", Constructor: "New", SourceDir: "plugins/dig"},
	{Name: "eat", Package: modulePath + "/plugins/eat", Constructor: "New", SourceDir: "plugins/eat"},
	{Name: "eatgif", Package: modulePath + "/plugins/eat", Constructor: "NewGIF", SourceDir: "plugins/eat"},
	{Name: "gif", Package: modulePath + "/plugins/gif", Constructor: "New", SourceDir: "plugins/gif"},
	{Name: "ids", Package: modulePath + "/plugins/ids", Constructor: "New", SourceDir: "plugins/ids"},
	{Name: "ip", Package: modulePath + "/plugins/iplookup", Constructor: "New", SourceDir: "plugins/iplookup"},
	{Name: "isalive", Package: modulePath + "/plugins/isalive", Constructor: "New", SourceDir: "plugins/isalive"},
	{Name: "jointime", Package: modulePath + "/plugins/jointime", Constructor: "New", SourceDir: "plugins/jointime"},
	{Name: "nsticker", Package: modulePath + "/plugins/nsticker", Constructor: "New", SourceDir: "plugins/nsticker"},
	{Name: "pmcaptcha", Package: modulePath + "/plugins/pmcaptcha", Constructor: "New", SourceDir: "plugins/pmcaptcha", MinHost: "0.7.0"},
	{Name: "rate", Package: modulePath + "/plugins/rate", Constructor: "New", SourceDir: "plugins/rate"},
	{Name: "re", Package: modulePath + "/plugins/repeat", Constructor: "New", SourceDir: "plugins/repeat"},
	{Name: "search", Package: modulePath + "/plugins/search", Constructor: "New", SourceDir: "plugins/search"},
	{Name: "speedlink", Package: modulePath + "/plugins/speedlink", Constructor: "New", SourceDir: "plugins/speedlink", MinHost: "0.7.1"},
	{Name: "telegram-backup", Package: modulePath + "/plugins/telegrambackup", Constructor: "New", SourceDir: "plugins/telegrambackup"},
	{Name: "trace", Package: modulePath + "/plugins/trace", Constructor: "New", SourceDir: "plugins/trace"},
	{Name: "yt-dlp", Package: modulePath + "/plugins/ytdlp", Constructor: "New", SourceDir: "plugins/ytdlp"},
	{Name: "yvlu", Package: modulePath + "/plugins/yvlu", Constructor: "New", SourceDir: "plugins/yvlu"},
	{Name: "zhijiao", Package: modulePath + "/plugins/zhijiao", Constructor: "New", SourceDir: "plugins/zhijiao"},
}

func All() []Spec {
	result := append([]Spec(nil), catalog...)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func Find(name string) (Spec, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, item := range catalog {
		if item.Name == name {
			return item, true
		}
	}
	return Spec{}, false
}

func Validate() error {
	seen := make(map[string]struct{}, len(catalog))
	for _, item := range catalog {
		if item.Name == "" || item.Package == "" ||
			item.Constructor == "" || item.SourceDir == "" {
			return fmt.Errorf("incomplete plugin build specification: %+v", item)
		}
		if _, exists := seen[item.Name]; exists {
			return fmt.Errorf("duplicate plugin build specification %q", item.Name)
		}
		seen[item.Name] = struct{}{}
	}
	return nil
}
