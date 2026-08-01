package plugin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Acacia415/TeleBox-Go/internal/command"
)

var pluginNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type entry struct {
	plugin  Plugin
	enabled bool
}

type Registry struct {
	mu      sync.RWMutex
	router  *command.Router
	entries map[string]*entry
	order   []string
}

func NewRegistry(router *command.Router) *Registry {
	return &Registry{
		router:  router,
		entries: make(map[string]*entry),
	}
}

func (r *Registry) Add(candidate Plugin) error {
	if candidate == nil {
		return errors.New("plugin is nil")
	}
	metadata := candidate.Metadata()
	metadata.Name = normalizeName(metadata.Name)
	if !pluginNamePattern.MatchString(metadata.Name) {
		return fmt.Errorf("invalid plugin name %q", metadata.Name)
	}
	if strings.TrimSpace(metadata.Version) == "" {
		return fmt.Errorf("plugin %q has no version", metadata.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[metadata.Name]; exists {
		return fmt.Errorf("plugin %q is already registered", metadata.Name)
	}
	r.entries[metadata.Name] = &entry{plugin: candidate}
	return nil
}

func (r *Registry) Enable(ctx context.Context, name string) error {
	name = normalizeName(name)

	r.mu.Lock()
	defer r.mu.Unlock()
	item, exists := r.entries[name]
	if !exists {
		return fmt.Errorf("plugin %q is not registered", name)
	}
	if item.enabled {
		return nil
	}

	if err := item.plugin.Start(ctx); err != nil {
		return fmt.Errorf("start plugin %q: %w", name, err)
	}
	if err := r.router.Register(name, item.plugin.Commands()...); err != nil {
		_ = item.plugin.Stop(ctx)
		return fmt.Errorf("register commands for plugin %q: %w", name, err)
	}

	item.enabled = true
	r.order = append(r.order, name)
	return nil
}

func (r *Registry) Disable(ctx context.Context, name string) error {
	name = normalizeName(name)

	r.mu.Lock()
	defer r.mu.Unlock()
	item, exists := r.entries[name]
	if !exists {
		return fmt.Errorf("plugin %q is not registered", name)
	}
	if !item.enabled {
		return nil
	}

	r.router.UnregisterPlugin(name)
	item.enabled = false
	r.removeFromOrder(name)
	if err := item.plugin.Stop(ctx); err != nil {
		return fmt.Errorf("stop plugin %q: %w", name, err)
	}
	return nil
}

// Remove stops an enabled plugin and removes it from the registry. The core
// plugin is intentionally permanent because it owns the administration
// commands used to recover the installation.
func (r *Registry) Remove(ctx context.Context, name string) error {
	name = normalizeName(name)
	if name == "core" {
		return errors.New("core plugin cannot be removed")
	}
	if err := r.Disable(ctx, name); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[name]; !exists {
		return fmt.Errorf("plugin %q is not registered", name)
	}
	delete(r.entries, name)
	r.removeFromOrder(name)
	return nil
}

func (r *Registry) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var shutdownErrors []error
	for index := len(r.order) - 1; index >= 0; index-- {
		name := r.order[index]
		item := r.entries[name]
		r.router.UnregisterPlugin(name)
		item.enabled = false
		if err := item.plugin.Stop(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("stop plugin %q: %w", name, err))
		}
	}
	r.order = nil
	return errors.Join(shutdownErrors...)
}

func (r *Registry) List() []Status {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Status, 0, len(r.entries))
	for _, item := range r.entries {
		result = append(result, Status{
			Metadata: item.plugin.Metadata(),
			Enabled:  item.enabled,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Metadata.Name < result[j].Metadata.Name
	})
	return result
}

func (r *Registry) Status(name string) (Status, bool) {
	name = normalizeName(name)
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, exists := r.entries[name]
	if !exists {
		return Status{}, false
	}
	return Status{
		Metadata: item.plugin.Metadata(),
		Enabled:  item.enabled,
	}, true
}

func (r *Registry) MessageListeners() []Listener {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Listener, 0)
	for _, name := range r.order {
		item := r.entries[name]
		if !item.enabled {
			continue
		}
		if listener, ok := item.plugin.(MessageListener); ok {
			if conditional, ok := listener.(ConditionalMessageListener); ok &&
				!conditional.ListensToMessages() {
				continue
			}
			result = append(result, Listener{Plugin: name, Handler: listener})
		}
	}
	return result
}

func (r *Registry) EditedMessageListeners() []EditedListener {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]EditedListener, 0)
	for _, name := range r.order {
		item := r.entries[name]
		if !item.enabled {
			continue
		}
		if listener, ok := item.plugin.(EditedMessageListener); ok {
			if conditional, ok := listener.(ConditionalEditedMessageListener); ok &&
				!conditional.ListensToEditedMessages() {
				continue
			}
			result = append(result, EditedListener{Plugin: name, Handler: listener})
		}
	}
	return result
}

func (r *Registry) removeFromOrder(name string) {
	for index, item := range r.order {
		if item == name {
			r.order = append(r.order[:index], r.order[index+1:]...)
			return
		}
	}
}

func normalizeName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
