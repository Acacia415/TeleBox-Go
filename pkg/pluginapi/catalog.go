package pluginapi

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const CatalogSchemaVersion = 1

var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

type Catalog struct {
	SchemaVersion int             `json:"schema_version"`
	Plugins       []CatalogPlugin `json:"plugins"`
}

type CatalogPlugin struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Homepage    string          `json:"homepage,omitempty"`
	Releases    []PluginRelease `json:"releases"`
}

type PluginRelease struct {
	Version   string     `json:"version"`
	MinHost   string     `json:"min_host,omitempty"`
	Artifacts []Artifact `json:"artifacts"`
}

type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
	Format string `json:"format"`
}

func (c Catalog) Validate() error {
	var problems []error
	if c.SchemaVersion != CatalogSchemaVersion {
		problems = append(problems, fmt.Errorf(
			"unsupported catalog schema %d",
			c.SchemaVersion,
		))
	}
	seen := make(map[string]struct{}, len(c.Plugins))
	for _, item := range c.Plugins {
		if !namePattern.MatchString(item.Name) {
			problems = append(problems, fmt.Errorf(
				"invalid catalog plugin name %q",
				item.Name,
			))
		}
		if _, exists := seen[item.Name]; exists {
			problems = append(problems, fmt.Errorf(
				"duplicate catalog plugin %q",
				item.Name,
			))
		}
		seen[item.Name] = struct{}{}
		if strings.TrimSpace(item.Description) == "" {
			problems = append(problems, fmt.Errorf(
				"catalog plugin %q has no description",
				item.Name,
			))
		}
		if len(item.Releases) == 0 {
			problems = append(problems, fmt.Errorf(
				"catalog plugin %q has no releases",
				item.Name,
			))
		}
		for _, release := range item.Releases {
			if !versionPattern.MatchString(release.Version) {
				problems = append(problems, fmt.Errorf(
					"plugin %q has invalid release %q",
					item.Name,
					release.Version,
				))
			}
			if len(release.Artifacts) == 0 {
				problems = append(problems, fmt.Errorf(
					"plugin %q release %q has no artifacts",
					item.Name,
					release.Version,
				))
			}
			for _, artifact := range release.Artifacts {
				if err := artifact.Validate(); err != nil {
					problems = append(problems, fmt.Errorf(
						"plugin %q release %q: %w",
						item.Name,
						release.Version,
						err,
					))
				}
			}
		}
	}
	return errors.Join(problems...)
}

func (a Artifact) Validate() error {
	if a.OS == "" || a.Arch == "" {
		return errors.New("artifact OS and architecture are required")
	}
	switch a.Format {
	case "zip", "tar.gz":
	default:
		return fmt.Errorf("unsupported artifact format %q", a.Format)
	}
	parsed, err := url.Parse(a.URL)
	if err != nil {
		return fmt.Errorf("parse artifact URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("artifact URL must be an HTTPS URL without credentials")
	}
	if !sha256Pattern.MatchString(a.SHA256) {
		return errors.New("artifact SHA-256 is invalid")
	}
	if a.Size < 0 {
		return errors.New("artifact size cannot be negative")
	}
	return nil
}

func (c Catalog) Find(name string) (CatalogPlugin, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, item := range c.Plugins {
		if item.Name == name {
			return item, true
		}
	}
	return CatalogPlugin{}, false
}

func (p CatalogPlugin) Latest() (PluginRelease, bool) {
	if len(p.Releases) == 0 {
		return PluginRelease{}, false
	}
	// Catalog releases are explicitly ordered newest first. Keeping ordering
	// authoritative avoids pretending lexical comparison is semantic versioning.
	return p.Releases[0], true
}

func (r PluginRelease) ArtifactFor(goos, goarch string) (Artifact, bool) {
	for _, artifact := range r.Artifacts {
		if artifact.OS == goos && artifact.Arch == goarch {
			return artifact, true
		}
	}
	return Artifact{}, false
}

func (c Catalog) SortedPlugins() []CatalogPlugin {
	result := append([]CatalogPlugin(nil), c.Plugins...)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}
