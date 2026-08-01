package pmcaptcha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/storage"
)

const (
	stateNamespace = "pmcaptcha"
	stateKey       = "state"
)

type Config struct {
	Welcome         string   `json:"welcome,omitempty"`
	WhitelistWords  []string `json:"whitelist_words,omitempty"`
	BlacklistWords  []string `json:"blacklist_words,omitempty"`
	MathTimeout     int      `json:"math_timeout"`
	StickerTimeout  int      `json:"sticker_timeout"`
	ImageTimeout    int      `json:"image_timeout"`
	DisablePM       bool     `json:"disable_pm"`
	Action          string   `json:"action"`
	Report          bool     `json:"report"`
	Premium         string   `json:"premium"`
	CommonGroups    *int     `json:"common_groups,omitempty"`
	HistoryCount    *int     `json:"history_count,omitempty"`
	Initiative      bool     `json:"initiative"`
	Silent          bool     `json:"silent"`
	FloodLimit      int      `json:"flood_limit"`
	FloodUsername   bool     `json:"flood_username"`
	FloodAction     string   `json:"flood_action"`
	CustomRule      string   `json:"custom_rule,omitempty"`
	DetailedLogs    bool     `json:"detailed_logs"`
	ChallengeType   string   `json:"challenge_type"`
	ImageType       string   `json:"image_type"`
	ImageMaxRetries int      `json:"image_max_retries"`
}

type Stats struct {
	Passed  int `json:"passed"`
	Banned  int `json:"banned"`
	Flooded int `json:"flooded"`
}

type Challenge struct {
	UserID       int64     `json:"user_id"`
	Type         string    `json:"type"`
	Answer       int       `json:"answer,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	MessageIDs   []int     `json:"message_ids,omitempty"`
	TryCount     int       `json:"try_count,omitempty"`
	CanReport    bool      `json:"can_report"`
}

type FloodState struct {
	Active              bool             `json:"active"`
	Users               map[string]bool  `json:"users,omitempty"`
	Recent              map[string]int64 `json:"recent,omitempty"`
	LastMessageAt       time.Time        `json:"last_message_at,omitempty"`
	PreviousAutoArchive bool             `json:"previous_auto_archive"`
	Username            string           `json:"username,omitempty"`
	TemporaryChannelID  int64            `json:"temporary_channel_id,omitempty"`
}

type State struct {
	Config     Config               `json:"config"`
	Verified   map[string]time.Time `json:"verified"`
	Challenges map[string]Challenge `json:"challenges"`
	Stats      Stats                `json:"stats"`
	Flood      FloodState           `json:"flood"`
}

func defaultState() State {
	return State{
		Config: Config{
			MathTimeout:     30,
			StickerTimeout:  30,
			ImageTimeout:    300,
			Action:          "ban",
			Report:          true,
			Premium:         "none",
			Initiative:      true,
			FloodLimit:      5,
			FloodAction:     "delete",
			ChallengeType:   "math",
			ImageType:       "func",
			ImageMaxRetries: 3,
		},
		Verified:   make(map[string]time.Time),
		Challenges: make(map[string]Challenge),
		Flood: FloodState{
			Users:  make(map[string]bool),
			Recent: make(map[string]int64),
		},
	}
}

func normalizeState(state *State) {
	defaults := defaultState()
	if state.Verified == nil {
		state.Verified = make(map[string]time.Time)
	}
	if state.Challenges == nil {
		state.Challenges = make(map[string]Challenge)
	}
	if state.Flood.Users == nil {
		state.Flood.Users = make(map[string]bool)
	}
	if state.Flood.Recent == nil {
		state.Flood.Recent = make(map[string]int64)
	}
	if state.Config.MathTimeout < 0 {
		state.Config.MathTimeout = defaults.Config.MathTimeout
	}
	if state.Config.StickerTimeout < 0 {
		state.Config.StickerTimeout = defaults.Config.StickerTimeout
	}
	if state.Config.ImageTimeout < 0 {
		state.Config.ImageTimeout = defaults.Config.ImageTimeout
	}
	if !oneOf(state.Config.Action, "ban", "delete", "none") {
		state.Config.Action = defaults.Config.Action
	}
	if !oneOf(state.Config.Premium, "allow", "ban", "only", "none") {
		state.Config.Premium = defaults.Config.Premium
	}
	if state.Config.FloodLimit <= 0 {
		state.Config.FloodLimit = defaults.Config.FloodLimit
	}
	if !oneOf(state.Config.FloodAction, "asis", "delete", "captcha", "none") {
		state.Config.FloodAction = defaults.Config.FloodAction
	}
	if !oneOf(state.Config.ChallengeType, "math", "sticker", "img") {
		state.Config.ChallengeType = defaults.Config.ChallengeType
	}
	if !oneOf(state.Config.ImageType, "func", "github", "rec") {
		state.Config.ImageType = defaults.Config.ImageType
	}
	if state.Config.ImageMaxRetries <= 0 {
		state.Config.ImageMaxRetries = defaults.Config.ImageMaxRetries
	}
	state.Config.WhitelistWords = cleanWords(state.Config.WhitelistWords)
	state.Config.BlacklistWords = cleanWords(state.Config.BlacklistWords)
}

func (p *Plugin) loadState(ctx context.Context) error {
	raw, err := p.services.Storage.Get(ctx, stateNamespace, stateKey)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 PMCaptcha 数据失败: %w", err)
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("解析 PMCaptcha 数据失败: %w", err)
	}
	normalizeState(&state)
	p.state = state
	return nil
}

func (p *Plugin) persistLocked(ctx context.Context) error {
	raw, err := json.Marshal(p.state)
	if err != nil {
		return err
	}
	return p.services.Storage.Put(ctx, stateNamespace, stateKey, raw)
}

func cloneConfig(config Config) Config {
	result := config
	result.WhitelistWords = append([]string(nil), config.WhitelistWords...)
	result.BlacklistWords = append([]string(nil), config.BlacklistWords...)
	if config.CommonGroups != nil {
		value := *config.CommonGroups
		result.CommonGroups = &value
	}
	if config.HistoryCount != nil {
		value := *config.HistoryCount
		result.HistoryCount = &value
	}
	return result
}

func cleanWords(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	return result
}

func splitWords(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return cleanWords(strings.Split(value, ","))
}

func verifiedKey(userID int64) string {
	return strconv.FormatInt(userID, 10)
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
