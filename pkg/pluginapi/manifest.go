package pluginapi

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	ManifestSchemaVersion = 1
	HostAPIVersion        = 1
)

var (
	namePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
)

type Manifest struct {
	SchemaVersion int         `json:"schema_version"`
	APIVersion    int         `json:"api_version"`
	Name          string      `json:"name"`
	Version       string      `json:"version"`
	Description   string      `json:"description"`
	Executable    string      `json:"executable"`
	Commands      []Command   `json:"commands"`
	Listens       bool        `json:"listens_to_messages,omitempty"`
	Permissions   Permissions `json:"permissions,omitempty"`
	Homepage      string      `json:"homepage,omitempty"`
}

type Command struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"`
	Description string   `json:"description,omitempty"`
	Usage       []string `json:"usage,omitempty"`
	HelpHTML    string   `json:"help_html,omitempty"`
	OwnerOnly   bool     `json:"owner_only,omitempty"`
}

type Permissions struct {
	Telegram []string `json:"telegram,omitempty"`
	Tools    []string `json:"tools,omitempty"`
	Network  bool     `json:"network,omitempty"`
	Storage  bool     `json:"storage,omitempty"`
}

func (m Manifest) Validate() error {
	var problems []error
	if m.SchemaVersion != ManifestSchemaVersion {
		problems = append(problems, fmt.Errorf(
			"unsupported manifest schema %d",
			m.SchemaVersion,
		))
	}
	if m.APIVersion != HostAPIVersion {
		problems = append(problems, fmt.Errorf(
			"unsupported plugin API %d",
			m.APIVersion,
		))
	}
	if !namePattern.MatchString(m.Name) {
		problems = append(problems, fmt.Errorf("invalid plugin name %q", m.Name))
	}
	if !versionPattern.MatchString(m.Version) {
		problems = append(problems, fmt.Errorf("invalid plugin version %q", m.Version))
	}
	if strings.TrimSpace(m.Description) == "" {
		problems = append(problems, errors.New("plugin description is required"))
	}
	if err := validateExecutable(m.Executable); err != nil {
		problems = append(problems, err)
	}
	if len(m.Commands) == 0 && !m.Listens {
		problems = append(problems, errors.New(
			"plugin must expose a command or message listener",
		))
	}

	seenCommands := make(map[string]struct{})
	for _, command := range m.Commands {
		names := append([]string{command.Name}, command.Aliases...)
		for _, name := range names {
			if !namePattern.MatchString(name) {
				problems = append(problems, fmt.Errorf(
					"invalid command or alias %q",
					name,
				))
				continue
			}
			if _, exists := seenCommands[name]; exists {
				problems = append(problems, fmt.Errorf(
					"duplicate command or alias %q",
					name,
				))
			}
			seenCommands[name] = struct{}{}
		}
	}
	return errors.Join(problems...)
}

func validateExecutable(value string) error {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	if normalized == "" {
		return errors.New("plugin executable is required")
	}
	if path.IsAbs(normalized) ||
		strings.Contains(strings.SplitN(normalized, "/", 2)[0], ":") {
		return errors.New("plugin executable must be relative")
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("plugin executable escapes its installation directory")
	}
	return nil
}

func (m Manifest) CommandNames() []string {
	result := make([]string, 0, len(m.Commands))
	for _, command := range m.Commands {
		result = append(result, command.Name)
	}
	sort.Strings(result)
	return result
}
