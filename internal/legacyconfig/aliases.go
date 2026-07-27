package legacyconfig

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// ReadAliases reads the aliases table used by the original TeleBox AliasDB.
// The returned map uses the original column as the user-facing alias and the
// final column as the rewritten command. A missing database is not an error.
func ReadAliases(databasePath string) (map[string]string, error) {
	if databasePath == "" {
		return nil, nil
	}
	if _, err := os.Stat(databasePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect legacy alias database: %w", err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open legacy alias database: %w", err)
	}
	defer database.Close()

	rows, err := database.Query(
		"SELECT original, final FROM aliases ORDER BY original",
	)
	if err != nil {
		return nil, fmt.Errorf("read legacy aliases table: %w", err)
	}
	defer rows.Close()

	aliases := make(map[string]string)
	for rows.Next() {
		var alias, target string
		if err := rows.Scan(&alias, &target); err != nil {
			return nil, fmt.Errorf("scan legacy alias row: %w", err)
		}
		aliases[alias] = target
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy aliases: %w", err)
	}
	return aliases, nil
}
