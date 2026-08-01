package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultPluginCatalogURL = "https://github.com/Acacia415/TeleBox-Go/" +
		"releases/download/plugin-registry/plugin-catalog.json"
	legacyPluginCatalogURL = "https://github.com/Acacia415/TeleBox-Go/" +
		"releases/latest/download/plugin-catalog.json"
)

// Config contains all process-level configuration. Plugin-specific settings
// will be stored separately so one plugin cannot accidentally receive another
// plugin's secrets.
type Config struct {
	SourcePath string         `json:"-"`
	Telegram   TelegramConfig `json:"telegram"`
	Commands   CommandConfig  `json:"commands"`
	Storage    StorageConfig  `json:"storage"`
	Tools      ToolConfig     `json:"tools"`
	HTTP       HTTPConfig     `json:"http"`
	Plugins    PluginConfig   `json:"plugins"`
	Logging    LoggingConfig  `json:"logging"`
}

type TelegramConfig struct {
	APIID       int    `json:"api_id"`
	APIHash     string `json:"api_hash"`
	SessionFile string `json:"session_file"`
	LoginMode   string `json:"login_mode"`
}

type CommandConfig struct {
	Prefixes            []string `json:"prefixes"`
	OwnerIDs            []int64  `json:"owner_ids"`
	MaxConcurrent       int      `json:"max_concurrent"`
	QueueCapacity       int      `json:"queue_capacity"`
	PerSenderIntervalMS int      `json:"per_sender_interval_ms"`
}

type StorageConfig struct {
	Path             string `json:"path"`
	AssetsPath       string `json:"assets_path"`
	LegacyAssetsPath string `json:"legacy_assets_path"`
}

type ToolConfig struct {
	MaxConcurrent int `json:"max_concurrent"`
}

type HTTPConfig struct {
	TimeoutSeconds   int   `json:"timeout_seconds"`
	MaxConcurrent    int   `json:"max_concurrent"`
	MaxResponseBytes int64 `json:"max_response_bytes"`
}

type PluginConfig struct {
	Enabled         []string `json:"enabled"`
	Disabled        []string `json:"disabled"`
	Directory       string   `json:"directory"`
	CatalogURL      string   `json:"catalog_url"`
	MaxArchiveBytes int64    `json:"max_archive_bytes"`
}

type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
	Path   string `json:"path"`
}

func Default() Config {
	return Config{
		Telegram: TelegramConfig{
			SessionFile: "data/session.json",
			LoginMode:   "existing",
		},
		Commands: CommandConfig{
			Prefixes:            []string{"-"},
			OwnerIDs:            []int64{},
			MaxConcurrent:       8,
			QueueCapacity:       128,
			PerSenderIntervalMS: 250,
		},
		Storage: StorageConfig{
			Path:             "data/telebox.db",
			AssetsPath:       "data/assets",
			LegacyAssetsPath: "data/legacy-assets",
		},
		Tools: ToolConfig{
			MaxConcurrent: 4,
		},
		HTTP: HTTPConfig{
			TimeoutSeconds:   30,
			MaxConcurrent:    16,
			MaxResponseBytes: 8 << 20,
		},
		Plugins: PluginConfig{
			Enabled:         []string{},
			Disabled:        []string{},
			Directory:       "data/plugins",
			CatalogURL:      DefaultPluginCatalogURL,
			MaxArchiveBytes: 128 << 20,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
			Path:   "data/logs/telebox.log",
		},
	}
}

// Load reads a strict JSON config, applies environment overrides, resolves
// relative data paths against the config directory, and validates the result.
func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, fmt.Errorf("decode config %q: trailing JSON value", path)
		}
		return Config{}, fmt.Errorf("decode config %q: %w", path, err)
	}

	if err := applyEnvironment(&cfg); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(cfg.Plugins.CatalogURL) == legacyPluginCatalogURL {
		cfg.Plugins.CatalogURL = DefaultPluginCatalogURL
	}

	baseDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("resolve config directory: %w", err)
	}
	cfg.resolvePaths(baseDir)
	cfg.SourcePath = resolvePath(baseDir, filepath.Base(path))

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var problems []string

	if c.Telegram.APIID <= 0 {
		problems = append(problems, "telegram.api_id must be greater than zero")
	}
	if strings.TrimSpace(c.Telegram.APIHash) == "" {
		problems = append(problems, "telegram.api_hash is required")
	}
	if strings.TrimSpace(c.Telegram.SessionFile) == "" {
		problems = append(problems, "telegram.session_file is required")
	}
	switch strings.ToLower(c.Telegram.LoginMode) {
	case "existing", "qr", "phone":
	default:
		problems = append(problems, "telegram.login_mode must be existing, qr, or phone")
	}
	if strings.TrimSpace(c.Storage.Path) == "" {
		problems = append(problems, "storage.path is required")
	}
	if strings.TrimSpace(c.Storage.AssetsPath) == "" {
		problems = append(problems, "storage.assets_path is required")
	}
	if strings.TrimSpace(c.Storage.LegacyAssetsPath) == "" {
		problems = append(problems, "storage.legacy_assets_path is required")
	}
	if pathsOverlap(c.Storage.AssetsPath, c.Storage.LegacyAssetsPath) {
		problems = append(problems, "storage.assets_path and storage.legacy_assets_path must not overlap")
	}
	if c.Tools.MaxConcurrent <= 0 {
		problems = append(problems, "tools.max_concurrent must be greater than zero")
	}
	if c.HTTP.TimeoutSeconds <= 0 {
		problems = append(problems, "http.timeout_seconds must be greater than zero")
	}
	if c.HTTP.MaxConcurrent <= 0 {
		problems = append(problems, "http.max_concurrent must be greater than zero")
	}
	if c.HTTP.MaxResponseBytes <= 0 {
		problems = append(problems, "http.max_response_bytes must be greater than zero")
	}
	if strings.TrimSpace(c.Plugins.Directory) == "" {
		problems = append(problems, "plugins.directory is required")
	}
	if c.Plugins.MaxArchiveBytes <= 0 {
		problems = append(problems, "plugins.max_archive_bytes must be greater than zero")
	}
	if parsed, err := url.Parse(c.Plugins.CatalogURL); err != nil {
		problems = append(problems, "plugins.catalog_url is invalid")
	} else if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		problems = append(problems,
			"plugins.catalog_url must be an HTTPS URL without credentials",
		)
	}
	if len(c.Commands.Prefixes) == 0 {
		problems = append(problems, "commands.prefixes must contain at least one prefix")
	}
	if c.Commands.MaxConcurrent <= 0 {
		problems = append(problems, "commands.max_concurrent must be greater than zero")
	}
	if c.Commands.QueueCapacity < c.Commands.MaxConcurrent {
		problems = append(problems, "commands.queue_capacity must be at least commands.max_concurrent")
	}
	if c.Commands.PerSenderIntervalMS < 0 {
		problems = append(problems, "commands.per_sender_interval_ms cannot be negative")
	}

	seenPrefixes := make(map[string]struct{}, len(c.Commands.Prefixes))
	for _, prefix := range c.Commands.Prefixes {
		if prefix == "" {
			problems = append(problems, "commands.prefixes cannot contain an empty prefix")
			continue
		}
		if strings.ContainsAny(prefix, " \t\r\n") {
			problems = append(problems, "commands.prefixes cannot contain whitespace")
		}
		if _, exists := seenPrefixes[prefix]; exists {
			problems = append(problems, fmt.Sprintf("commands.prefixes contains duplicate %q", prefix))
		}
		seenPrefixes[prefix] = struct{}{}
	}

	switch strings.ToLower(c.Logging.Level) {
	case "debug", "info", "warning", "warn", "error", "silent", "off":
	default:
		problems = append(problems, "logging.level must be debug, info, warn, or error")
	}
	switch strings.ToLower(c.Logging.Format) {
	case "text", "json":
	default:
		problems = append(problems, "logging.format must be text or json")
	}
	if strings.TrimSpace(c.Logging.Path) == "" {
		problems = append(problems, "logging.path is required")
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c *Config) resolvePaths(baseDir string) {
	c.Telegram.SessionFile = resolvePath(baseDir, c.Telegram.SessionFile)
	c.Storage.Path = resolvePath(baseDir, c.Storage.Path)
	c.Storage.AssetsPath = resolvePath(baseDir, c.Storage.AssetsPath)
	c.Storage.LegacyAssetsPath = resolvePath(baseDir, c.Storage.LegacyAssetsPath)
	c.Plugins.Directory = resolvePath(baseDir, c.Plugins.Directory)
	c.Logging.Path = resolvePath(baseDir, c.Logging.Path)
}

func resolvePath(baseDir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func pathsOverlap(first, second string) bool {
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, child string) bool {
	parent, parentErr := filepath.Abs(parent)
	child, childErr := filepath.Abs(child)
	if parentErr != nil || childErr != nil {
		return strings.EqualFold(filepath.Clean(parent), filepath.Clean(child))
	}
	relative, err := filepath.Rel(parent, child)
	return err == nil &&
		(relative == "." ||
			(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}

func applyEnvironment(cfg *Config) error {
	if value, ok := os.LookupEnv("TELEBOX_API_ID"); ok {
		apiID, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("parse TELEBOX_API_ID: %w", err)
		}
		cfg.Telegram.APIID = apiID
	}
	if value, ok := os.LookupEnv("TELEBOX_API_HASH"); ok {
		cfg.Telegram.APIHash = value
	}
	if value, ok := os.LookupEnv("TELEBOX_SESSION_FILE"); ok {
		cfg.Telegram.SessionFile = value
	}
	if value, ok := os.LookupEnv("TELEBOX_STORAGE_PATH"); ok {
		cfg.Storage.Path = value
	}
	if value, ok := os.LookupEnv("TELEBOX_ASSETS_PATH"); ok {
		cfg.Storage.AssetsPath = value
	}
	if value, ok := os.LookupEnv("TELEBOX_LEGACY_ASSETS_PATH"); ok {
		cfg.Storage.LegacyAssetsPath = value
	}
	if value, ok := os.LookupEnv("TELEBOX_PLUGIN_DIR"); ok {
		cfg.Plugins.Directory = value
	}
	if value, ok := os.LookupEnv("TELEBOX_PLUGIN_CATALOG"); ok {
		cfg.Plugins.CatalogURL = strings.TrimSpace(value)
	}
	if value, ok := os.LookupEnv("TELEBOX_LOGIN_MODE"); ok {
		cfg.Telegram.LoginMode = strings.ToLower(strings.TrimSpace(value))
	}
	if value, ok := os.LookupEnv("TELEBOX_LOG_LEVEL"); ok {
		cfg.Logging.Level = strings.ToLower(strings.TrimSpace(value))
	}
	if value, ok := os.LookupEnv("TELEBOX_LOG_FORMAT"); ok {
		cfg.Logging.Format = strings.ToLower(strings.TrimSpace(value))
	}
	if value, ok := os.LookupEnv("TELEBOX_LOG_PATH"); ok {
		cfg.Logging.Path = value
	}
	return nil
}
