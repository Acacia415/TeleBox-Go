package legacyconfig

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

// ReadSQLiteConfig reads the key/value config table used by legacy TeleBox
// plugins. A missing database is not an error and returns a nil map.
func ReadSQLiteConfig(databasePath string) (map[string]string, error) {
	if databasePath == "" {
		return nil, nil
	}
	if _, err := os.Stat(databasePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect legacy config database: %w", err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open legacy config database: %w", err)
	}
	defer database.Close()
	rows, err := database.Query("SELECT key, value FROM config")
	if err != nil {
		return nil, fmt.Errorf("read legacy config table: %w", err)
	}
	defer rows.Close()
	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan legacy config row: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy config rows: %w", err)
	}
	return values, nil
}
