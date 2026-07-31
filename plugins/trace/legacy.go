package trace

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Acacia415/TeleBox-Go/internal/legacyconfig"
	"github.com/Acacia415/TeleBox-Go/internal/telegram"
	_ "modernc.org/sqlite"
)

func (p *Plugin) loadLegacyState() (traceState, bool, error) {
	for _, directory := range legacyconfig.CandidatePaths(
		p.services.AssetsDir,
		p.services.LegacyAssetsDir,
		"trace",
	) {
		state, found, err := loadLegacyTraceSQLite(
			filepath.Join(directory, "trace.db"),
		)
		if err != nil {
			return traceState{}, false, err
		}
		if found {
			return state, true, nil
		}
		for _, name := range []string{"db.json", "trace_db.json"} {
			state, found, err = loadLegacyTraceJSON(filepath.Join(directory, name))
			if err != nil {
				return traceState{}, false, err
			}
			if found {
				return state, true, nil
			}
		}
	}
	return traceState{}, false, nil
}

func loadLegacyTraceSQLite(path string) (traceState, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return traceState{}, false, nil
		}
		return traceState{}, false, err
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return traceState{}, false, fmt.Errorf("open legacy trace database: %w", err)
	}
	defer database.Close()
	state := defaultState()
	if err := scanLegacyReactionTable(
		database,
		"SELECT CAST(user_id AS TEXT), reactions FROM traced_users",
		state.Users,
	); err != nil {
		return traceState{}, false, err
	}
	if err := scanLegacyReactionTable(
		database,
		"SELECT keyword, reactions FROM traced_keywords",
		state.Keywords,
	); err != nil {
		return traceState{}, false, err
	}
	rows, err := database.Query("SELECT key, value FROM config")
	if err != nil {
		return traceState{}, false, fmt.Errorf("read legacy trace config: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return traceState{}, false, err
		}
		enabled, parseErr := strconv.ParseBool(value)
		if parseErr != nil {
			continue
		}
		switch strings.ToLower(key) {
		case "keep_log":
			state.Config.KeepLog = enabled
		case "big":
			state.Config.Big = enabled
		}
	}
	if err := rows.Err(); err != nil {
		return traceState{}, false, err
	}
	return state, true, nil
}

func scanLegacyReactionTable(
	database *sql.DB,
	query string,
	target map[string][]telegram.Reaction,
) error {
	rows, err := database.Query(query)
	if err != nil {
		return fmt.Errorf("read legacy trace reactions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return err
		}
		reactions := decodeLegacyReactions([]byte(raw))
		if key != "" && len(reactions) > 0 {
			target[key] = reactions
		}
	}
	return rows.Err()
}

func loadLegacyTraceJSON(path string) (traceState, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return traceState{}, false, nil
		}
		return traceState{}, false, err
	}
	var document struct {
		Users    map[string]json.RawMessage `json:"users"`
		Keywords map[string]json.RawMessage `json:"keywords"`
		Config   struct {
			KeepLog *bool `json:"keepLog"`
			Big     *bool `json:"big"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return traceState{}, false, fmt.Errorf("decode legacy trace JSON: %w", err)
	}
	state := defaultState()
	for key, value := range document.Users {
		if reactions := decodeLegacyReactions(value); len(reactions) > 0 {
			state.Users[key] = reactions
		}
	}
	for key, value := range document.Keywords {
		if reactions := decodeLegacyReactions(value); len(reactions) > 0 {
			state.Keywords[key] = reactions
		}
	}
	if document.Config.KeepLog != nil {
		state.Config.KeepLog = *document.Config.KeepLog
	}
	if document.Config.Big != nil {
		state.Config.Big = *document.Config.Big
	}
	return state, true, nil
}

func decodeLegacyReactions(raw []byte) []telegram.Reaction {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	if object, ok := value.(map[string]any); ok {
		combined := make([]any, 0)
		for _, key := range []string{"reactions", "custom_emojis"} {
			if values, ok := object[key].([]any); ok {
				combined = append(combined, values...)
			}
		}
		value = combined
	}
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]telegram.Reaction, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		reaction, ok := legacyReaction(value)
		if !ok {
			continue
		}
		key := reaction.Emoji + "/" + strconv.FormatInt(reaction.DocumentID, 10)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reaction)
	}
	return result
}

func legacyReaction(value any) (telegram.Reaction, bool) {
	switch typed := value.(type) {
	case string:
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return telegram.Reaction{}, false
		}
		if documentID, err := strconv.ParseInt(typed, 10, 64); err == nil &&
			documentID > 0 {
			return telegram.Reaction{DocumentID: documentID}, true
		}
		return telegram.Reaction{Emoji: typed}, true
	case float64:
		if typed > 0 {
			return telegram.Reaction{DocumentID: int64(typed)}, true
		}
	case map[string]any:
		for _, key := range []string{"document_id", "documentId", "DocumentID", "value"} {
			if reaction, ok := legacyReaction(typed[key]); ok &&
				reaction.DocumentID > 0 {
				return reaction, true
			}
		}
		for _, key := range []string{"emoji", "Emoji"} {
			if reaction, ok := legacyReaction(typed[key]); ok &&
				reaction.Emoji != "" {
				return reaction, true
			}
		}
	}
	return telegram.Reaction{}, false
}
