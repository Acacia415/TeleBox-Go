package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/storage"
	_ "modernc.org/sqlite"
)

func (p *Plugin) migrateLegacy(ctx context.Context) error {
	if p.read(ctx, "legacy_imported", "") == "1" {
		return nil
	}
	var databasePath string
	for _, candidate := range []string{
		filepath.Join(p.services.AssetsDir, "ai_config.db"),
		filepath.Join(p.services.AssetsDir, "ai", "ai_config.db"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			databasePath = candidate
			break
		}
	}
	if databasePath == "" {
		return nil
	}
	database, err := sql.Open(
		"sqlite",
		"file:"+filepath.ToSlash(databasePath)+"?mode=ro",
	)
	if err != nil {
		return err
	}
	defer database.Close()
	rows, err := database.QueryContext(ctx, `SELECT key, value FROM config`)
	if err != nil {
		return err
	}
	defer rows.Close()
	legacy := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		legacy[key] = value
	}
	if err := rows.Err(); err != nil {
		return err
	}
	selected, ok := parseProvider(firstNonEmpty(
		legacy["ai_current_provider"],
		legacy["ai_active_provider"],
	))
	if !ok {
		selected = providerGemini
	}
	migrated := 0
	putIfMissing := func(key, value string) error {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		if _, err := p.services.Storage.Get(ctx, "ai", key); err == nil {
			return nil
		} else if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if err := p.write(ctx, key, value); err != nil {
			return err
		}
		migrated++
		return nil
	}
	if err := putIfMissing("provider", string(selected)); err != nil {
		return err
	}
	var keys map[string]string
	if json.Unmarshal([]byte(legacy["ai_keys"]), &keys) == nil {
		for name, value := range keys {
			candidate, valid := parseProvider(name)
			if valid {
				if err := putIfMissing("key."+string(candidate), value); err != nil {
					return err
				}
			}
		}
	}
	if err := putIfMissing(
		"key."+string(selected),
		legacy["ai_api_key"],
	); err != nil {
		return err
	}
	var baseURLs map[string]string
	if json.Unmarshal([]byte(legacy["ai_base_urls"]), &baseURLs) == nil {
		for name, value := range baseURLs {
			candidate, valid := parseProvider(name)
			if valid {
				if err := putIfMissing(
					"base_url."+string(candidate),
					value,
				); err != nil {
					return err
				}
			}
		}
	}
	if err := putIfMissing(
		"base_url."+string(selected),
		legacy["ai_base_url"],
	); err != nil {
		return err
	}
	var models map[string]string
	_ = json.Unmarshal([]byte(legacy["ai_models"]), &models)
	oldModelKeys := map[feature]string{
		featureChat:   "ai_chat_model",
		featureSearch: "ai_search_model",
		featureImage:  "ai_image_model",
		featureTTS:    "ai_tts_model",
	}
	for requested, oldKey := range oldModelKeys {
		value := firstNonEmpty(models[string(requested)], legacy[oldKey])
		if err := putIfMissing(
			"model."+string(selected)+"."+string(requested),
			value,
		); err != nil {
			return err
		}
	}
	simpleKeys := map[string]string{
		"ai_thirdparty_compat":         "thirdparty.compat",
		"ai_tts_voice":                 "tts_voice",
		"ai_max_tokens":                "max_tokens",
		"ai_context_enabled":           "context",
		"ai_prompts":                   "prompts",
		"ai_chat_active_prompt":        "prompt.active.chat",
		"ai_search_active_prompt":      "prompt.active.search",
		"ai_tts_active_prompt":         "prompt.active.tts",
		"ai_telegraph_enabled":         "telegraph",
		"ai_telegraph_limit":           "telegraph.limit",
		"ai_telegraph_token":           "telegraph.token",
		"ai_telegraph_posts":           "telegraph.posts",
		"ai_collapsible_quote_enabled": "collapse",
	}
	for oldKey, newKey := range simpleKeys {
		if err := putIfMissing(newKey, legacy[oldKey]); err != nil {
			return err
		}
	}
	if history := convertLegacyHistory(legacy["ai_chat_history"]); len(history) > 0 {
		body, err := json.Marshal(history)
		if err != nil {
			return err
		}
		if err := putIfMissing("legacy.history", string(body)); err != nil {
			return err
		}
	}
	if err := p.write(ctx, "legacy_imported", "1"); err != nil {
		return err
	}
	if migrated > 0 {
		p.services.Logger.Info(
			"migrated legacy AI config",
			"values", migrated,
			"path", databasePath,
		)
	}
	return nil
}

func convertLegacyHistory(encoded string) []chatMessage {
	var source []struct {
		Role  string `json:"role"`
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	}
	if json.Unmarshal([]byte(encoded), &source) != nil {
		return nil
	}
	result := make([]chatMessage, 0, len(source))
	for _, item := range source {
		var text strings.Builder
		for _, part := range item.Parts {
			text.WriteString(part.Text)
		}
		role := item.Role
		if role == "model" {
			role = "assistant"
		}
		if (role == "user" || role == "assistant") &&
			strings.TrimSpace(text.String()) != "" {
			result = append(result, chatMessage{
				Role: role,
				Text: strings.TrimSpace(text.String()),
			})
		}
	}
	if len(result) > 40 {
		result = result[len(result)-40:]
	}
	return result
}
