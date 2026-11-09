package eat

import "embed"

//go:embed builtin/*.png
var builtinAssets embed.FS

// Keep personal templates with the plugin so refreshing the remote catalog
// cannot remove them. Coordinates refer to the original 512x512 canvas.
func builtinEntries() map[string]entry {
	return map[string]entry{
		"kj": {
			Name: "看剑！偷袭",
			URL:  "builtin/kanjian.png",
			Me: &role{
				X: 77, Y: 193,
				Mask: "builtin/maskkj-me.png",
			},
			You: &role{
				X: 415, Y: 246,
				Mask: "builtin/maskkj-you.png",
			},
		},
	}
}
