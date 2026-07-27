package command

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
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
	Usage       []string
	HelpHTML    string
	OwnerOnly   bool
	Handler     Handler
}

type RouteInfo struct {
	Plugin      string
	Name        string
	Aliases     []string
	Description string
	Usage       []string
	HelpHTML    string
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
	mu            sync.RWMutex
	prefixes      []string
	owners        map[int64]struct{}
	routes        map[string]route
	userAliases   map[string]string
	delegates     map[int64]struct{}
	delegateChats map[int64]struct{}
	suppressed    map[messageKey]time.Time
}

type messageKey struct {
	chatID    int64
	messageID int
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
		prefixes:      normalizedPrefixes,
		owners:        owners,
		routes:        make(map[string]route),
		userAliases:   make(map[string]string),
		delegates:     make(map[int64]struct{}),
		delegateChats: make(map[int64]struct{}),
		suppressed:    make(map[messageKey]time.Time),
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
		definition.Usage = normalizeUsage(definition.Usage)
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
	return r.dispatch(ctx, message, false)
}

func (r *Router) DispatchAsOwner(
	ctx context.Context,
	message telegram.Message,
) (DispatchResult, error) {
	return r.dispatch(ctx, message, true)
}

func (r *Router) dispatch(
	ctx context.Context,
	message telegram.Message,
	forceOwner bool,
) (DispatchResult, error) {
	if !forceOwner && r.consumeSuppressed(message) {
		return DispatchResult{}, nil
	}
	request, ok := r.Parse(message)
	if !ok {
		return DispatchResult{}, nil
	}

	r.mu.RLock()
	entry, exists := r.routes[request.Command]
	owner := forceOwner || r.isOwnerLocked(message)
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
	if entry.definition.OwnerOnly && !owner {
		return result, ErrPermissionDenied
	}
	return result, entry.definition.Handler(ctx, request)
}

// SuppressNext prevents the application's normal dispatch pass from running a
// message that a trusted listener has already dispatched explicitly.
func (r *Router) SuppressNext(message telegram.Message) {
	if message.ID <= 0 {
		return
	}
	now := time.Now()
	r.mu.Lock()
	for key, expires := range r.suppressed {
		if now.After(expires) {
			delete(r.suppressed, key)
		}
	}
	r.suppressed[messageKey{
		chatID: message.ChatID, messageID: message.ID,
	}] = now.Add(time.Minute)
	r.mu.Unlock()
}

func (r *Router) consumeSuppressed(message telegram.Message) bool {
	if message.ID <= 0 {
		return false
	}
	key := messageKey{chatID: message.ChatID, messageID: message.ID}
	r.mu.Lock()
	expires, exists := r.suppressed[key]
	if exists {
		delete(r.suppressed, key)
	}
	r.mu.Unlock()
	return exists && time.Now().Before(expires)
}

func (r *Router) IsOwner(message telegram.Message) bool {
	r.mu.RLock()
	configured := r.isOwnerLocked(message)
	r.mu.RUnlock()
	return configured
}

func (r *Router) isOwnerLocked(message telegram.Message) bool {
	if message.Outgoing {
		return true
	}
	if _, configured := r.owners[message.SenderID]; configured {
		return true
	}
	if _, delegated := r.delegates[message.SenderID]; !delegated {
		return false
	}
	if len(r.delegateChats) == 0 {
		return true
	}
	_, allowedChat := r.delegateChats[message.ChatID]
	return allowedChat
}

func (r *Router) SetDelegates(userIDs, chatIDs []int64) {
	users := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id > 0 {
			users[id] = struct{}{}
		}
	}
	chats := make(map[int64]struct{}, len(chatIDs))
	for _, id := range chatIDs {
		if id != 0 {
			chats[id] = struct{}{}
		}
	}
	r.mu.Lock()
	r.delegates = users
	r.delegateChats = chats
	r.mu.Unlock()
}

func (r *Router) Delegates() ([]int64, []int64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	users := make([]int64, 0, len(r.delegates))
	for id := range r.delegates {
		users = append(users, id)
	}
	chats := make([]int64, 0, len(r.delegateChats))
	for id := range r.delegateChats {
		chats = append(chats, id)
	}
	sort.Slice(users, func(i, j int) bool { return users[i] < users[j] })
	sort.Slice(chats, func(i, j int) bool { return chats[i] < chats[j] })
	return users, chats
}

func (r *Router) Parse(message telegram.Message) (Request, bool) {
	text := strings.TrimSpace(message.Text)
	r.mu.RLock()
	prefixes := append([]string(nil), r.prefixes...)
	aliases := cloneAliases(r.userAliases)
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
	body = rewriteUserAlias(body, aliases)

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

// SetUserAliases replaces the owner-managed command aliases. Alias keys are
// matched case-insensitively using the longest sequence of words. Targets may
// include fixed arguments, for example "clean" -> "bd 20".
func (r *Router) SetUserAliases(aliases map[string]string) error {
	normalized := make(map[string]string, len(aliases))
	for alias, target := range aliases {
		alias, err := normalizeAliasPhrase(alias)
		if err != nil {
			return err
		}
		target, err = normalizeAliasTarget(target)
		if err != nil {
			return fmt.Errorf("alias %q: %w", alias, err)
		}
		if strings.Fields(alias)[0] == "alias" {
			return errors.New(`command alias "alias" is reserved`)
		}
		normalized[alias] = target
	}
	if err := validateAliasGraph(normalized); err != nil {
		return err
	}
	r.mu.Lock()
	r.userAliases = normalized
	r.mu.Unlock()
	return nil
}

func (r *Router) UserAliases() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneAliases(r.userAliases)
}

func (r *Router) ResolveUserAlias(alias string) (string, bool) {
	alias = strings.ToLower(strings.Join(strings.Fields(alias), " "))
	r.mu.RLock()
	target, ok := r.userAliases[alias]
	r.mu.RUnlock()
	return target, ok
}

func (r *Router) HasRoute(name string) bool {
	name = normalizeName(name)
	r.mu.RLock()
	_, ok := r.routes[name]
	r.mu.RUnlock()
	return ok
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
			Usage:       append([]string(nil), entry.definition.Usage...),
			HelpHTML:    entry.definition.HelpHTML,
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

func normalizeUsage(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeAliasPhrase(value string) (string, error) {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	if len(parts) == 0 {
		return "", errors.New("command alias cannot be empty")
	}
	if len(parts) > 16 {
		return "", errors.New("command alias cannot contain more than 16 words")
	}
	normalized := strings.Join(parts, " ")
	if len(normalized) > 128 {
		return "", errors.New("command alias cannot exceed 128 characters")
	}
	for _, part := range parts {
		if strings.IndexFunc(part, unicode.IsControl) >= 0 {
			return "", errors.New("command alias contains a control character")
		}
	}
	return normalized, nil
}

func normalizeAliasTarget(value string) (string, error) {
	parts := strings.Fields(strings.TrimSpace(value))
	if len(parts) == 0 {
		return "", errors.New("alias target cannot be empty")
	}
	if len(parts) > 64 {
		return "", errors.New("alias target cannot contain more than 64 words")
	}
	parts[0] = normalizeName(parts[0])
	if parts[0] == "" {
		return "", errors.New("alias target command cannot be empty")
	}
	normalized := strings.Join(parts, " ")
	if len(normalized) > 1024 {
		return "", errors.New("alias target cannot exceed 1024 characters")
	}
	return normalized, nil
}

func validateAliasGraph(aliases map[string]string) error {
	for alias, target := range aliases {
		targetParts := strings.Fields(strings.ToLower(target))
		if len(targetParts) == 0 {
			continue
		}
		for candidate := range aliases {
			candidateParts := strings.Fields(candidate)
			if len(candidateParts) > len(targetParts) {
				continue
			}
			matches := true
			for index := range candidateParts {
				if candidateParts[index] != targetParts[index] {
					matches = false
					break
				}
			}
			if matches {
				return fmt.Errorf(
					"alias %q redirects through alias %q; chained aliases are not supported",
					alias,
					candidate,
				)
			}
		}
	}
	return nil
}

func rewriteUserAlias(body string, aliases map[string]string) string {
	if len(aliases) == 0 {
		return body
	}
	parts := strings.Fields(body)
	if len(parts) == 0 {
		return body
	}
	lower := make([]string, len(parts))
	for index, part := range parts {
		lower[index] = strings.ToLower(part)
	}
	bestAlias := ""
	bestWords := 0
	for alias := range aliases {
		aliasParts := strings.Fields(alias)
		if len(aliasParts) <= bestWords || len(aliasParts) > len(lower) {
			continue
		}
		matches := true
		for index := range aliasParts {
			if aliasParts[index] != lower[index] {
				matches = false
				break
			}
		}
		if matches {
			bestAlias = alias
			bestWords = len(aliasParts)
		}
	}
	if bestAlias == "" {
		return body
	}
	target := strings.Fields(aliases[bestAlias])
	return strings.Join(append(target, parts[bestWords:]...), " ")
}

func cloneAliases(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for alias, target := range source {
		result[alias] = target
	}
	return result
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
