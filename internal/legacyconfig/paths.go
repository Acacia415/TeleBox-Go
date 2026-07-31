package legacyconfig

import (
	"path/filepath"
	"strings"
)

// CandidatePaths returns active migration assets first and preserved legacy
// assets second. Plugins can import from either location without duplicating
// path handling, while existing Go storage remains authoritative.
func CandidatePaths(
	assetsDir string,
	legacyAssetsDir string,
	relativePaths ...string,
) []string {
	var result []string
	seen := make(map[string]struct{})
	for _, root := range []string{assetsDir, legacyAssetsDir} {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		for _, relative := range relativePaths {
			relative = strings.TrimSpace(relative)
			if relative == "" || filepath.IsAbs(relative) {
				continue
			}
			candidate := filepath.Clean(filepath.Join(
				root,
				filepath.FromSlash(relative),
			))
			if _, exists := seen[candidate]; exists {
				continue
			}
			seen[candidate] = struct{}{}
			result = append(result, candidate)
		}
	}
	return result
}
