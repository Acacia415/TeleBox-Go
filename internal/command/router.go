package command

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

var (
	ErrPermissionDenied = errors.New("command permission denied")
	ErrRouteConflict    = errors.New("command route conflict")
)

type Request struct {
	Message telegram.Message
	Prefix  string
	Command string
	Args    []string
	RawArgs string
}

type Handler func(context.Context, Request) error

type Definition struct {
	Name        string
	Aliases     []string
	Description string
	OwnerOnly   bool
	Handler     Handler
}

type RouteInfo struct {
	Plugin      string
	Name        string
	Aliases     []string
	Description string
	OwnerOnly   bool
}

type route struct {
	plugin     string
	definition Definition
}

type DispatchResult struct {
	Matched bool
	Plugin  string
	Command string
}

type Router struct {
	mu       sync.RWMutex
	prefixes []string
	owners   map[int64]struct{}
	routes   map[string]route
}

func NewRouter(prefixes []string, ownerIDs []int64) (*Router, error) {
	normalizedPrefixes, err := normalizePrefixes(prefixes)
	if err != nil {
		return nil, err
	}

	owners := make(map[int64]struct{}, len(ownerIDs))
	for _, id := range ownerIDs {
		owners[id] = struct{}{}
	}
	return &Router{
		prefixes: normalizedPrefixes,
		owners:   owners,
		routes:   make(map[string]route),
	}, nil
}

func (r *Router) Register(pluginName string, definitions ...Definition) error {
	pluginName = normalizeName(pluginName)
	if pluginName == "" {
		return errors.New("plugin name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	pending := make(map[string]route)
	for _, definition := range definitions {
		definition.Name = normalizeName(definition.Name)
		if definition.Name == "" {
			return errors.New("command name is required")
		}
		if definition.Handler == nil {
			return fmt.Errorf("command %q has no handler", definition.Name)
		}

		names := append([]string{definition.Name}, definition.Aliases...)
		normalizedAliases := make([]string, 0, len(definition.Aliases))
		for index, name := range names {
			name = normalizeName(name)
			if name == "" {
				return fmt.Errorf("command %q contains an empty alias", definition.Name)
			}
			if index > 0 {
				normalizedAliases = append(normalizedAliases, name)
			}
			if existing, ok := r.routes[name]; ok {
				return fmt.Errorf("%w: %q is already owned by plugin %q", ErrRouteConflict, name, existing.plugin)
			}
			if _, ok := pending[name]; ok {
				return fmt.Errorf("%w: duplicate route %q in plugin %q", ErrRouteConflict, name, pluginName)
			}
			pending[name] = route{plugin: pluginName, definition: definition}
		}
		definition.Aliases = normalizedAliases
		pending[definition.Name] = route{plugin: pluginName, definition: definition}
		for _, alias := range normalizedAliases {
			pending[alias] = route{plugin: pluginName, definition: definition}
		}
	}

	for name, entry := range pending {
		r.routes[name] = entry
	}
	return nil
}

func (r *Router) UnregisterPlugin(pluginName string) {
	pluginName = normalizeName(pluginName)
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, entry := range r.routes {
		if entry.plugin == pluginName {
			delete(r.routes, name)
		}
	}
}

func (r *Router) Dispatch(ctx context.Context, message telegram.Message) (DispatchResult, error) {
	request, ok := r.Parse(message)
	if !ok {
		return DispatchResult{}, nil
	}

	r.mu.RLock()
	entry, exists := r.routes[request.Command]
	_, configuredOwner := r.owners[message.SenderID]
	r.mu.RUnlock()
	if !exists {
		return DispatchResult{}, nil
	}

	result := DispatchResult{
		Matched: true,
		Plugin:  entry.plugin,
		Command: entry.definition.Name,
	}
	// TeleBox is a user client. An outgoing message was authored by the
	// authenticated account and is always an owner command.
	owner := message.Outgoing || configuredOwner
	if entry.definition.OwnerOnly && !owner {
		return result, ErrPermissionDenied
	}
	return result, entry.definition.Handler(ctx, request)
}

func (r *Router) IsOwner(message telegram.Message) bool {
	if message.Outgoing {
		return true
	}
	r.mu.RLock()
	_, configured := r.owners[message.SenderID]
	r.mu.RUnlock()
	return configured
}

func (r *Router) Parse(message telegram.Message) (Request, bool) {
	text := strings.TrimSpace(message.Text)
	r.mu.RLock()
	prefixes := append([]string(nil), r.prefixes...)
	r.mu.RUnlock()
	prefix := ""
	for _, candidate := range prefixes {
		if strings.HasPrefix(text, candidate) {
			prefix = candidate
			break
		}
	}
	if prefix == "" {
		return Request{}, false
	}

	body := strings.TrimSpace(strings.TrimPrefix(text, prefix))
	if body == "" {
		return Request{}, false
	}

	commandEnd := strings.IndexFunc(body, unicode.IsSpace)
	commandName := body
	rawArgs := ""
	if commandEnd >= 0 {
		commandName = body[:commandEnd]
		rawArgs = strings.TrimSpace(body[commandEnd:])
	}
	commandName = normalizeName(commandName)
	if commandName == "" {
		return Request{}, false
	}

	return Request{
		Message: message,
		Prefix:  prefix,
		Command: commandName,
		Args:    strings.Fields(rawArgs),
		RawArgs: rawArgs,
	}, true
}

func (r *Router) SetPrefixes(prefixes []string) error {
	normalized, err := normalizePrefixes(prefixes)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.prefixes = normalized
	r.mu.Unlock()
	return nil
}

func (r *Router) Prefixes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.prefixes...)
}

func (r *Router) List() []RouteInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]struct{})
	result := make([]RouteInfo, 0, len(r.routes))
	for _, entry := range r.routes {
		key := entry.plugin + "\x00" + entry.definition.Name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, RouteInfo{
			Plugin:      entry.plugin,
			Name:        entry.definition.Name,
			Aliases:     append([]string(nil), entry.definition.Aliases...),
			Description: entry.definition.Description,
			OwnerOnly:   entry.definition.OwnerOnly,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Plugin == result[j].Plugin {
			return result[i].Name < result[j].Name
		}
		return result[i].Plugin < result[j].Plugin
	})
	return result
}

func normalizeName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizePrefixes(prefixes []string) ([]string, error) {
	if len(prefixes) == 0 {
		return nil, errors.New("at least one command prefix is required")
	}
	result := append([]string(nil), prefixes...)
	seen := make(map[string]struct{}, len(result))
	for _, prefix := range result {
		if prefix == "" {
			return nil, errors.New("command prefix cannot be empty")
		}
		if strings.ContainsAny(prefix, " \t\r\n") {
			return nil, fmt.Errorf("command prefix %q contains whitespace", prefix)
		}
		if _, exists := seen[prefix]; exists {
			return nil, fmt.Errorf("duplicate command prefix %q", prefix)
		}
		seen[prefix] = struct{}{}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return len(result[i]) > len(result[j])
	})
	return result, nil
}
