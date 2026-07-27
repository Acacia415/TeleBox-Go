package pluginmarket

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

const maxCatalogBytes = 2 << 20
const catalogTTL = 5 * time.Minute

type Config struct {
	Directory       string
	CatalogURL      string
	MaxArchiveBytes int64
	Client          *http.Client
	GOOS            string
	GOARCH          string
}

type Installed struct {
	Manifest   pluginapi.Manifest
	Directory  string
	Executable string
}

type InstallResult struct {
	Installed Installed
	Previous  string
	Updated   bool
}

type Manager struct {
	directory       string
	catalogURL      string
	maxArchiveBytes int64
	client          *http.Client
	goos            string
	goarch          string

	mu        sync.Mutex
	catalogMu sync.Mutex
	catalog   pluginapi.Catalog
	catalogAt time.Time
}

func New(cfg Config) (*Manager, error) {
	if strings.TrimSpace(cfg.Directory) == "" {
		return nil, errors.New("plugin directory is required")
	}
	if strings.TrimSpace(cfg.CatalogURL) == "" {
		return nil, errors.New("plugin catalog URL is required")
	}
	parsedCatalog, err := url.Parse(cfg.CatalogURL)
	if err != nil || parsedCatalog.Scheme != "https" ||
		parsedCatalog.Host == "" || parsedCatalog.User != nil {
		return nil, errors.New(
			"plugin catalog must be an HTTPS URL without credentials",
		)
	}
	if cfg.MaxArchiveBytes <= 0 {
		return nil, errors.New("plugin archive limit must be greater than zero")
	}
	client := cfg.Client
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxIdleConnsPerHost = 2
		client = &http.Client{
			Transport: transport,
			Timeout:   2 * time.Minute,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("stopped after 5 redirects")
				}
				if request.URL.Scheme != "https" ||
					request.URL.Host == "" ||
					request.URL.User != nil {
					return errors.New("plugin download redirect is not a safe HTTPS URL")
				}
				return nil
			},
		}
	}
	goos := cfg.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := cfg.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return &Manager{
		directory:       filepath.Clean(cfg.Directory),
		catalogURL:      cfg.CatalogURL,
		maxArchiveBytes: cfg.MaxArchiveBytes,
		client:          client,
		goos:            goos,
		goarch:          goarch,
	}, nil
}

func (m *Manager) Catalog(ctx context.Context) (pluginapi.Catalog, error) {
	m.catalogMu.Lock()
	defer m.catalogMu.Unlock()
	if !m.catalogAt.IsZero() && time.Since(m.catalogAt) < catalogTTL {
		return cloneCatalog(m.catalog), nil
	}
	body, err := m.get(ctx, m.catalogURL, maxCatalogBytes)
	if err != nil {
		return pluginapi.Catalog{}, fmt.Errorf("download plugin catalog: %w", err)
	}
	var catalog pluginapi.Catalog
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return pluginapi.Catalog{}, fmt.Errorf("decode plugin catalog: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return pluginapi.Catalog{}, err
	}
	if err := catalog.Validate(); err != nil {
		return pluginapi.Catalog{}, fmt.Errorf("validate plugin catalog: %w", err)
	}
	m.catalog = catalog
	m.catalogAt = time.Now()
	return cloneCatalog(catalog), nil
}

func (m *Manager) Search(
	ctx context.Context,
	query string,
) ([]pluginapi.CatalogPlugin, error) {
	catalog, err := m.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	result := make([]pluginapi.CatalogPlugin, 0)
	for _, item := range catalog.SortedPlugins() {
		if query == "" ||
			strings.Contains(item.Name, query) ||
			strings.Contains(strings.ToLower(item.Description), query) {
			result = append(result, item)
		}
	}
	return result, nil
}

func (m *Manager) Installed() ([]Installed, error) {
	entries, err := os.ReadDir(m.directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plugin directory: %w", err)
	}
	result := make([]Installed, 0, len(entries))
	var scanErrors []error
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		item, err := m.readInstalled(entry.Name())
		if err != nil {
			scanErrors = append(scanErrors, fmt.Errorf(
				"inspect installed plugin %q: %w",
				entry.Name(),
				err,
			))
			continue
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Manifest.Name < result[j].Manifest.Name
	})
	return result, errors.Join(scanErrors...)
}

func (m *Manager) Status(name string) (Installed, bool, error) {
	if !validPluginName(name) {
		return Installed{}, false, fmt.Errorf("invalid plugin name %q", name)
	}
	item, err := m.readInstalled(name)
	if errors.Is(err, os.ErrNotExist) {
		return Installed{}, false, nil
	}
	if err != nil {
		return Installed{}, false, err
	}
	return item, true, nil
}

func (m *Manager) Install(
	ctx context.Context,
	name string,
	version string,
) (InstallResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name = strings.ToLower(strings.TrimSpace(name))
	if !validPluginName(name) {
		return InstallResult{}, fmt.Errorf("invalid plugin name %q", name)
	}
	catalog, err := m.Catalog(ctx)
	if err != nil {
		return InstallResult{}, err
	}
	item, exists := catalog.Find(name)
	if !exists {
		return InstallResult{}, fmt.Errorf("plugin %q was not found", name)
	}
	release, exists := selectRelease(item, version)
	if !exists {
		return InstallResult{}, fmt.Errorf(
			"plugin %q version %q was not found",
			name,
			version,
		)
	}
	artifact, exists := release.ArtifactFor(m.goos, m.goarch)
	if !exists {
		return InstallResult{}, fmt.Errorf(
			"plugin %q has no %s/%s build",
			name,
			m.goos,
			m.goarch,
		)
	}
	archive, err := m.get(ctx, artifact.URL, m.maxArchiveBytes)
	if err != nil {
		return InstallResult{}, fmt.Errorf("download plugin %q: %w", name, err)
	}
	sum := sha256.Sum256(archive)
	actualHash := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actualHash, artifact.SHA256) {
		return InstallResult{}, fmt.Errorf(
			"plugin %q checksum mismatch",
			name,
		)
	}

	if err := os.MkdirAll(m.directory, 0o700); err != nil {
		return InstallResult{}, fmt.Errorf("create plugin directory: %w", err)
	}
	stage, err := os.MkdirTemp(m.directory, ".install-"+name+"-")
	if err != nil {
		return InstallResult{}, fmt.Errorf("create plugin staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := extractArchive(
		bytes.NewReader(archive),
		int64(len(archive)),
		artifact.Format,
		stage,
		m.maxArchiveBytes*4,
	); err != nil {
		return InstallResult{}, fmt.Errorf("extract plugin %q: %w", name, err)
	}
	manifest, err := readManifest(filepath.Join(stage, "plugin.json"))
	if err != nil {
		return InstallResult{}, err
	}
	if manifest.Name != name || manifest.Version != release.Version {
		return InstallResult{}, errors.New(
			"plugin manifest does not match the selected catalog release",
		)
	}
	executable, err := resolveExecutable(stage, manifest.Executable)
	if err != nil {
		return InstallResult{}, err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(executable, 0o700); err != nil {
			return InstallResult{}, fmt.Errorf("make plugin executable: %w", err)
		}
	}

	target := filepath.Join(m.directory, name)
	previous := ""
	if installed, readErr := m.readInstalled(name); readErr == nil {
		previous = installed.Manifest.Version
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return InstallResult{}, readErr
	}
	backup := ""
	if _, statErr := os.Stat(target); statErr == nil {
		backup = filepath.Join(
			m.directory,
			fmt.Sprintf(".backup-%s-%d", name, time.Now().UnixNano()),
		)
		if err := os.Rename(target, backup); err != nil {
			return InstallResult{}, fmt.Errorf("stage previous plugin: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return InstallResult{}, fmt.Errorf("inspect installed plugin: %w", statErr)
	}
	if err := os.Rename(stage, target); err != nil {
		if backup != "" {
			_ = os.Rename(backup, target)
		}
		return InstallResult{}, fmt.Errorf("activate plugin %q: %w", name, err)
	}
	if backup != "" {
		if err := os.RemoveAll(backup); err != nil {
			return InstallResult{}, fmt.Errorf("remove previous plugin backup: %w", err)
		}
	}
	installed, err := m.readInstalled(name)
	if err != nil {
		return InstallResult{}, err
	}
	return InstallResult{
		Installed: installed,
		Previous:  previous,
		Updated:   previous != "" && previous != installed.Manifest.Version,
	}, nil
}

func (m *Manager) Remove(name string) (Installed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name = strings.ToLower(strings.TrimSpace(name))
	if !validPluginName(name) {
		return Installed{}, fmt.Errorf("invalid plugin name %q", name)
	}
	installed, err := m.readInstalled(name)
	if err != nil {
		return Installed{}, err
	}
	target := filepath.Join(m.directory, name)
	trash := filepath.Join(
		m.directory,
		fmt.Sprintf(".removed-%s-%d", name, time.Now().UnixNano()),
	)
	if err := os.Rename(target, trash); err != nil {
		return Installed{}, fmt.Errorf("deactivate plugin %q: %w", name, err)
	}
	if err := os.RemoveAll(trash); err != nil {
		_ = os.Rename(trash, target)
		return Installed{}, fmt.Errorf("remove plugin %q: %w", name, err)
	}
	return installed, nil
}

func (m *Manager) readInstalled(name string) (Installed, error) {
	root := filepath.Join(m.directory, name)
	manifest, err := readManifest(filepath.Join(root, "plugin.json"))
	if err != nil {
		return Installed{}, err
	}
	if manifest.Name != name {
		return Installed{}, fmt.Errorf(
			"plugin directory %q contains manifest for %q",
			name,
			manifest.Name,
		)
	}
	executable, err := resolveExecutable(root, manifest.Executable)
	if err != nil {
		return Installed{}, err
	}
	return Installed{
		Manifest:   manifest,
		Directory:  root,
		Executable: executable,
	}, nil
}

func (m *Manager) get(
	ctx context.Context,
	target string,
	limit int64,
) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	request.Header.Set("User-Agent", "TeleBox-Go-Plugin-Manager/1")
	response, err := m.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read HTTP response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}

func selectRelease(
	item pluginapi.CatalogPlugin,
	version string,
) (pluginapi.PluginRelease, bool) {
	version = strings.TrimSpace(version)
	if version == "" || version == "latest" {
		return item.Latest()
	}
	for _, release := range item.Releases {
		if release.Version == version ||
			strings.TrimPrefix(release.Version, "v") == strings.TrimPrefix(version, "v") {
			return release, true
		}
	}
	return pluginapi.PluginRelease{}, false
}

func readManifest(path string) (pluginapi.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pluginapi.Manifest{}, err
	}
	var manifest pluginapi.Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return pluginapi.Manifest{}, fmt.Errorf("decode plugin manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return pluginapi.Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return pluginapi.Manifest{}, fmt.Errorf("validate plugin manifest: %w", err)
	}
	return manifest, nil
}

func resolveExecutable(root, relative string) (string, error) {
	normalized := strings.ReplaceAll(relative, `\`, "/")
	target := filepath.Join(root, filepath.FromSlash(normalized))
	relativeToRoot, err := filepath.Rel(root, target)
	if err != nil || relativeToRoot == ".." ||
		strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
		return "", errors.New("plugin executable escapes installation directory")
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", fmt.Errorf("inspect plugin executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("plugin executable is not a regular file")
	}
	return target, nil
}

func validPluginName(name string) bool {
	manifest := pluginapi.Manifest{
		SchemaVersion: pluginapi.ManifestSchemaVersion,
		APIVersion:    pluginapi.HostAPIVersion,
		Name:          strings.ToLower(strings.TrimSpace(name)),
		Version:       "v1.0.0",
		Description:   "validation",
		Executable:    "plugin",
		Commands:      []pluginapi.Command{{Name: "validation"}},
	}
	return manifest.Validate() == nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains a trailing value")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

func cloneCatalog(source pluginapi.Catalog) pluginapi.Catalog {
	result := pluginapi.Catalog{
		SchemaVersion: source.SchemaVersion,
		Plugins:       make([]pluginapi.CatalogPlugin, len(source.Plugins)),
	}
	for pluginIndex, item := range source.Plugins {
		result.Plugins[pluginIndex] = item
		result.Plugins[pluginIndex].Releases = make(
			[]pluginapi.PluginRelease,
			len(item.Releases),
		)
		for releaseIndex, release := range item.Releases {
			result.Plugins[pluginIndex].Releases[releaseIndex] = release
			result.Plugins[pluginIndex].Releases[releaseIndex].Artifacts =
				append([]pluginapi.Artifact(nil), release.Artifacts...)
		}
	}
	return result
}
