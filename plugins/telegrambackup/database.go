package telegrambackup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

type backupRecord struct {
	ID           int64  `json:"id"`
	ChatID       string `json:"chat_id"`
	ChatTitle    string `json:"chat_title"`
	ChatType     string `json:"chat_type"`
	MessageCount int    `json:"message_count"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type messageRecord struct {
	MessageID  int    `json:"message_id"`
	SenderID   string `json:"sender_id,omitempty"`
	SenderName string `json:"sender_name,omitempty"`
	Date       string `json:"date,omitempty"`
	Text       string `json:"text,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	MediaID    string `json:"media_id,omitempty"`
	ReplyToID  int    `json:"reply_to_id,omitempty"`
	Forward    string `json:"forward_from,omitempty"`
	RawData    string `json:"raw_data,omitempty"`
}

type chatLink struct {
	ChatID      string `json:"chat_id"`
	ChatTitle   string `json:"chat_title"`
	ChatType    string `json:"chat_type"`
	Username    string `json:"username,omitempty"`
	InviteLink  string `json:"invite_link,omitempty"`
	MemberCount int    `json:"member_count,omitempty"`
	Description string `json:"description,omitempty"`
	Verified    bool   `json:"is_verified"`
	Scam        bool   `json:"is_scam"`
	Fake        bool   `json:"is_fake"`
}

type exportDocument struct {
	Backup    backupRecord    `json:"backup"`
	Messages  []messageRecord `json:"messages"`
	Exported  string          `json:"exportDate"`
	Version   string          `json:"version"`
	ChatLinks []chatLink      `json:"chat_links,omitempty"`
}

type backupDB struct {
	db *sql.DB
}

func openBackupDB(path string) (*backupDB, error) {
	database, err := sql.Open(
		"sqlite",
		"file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
	)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	schema := `
CREATE TABLE IF NOT EXISTS backups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id TEXT NOT NULL,
    chat_title TEXT,
    chat_type TEXT,
    message_count INTEGER DEFAULT 0,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    backup_id INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    sender_id TEXT,
    sender_name TEXT,
    date TEXT,
    text TEXT,
    media_type TEXT,
    media_id TEXT,
    reply_to_id INTEGER,
    forward_from TEXT,
    raw_data TEXT,
    FOREIGN KEY (backup_id) REFERENCES backups(id) ON DELETE CASCADE,
    UNIQUE(backup_id, message_id)
);
CREATE TABLE IF NOT EXISTS chat_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id TEXT NOT NULL UNIQUE,
    chat_title TEXT,
    chat_type TEXT,
    username TEXT,
    invite_link TEXT,
    member_count INTEGER,
    description TEXT,
    is_verified INTEGER DEFAULT 0,
    is_scam INTEGER DEFAULT 0,
    is_fake INTEGER DEFAULT 0,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_messages_backup ON messages(backup_id);
CREATE INDEX IF NOT EXISTS idx_messages_date ON messages(date);
CREATE INDEX IF NOT EXISTS idx_chat_links_type ON chat_links(chat_type);
`
	if _, err := database.Exec(schema); err != nil {
		database.Close()
		return nil, fmt.Errorf("initialize telegram backup database: %w", err)
	}
	return &backupDB{db: database}, nil
}

func (d *backupDB) close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

func (d *backupDB) create(
	ctx context.Context,
	chatID string,
	title string,
	chatType string,
) (int64, error) {
	result, err := d.db.ExecContext(
		ctx,
		`INSERT INTO backups (chat_id, chat_title, chat_type) VALUES (?, ?, ?)`,
		chatID,
		title,
		chatType,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (d *backupDB) addMessages(
	ctx context.Context,
	backupID int64,
	messages []messageRecord,
) error {
	transaction, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	statement, err := transaction.PrepareContext(ctx, `
INSERT OR REPLACE INTO messages (
    backup_id, message_id, sender_id, sender_name, date, text,
    media_type, media_id, reply_to_id, forward_from, raw_data
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, message := range messages {
		if _, err := statement.ExecContext(
			ctx,
			backupID,
			message.MessageID,
			message.SenderID,
			message.SenderName,
			message.Date,
			message.Text,
			message.MediaType,
			message.MediaID,
			message.ReplyToID,
			message.Forward,
			message.RawData,
		); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func (d *backupDB) updateCount(ctx context.Context, backupID int64) error {
	_, err := d.db.ExecContext(ctx, `
UPDATE backups
SET message_count = (
    SELECT COUNT(*) FROM messages WHERE backup_id = ?
), updated_at = CURRENT_TIMESTAMP
WHERE id = ?`, backupID, backupID)
	return err
}

func (d *backupDB) list(ctx context.Context) ([]backupRecord, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT id, CAST(chat_id AS TEXT), COALESCE(chat_title, ''),
       COALESCE(chat_type, ''), message_count,
       CAST(created_at AS TEXT), CAST(updated_at AS TEXT)
FROM backups ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []backupRecord
	for rows.Next() {
		var item backupRecord
		if err := rows.Scan(
			&item.ID,
			&item.ChatID,
			&item.ChatTitle,
			&item.ChatType,
			&item.MessageCount,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *backupDB) get(ctx context.Context, id int64) (backupRecord, error) {
	var result backupRecord
	err := d.db.QueryRowContext(ctx, `
SELECT id, CAST(chat_id AS TEXT), COALESCE(chat_title, ''),
       COALESCE(chat_type, ''), message_count,
       CAST(created_at AS TEXT), CAST(updated_at AS TEXT)
FROM backups WHERE id = ?`, id).Scan(
		&result.ID,
		&result.ChatID,
		&result.ChatTitle,
		&result.ChatType,
		&result.MessageCount,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	return result, err
}

func (d *backupDB) messages(
	ctx context.Context,
	backupID int64,
) ([]messageRecord, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT message_id, COALESCE(CAST(sender_id AS TEXT), ''),
       COALESCE(sender_name, ''), COALESCE(CAST(date AS TEXT), ''),
       COALESCE(text, ''), COALESCE(media_type, ''),
       COALESCE(media_id, ''), COALESCE(reply_to_id, 0),
       COALESCE(forward_from, ''), COALESCE(raw_data, '')
FROM messages WHERE backup_id = ? ORDER BY message_id ASC`, backupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []messageRecord
	for rows.Next() {
		var item messageRecord
		if err := rows.Scan(
			&item.MessageID,
			&item.SenderID,
			&item.SenderName,
			&item.Date,
			&item.Text,
			&item.MediaType,
			&item.MediaID,
			&item.ReplyToID,
			&item.Forward,
			&item.RawData,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *backupDB) document(
	ctx context.Context,
	id int64,
) (exportDocument, error) {
	backup, err := d.get(ctx, id)
	if err != nil {
		return exportDocument{}, err
	}
	messages, err := d.messages(ctx, id)
	if err != nil {
		return exportDocument{}, err
	}
	return exportDocument{
		Backup:   backup,
		Messages: messages,
		Exported: time.Now().UTC().Format(time.RFC3339),
		Version:  "2.0",
	}, nil
}

func (d *backupDB) importDocument(
	ctx context.Context,
	document exportDocument,
) (int64, error) {
	if document.Version == "" || len(document.Messages) > messageLimit {
		return 0, errors.New("备份版本或消息数量无效")
	}
	if document.Backup.ChatID == "" ||
		len([]rune(document.Backup.ChatTitle)) > 500 ||
		len(document.Messages) != document.Backup.MessageCount &&
			document.Backup.MessageCount != 0 {
		return 0, errors.New("备份元数据无效")
	}
	backupID, err := d.create(
		ctx,
		document.Backup.ChatID,
		document.Backup.ChatTitle,
		document.Backup.ChatType,
	)
	if err != nil {
		return 0, err
	}
	if err := d.addMessages(ctx, backupID, document.Messages); err != nil {
		_, _ = d.db.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, backupID)
		return 0, err
	}
	if err := d.updateCount(ctx, backupID); err != nil {
		return 0, err
	}
	for _, link := range document.ChatLinks {
		if err := d.saveLink(ctx, link); err != nil {
			return 0, err
		}
	}
	return backupID, nil
}

func (d *backupDB) delete(ctx context.Context, id int64) (bool, error) {
	result, err := d.db.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

func (d *backupDB) clear(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `
DELETE FROM messages;
DELETE FROM backups;
DELETE FROM chat_links;`)
	return err
}

func (d *backupDB) saveLink(ctx context.Context, link chatLink) error {
	_, err := d.db.ExecContext(ctx, `
INSERT INTO chat_links (
    chat_id, chat_title, chat_type, username, invite_link,
    member_count, description, is_verified, is_scam, is_fake, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(chat_id) DO UPDATE SET
    chat_title = excluded.chat_title,
    chat_type = excluded.chat_type,
    username = excluded.username,
    invite_link = excluded.invite_link,
    member_count = excluded.member_count,
    description = excluded.description,
    is_verified = excluded.is_verified,
    is_scam = excluded.is_scam,
    is_fake = excluded.is_fake,
    updated_at = CURRENT_TIMESTAMP`,
		link.ChatID,
		link.ChatTitle,
		link.ChatType,
		link.Username,
		link.InviteLink,
		link.MemberCount,
		link.Description,
		link.Verified,
		link.Scam,
		link.Fake,
	)
	return err
}

func (d *backupDB) links(
	ctx context.Context,
	linkType string,
) ([]chatLink, error) {
	query := `
SELECT CAST(chat_id AS TEXT), COALESCE(chat_title, ''),
       COALESCE(chat_type, ''), COALESCE(username, ''),
       COALESCE(invite_link, ''), COALESCE(member_count, 0),
       COALESCE(description, ''), COALESCE(is_verified, 0),
       COALESCE(is_scam, 0), COALESCE(is_fake, 0)
FROM chat_links`
	var args []any
	if linkType != "" {
		query += ` WHERE chat_type = ?`
		args = append(args, linkType)
	}
	query += ` ORDER BY chat_title COLLATE NOCASE`
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []chatLink
	for rows.Next() {
		var item chatLink
		if err := rows.Scan(
			&item.ChatID,
			&item.ChatTitle,
			&item.ChatType,
			&item.Username,
			&item.InviteLink,
			&item.MemberCount,
			&item.Description,
			&item.Verified,
			&item.Scam,
			&item.Fake,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func rawMessageJSON(message messageRecord) string {
	body, err := json.Marshal(message)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func validChatID(value string) bool {
	_, err := strconv.ParseInt(value, 10, 64)
	return err == nil
}
