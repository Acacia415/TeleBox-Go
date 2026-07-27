package pluginmanager

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Acacia415/TeleBox-Go/internal/plugin"
	"github.com/Acacia415/TeleBox-Go/internal/pluginexternal"
	"github.com/Acacia415/TeleBox-Go/internal/pluginmarket"
	"github.com/Acacia415/TeleBox-Go/internal/service"
	"github.com/Acacia415/TeleBox-Go/internal/storage"
	"github.com/Acacia415/TeleBox-Go/pkg/pluginapi"
)

// Controller keeps the package manager, live registry, and persisted plugin
// state synchronized. Optional plugins are always loaded as child processes.
type Controller struct {
	market   *pluginmarket.Manager
	registry *plugin.Registry
	services service.Container
	workRoot string
	mu       sync.Mutex
}

func New(
	market *pluginmarket.Manager,
	registry *plugin.Registry,
	services service.Container,
	workRoot string,
) (*Controller, error) {
	if market == nil {
		return nil, errors.New("plugin market is required")
	}
	if registry == nil {
		return nil, errors.New("plugin registry is required")
	}
	if strings.TrimSpace(workRoot) == "" {
		return nil, errors.New("plugin work root is required")
	}
	return &Controller{
		market:   market,
		registry: registry,
		services: services,
		workRoot: workRoot,
	}, nil
}

// LoadInstalled discovers already-installed packages and registers them
// without enabling them. The application restores their persisted state after
// core startup.
func (c *Controller) LoadInstalled() error {
	installed, scanErr := c.market.Installed()
	var loadErrors []error
	if scanErr != nil {
		loadErrors = append(loadErrors, scanErr)
	}
	for _, item := range installed {
		if err := c.register(item); err != nil {
			loadErrors = append(loadErrors, fmt.Errorf(
				"register installed plugin %q: %w",
				item.Manifest.Name,
				err,
			))
		}
	}
	return errors.Join(loadErrors...)
}

func (c *Controller) Installed() ([]pluginmarket.Installed, error) {
	return c.market.Installed()
}

func (c *Controller) Search(
	ctx context.Context,
	query string,
) ([]pluginapi.CatalogPlugin, error) {
	return c.market.Search(ctx, query)
}

func (c *Controller) Install(
	ctx context.Context,
	name string,
	version string,
) (pluginmarket.InstallResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.install(ctx, name, version, true)
}

func (c *Controller) InstallArchive(
	ctx context.Context,
	archive []byte,
	format string,
) (pluginmarket.InstallResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	manifest, err := c.market.InspectArchive(archive, format)
	if err != nil {
		return pluginmarket.InstallResult{}, err
	}
	name := normalizePackageReference(manifest.Name)
	if name == "core" {
		return pluginmarket.InstallResult{}, errors.New(
			"core is part of TeleBox and cannot be installed separately",
		)
	}
	previousStatus, wasRegistered := c.registry.Status(name)
	if wasRegistered {
		if err := c.registry.Remove(ctx, name); err != nil {
			return pluginmarket.InstallResult{}, err
		}
	}
	result, err := c.market.InstallArchive(ctx, archive, format)
	if err != nil {
		restoreErr := c.restore(name, previousStatus, wasRegistered)
		return pluginmarket.InstallResult{}, errors.Join(err, restoreErr)
	}
	if err := c.register(result.Installed); err != nil {
		return result, err
	}
	shouldEnable := activationAfterInstall(true, previousStatus, wasRegistered)
	if shouldEnable {
		if err := c.registry.Enable(ctx, name); err != nil {
			return result, err
		}
	}
	if err := c.persist(ctx, name, shouldEnable); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Controller) Export(name, destination string) (pluginmarket.Installed, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.market.Export(normalizePackageReference(name), destination)
}

func (c *Controller) install(
	ctx context.Context,
	name string,
	version string,
	forceEnable bool,
) (pluginmarket.InstallResult, error) {
	name = normalizePackageReference(name)
	if name == "core" {
		return pluginmarket.InstallResult{}, errors.New("core is part of TeleBox and cannot be installed separately")
	}

	previousStatus, wasRegistered := c.registry.Status(name)
	if wasRegistered {
		if err := c.registry.Remove(ctx, name); err != nil {
			return pluginmarket.InstallResult{}, err
		}
	}
	result, err := c.market.Install(ctx, name, version)
	if err != nil {
		restoreErr := c.restore(name, previousStatus, wasRegistered)
		return pluginmarket.InstallResult{}, errors.Join(err, restoreErr)
	}
	if err := c.register(result.Installed); err != nil {
		return result, err
	}
	shouldEnable := activationAfterInstall(
		forceEnable,
		previousStatus,
		wasRegistered,
	)
	if shouldEnable {
		if err := c.registry.Enable(ctx, name); err != nil {
			persistErr := c.persist(context.WithoutCancel(ctx), name, false)
			return result, errors.Join(err, persistErr)
		}
	}
	if err := c.persist(ctx, name, shouldEnable); err != nil {
		var rollbackErr error
		if shouldEnable {
			rollbackErr = c.registry.Disable(context.WithoutCancel(ctx), name)
		}
		return result, errors.Join(err, rollbackErr)
	}
	return result, nil
}

func (c *Controller) Update(
	ctx context.Context,
	name string,
) (pluginmarket.InstallResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.install(ctx, name, "latest", false)
}

func (c *Controller) UpdateAll(
	ctx context.Context,
) ([]pluginmarket.InstallResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	installed, err := c.market.Installed()
	if err != nil && len(installed) == 0 {
		return nil, err
	}
	sort.Slice(installed, func(i, j int) bool {
		return installed[i].Manifest.Name < installed[j].Manifest.Name
	})
	results := make([]pluginmarket.InstallResult, 0, len(installed))
	var updateErrors []error
	if err != nil {
		updateErrors = append(updateErrors, err)
	}
	for _, item := range installed {
		result, updateErr := c.install(ctx, item.Manifest.Name, "latest", false)
		if updateErr != nil {
			updateErrors = append(updateErrors, fmt.Errorf(
				"update %s: %w",
				item.Manifest.Name,
				updateErr,
			))
			continue
		}
		results = append(results, result)
	}
	return results, errors.Join(updateErrors...)
}

func (c *Controller) Remove(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	name = normalizePackageReference(name)
	if name == "core" {
		return errors.New("core is part of TeleBox and cannot be removed")
	}
	previousStatus, wasRegistered := c.registry.Status(name)
	if wasRegistered {
		if err := c.registry.Remove(ctx, name); err != nil {
			return err
		}
	}
	if _, err := c.market.Remove(name); err != nil {
		restoreErr := c.restore(name, previousStatus, wasRegistered)
		return errors.Join(err, restoreErr)
	}
	return c.persist(ctx, name, false)
}

func (c *Controller) Enable(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	name = normalizePackageReference(name)
	if err := c.registry.Enable(ctx, name); err != nil {
		return err
	}
	if err := c.persist(ctx, name, true); err != nil {
		rollbackErr := c.registry.Disable(context.WithoutCancel(ctx), name)
		return errors.Join(err, rollbackErr)
	}
	return nil
}

func (c *Controller) Disable(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	name = normalizePackageReference(name)
	if name == "core" {
		return errors.New("core plugin cannot be disabled")
	}
	previous, exists := c.registry.Status(name)
	if !exists {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	if err := c.registry.Disable(ctx, name); err != nil {
		return err
	}
	if err := c.persist(ctx, name, false); err != nil {
		var rollbackErr error
		if previous.Enabled {
			rollbackErr = c.registry.Enable(context.WithoutCancel(ctx), name)
		}
		return errors.Join(err, rollbackErr)
	}
	return nil
}

func (c *Controller) register(installed pluginmarket.Installed) error {
	external, err := pluginexternal.New(installed, c.services, c.workRoot)
	if err != nil {
		return err
	}
	return c.registry.Add(external)
}

func (c *Controller) restore(
	name string,
	previous plugin.Status,
	wasRegistered bool,
) error {
	if !wasRegistered {
		return nil
	}
	installed, exists, err := c.market.Status(name)
	if err != nil || !exists {
		if err == nil {
			err = fmt.Errorf("previous plugin %q is no longer installed", name)
		}
		return err
	}
	if err := c.register(installed); err != nil {
		return err
	}
	if previous.Enabled {
		return c.registry.Enable(context.Background(), name)
	}
	return nil
}

func (c *Controller) persist(ctx context.Context, name string, enabled bool) error {
	return c.services.Storage.SetPluginState(ctx, storage.PluginState{
		Name:    name,
		Enabled: enabled,
	})
}

func normalizePackageReference(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func activationAfterInstall(
	forceEnable bool,
	previous plugin.Status,
	wasRegistered bool,
) bool {
	return forceEnable || (wasRegistered && previous.Enabled)
}
