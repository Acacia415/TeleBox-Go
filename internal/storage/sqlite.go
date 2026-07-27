package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("storage value not found")

type DB struct {
	sql *sql.DB
}

type PluginState struct {
	Name       string
	Enabled    bool
	ConfigJSON string
	UpdatedAt  time.Time
}

func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	// Keeping a single connection guarantees connection-local PRAGMAs such as
	// foreign_keys remain active. WAL still allows readers from migration tools.
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	store := &DB{sql: database}
	if err := store.initialize(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (d *DB) initialize(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	}
	for _, statement := range pragmas {
		if _, err := d.sql.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply SQLite pragma %q: %w", statement, err)
		}
	}

	if _, err := d.sql.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	migrations := [][]string{
		{
			`CREATE TABLE plugin_state (
				name TEXT PRIMARY KEY,
				enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
				config_json TEXT NOT NULL DEFAULT '{}',
				updated_at INTEGER NOT NULL
			)`,
			`CREATE TABLE plugin_kv (
				plugin TEXT NOT NULL,
				key TEXT NOT NULL,
				value BLOB NOT NULL,
				updated_at INTEGER NOT NULL,
				PRIMARY KEY (plugin, key)
			)`,
			`CREATE INDEX plugin_kv_plugin_idx ON plugin_kv(plugin)`,
		},
	}

	var current int
	if err := d.sql.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for index, statements := range migrations {
		version := index + 1
		if version <= current {
			continue
		}
		if err := d.applyMigration(ctx, version, statements); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) applyMigration(ctx context.Context, version int, statements []string) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema migration %d: %w", version, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply schema migration %d: %w", version, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)",
		version,
		time.Now().UTC().Unix(),
	); err != nil {
		return fmt.Errorf("record schema migration %d: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema migration %d: %w", version, err)
	}
	return nil
}

func (d *DB) Close() error {
	return d.sql.Close()
}

func (d *DB) Ping(ctx context.Context) error {
	return d.sql.PingContext(ctx)
}

// Backup writes a transactionally consistent, self-contained SQLite copy.
// VACUUM INTO also folds WAL contents into the destination database.
func (d *DB) Backup(ctx context.Context, destination string) error {
	if strings.TrimSpace(destination) == "" {
		return errors.New("backup destination is required")
	}
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	_ = os.Remove(absolute)
	if _, err := d.sql.ExecContext(ctx, "VACUUM INTO ?", absolute); err != nil {
		return fmt.Errorf("backup SQLite database: %w", err)
	}
	if err := os.Chmod(absolute, 0o600); err != nil {
		return fmt.Errorf("protect SQLite backup: %w", err)
	}
	return nil
}

func (d *DB) SetPluginState(ctx context.Context, state PluginState) error {
	if state.Name == "" {
		return errors.New("plugin name is required")
	}
	if state.ConfigJSON == "" {
		state.ConfigJSON = "{}"
	}
	updatedAt := state.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	enabled := 0
	if state.Enabled {
		enabled = 1
	}
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO plugin_state(name, enabled, config_json, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			enabled = excluded.enabled,
			config_json = excluded.config_json,
			updated_at = excluded.updated_at
	`, state.Name, enabled, state.ConfigJSON, updatedAt.Unix())
	if err != nil {
		return fmt.Errorf("set plugin state %q: %w", state.Name, err)
	}
	return nil
}

func (d *DB) PluginStates(ctx context.Context) ([]PluginState, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT name, enabled, config_json, updated_at
		FROM plugin_state
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list plugin states: %w", err)
	}
	defer rows.Close()

	var result []PluginState
	for rows.Next() {
		var (
			state     PluginState
			enabled   int
			updatedAt int64
		)
		if err := rows.Scan(&state.Name, &enabled, &state.ConfigJSON, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan plugin state: %w", err)
		}
		state.Enabled = enabled == 1
		state.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		result = append(result, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plugin states: %w", err)
	}
	return result, nil
}

func (d *DB) Put(ctx context.Context, plugin, key string, value []byte) error {
	if plugin == "" || key == "" {
		return errors.New("plugin and key are required")
	}
	if value == nil {
		value = []byte{}
	}
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO plugin_kv(plugin, key, value, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(plugin, key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, plugin, key, value, time.Now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("put %s/%s: %w", plugin, key, err)
	}
	return nil
}

func (d *DB) Get(ctx context.Context, plugin, key string) ([]byte, error) {
	var value []byte
	err := d.sql.QueryRowContext(ctx,
		"SELECT value FROM plugin_kv WHERE plugin = ? AND key = ?",
		plugin,
		key,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s/%s", ErrNotFound, plugin, key)
	}
	if err != nil {
		return nil, fmt.Errorf("get %s/%s: %w", plugin, key, err)
	}
	return append([]byte(nil), value...), nil
}

func (d *DB) Delete(ctx context.Context, plugin, key string) error {
	_, err := d.sql.ExecContext(ctx,
		"DELETE FROM plugin_kv WHERE plugin = ? AND key = ?",
		plugin,
		key,
	)
	if err != nil {
		return fmt.Errorf("delete %s/%s: %w", plugin, key, err)
	}
	return nil
}
