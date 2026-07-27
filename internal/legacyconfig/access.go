package legacyconfig

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
)

type AccessRecord struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type MessageRecord struct {
	ID       int64  `json:"id"`
	Message  string `json:"message"`
	Redirect string `json:"redirect,omitempty"`
}

type AccessDatabase struct {
	Users    []AccessRecord
	Chats    []AccessRecord
	Messages []MessageRecord
}

// ReadAccessDatabase reads the users/chats tables used by the original sudo
// and sure plugins. When includeMessages is true it also reads sure.msgs.
func ReadAccessDatabase(
	databasePath string,
	includeMessages bool,
) (AccessDatabase, error) {
	if databasePath == "" {
		return AccessDatabase{}, nil
	}
	if _, err := os.Stat(databasePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AccessDatabase{}, nil
		}
		return AccessDatabase{}, fmt.Errorf("inspect legacy access database: %w", err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return AccessDatabase{}, fmt.Errorf("open legacy access database: %w", err)
	}
	defer database.Close()

	users, err := readAccessRows(database, "users", "uid", "username")
	if err != nil {
		return AccessDatabase{}, err
	}
	chats, err := readAccessRows(database, "chats", "id", "name")
	if err != nil {
		return AccessDatabase{}, err
	}
	result := AccessDatabase{Users: users, Chats: chats}
	if !includeMessages {
		return result, nil
	}
	rows, err := database.Query(
		"SELECT id, msg, COALESCE(redirect, '') FROM msgs ORDER BY id",
	)
	if err != nil {
		return AccessDatabase{}, fmt.Errorf("read legacy msgs table: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item MessageRecord
		if err := rows.Scan(&item.ID, &item.Message, &item.Redirect); err != nil {
			return AccessDatabase{}, fmt.Errorf("scan legacy message row: %w", err)
		}
		result.Messages = append(result.Messages, item)
	}
	if err := rows.Err(); err != nil {
		return AccessDatabase{}, fmt.Errorf("iterate legacy message rows: %w", err)
	}
	return result, nil
}

func readAccessRows(
	database *sql.DB,
	table, idColumn, nameColumn string,
) ([]AccessRecord, error) {
	query := fmt.Sprintf(
		"SELECT %s, %s FROM %s ORDER BY %s",
		idColumn,
		nameColumn,
		table,
		idColumn,
	)
	rows, err := database.Query(query)
	if err != nil {
		return nil, fmt.Errorf("read legacy %s table: %w", table, err)
	}
	defer rows.Close()
	var result []AccessRecord
	for rows.Next() {
		var item AccessRecord
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, fmt.Errorf("scan legacy %s row: %w", table, err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy %s rows: %w", table, err)
	}
	return result, nil
}
