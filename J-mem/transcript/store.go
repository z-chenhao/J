// Package transcript persists complete J-agent transcript snapshots in SQLite.
package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/z-chenhao/J/J-agent/agent"
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

// ErrNotFound reports that a transcript session ID has no stored snapshot.
var ErrNotFound = errors.New("transcript not found")

// Store owns one SQLite transcript database.
type Store struct {
	db *sql.DB
}

// Open opens or creates a local SQLite transcript database and applies the
// supported schema migration.
func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("transcript database path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve transcript database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create transcript database directory: %w", err)
	}
	handle, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create transcript database: %w", err)
	}
	if err := handle.Close(); err != nil {
		return nil, fmt.Errorf("close transcript database file: %w", err)
	}

	db, err := sql.Open("sqlite", absolute)
	if err != nil {
		return nil, fmt.Errorf("open transcript database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) initialize(ctx context.Context) error {
	if _, err := store.db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("configure transcript database busy timeout: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		return fmt.Errorf("configure transcript database journal: %w", err)
	}
	var version int
	if err := store.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read transcript schema version: %w", err)
	}
	switch version {
	case 0:
		transaction, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin transcript migration: %w", err)
		}
		defer transaction.Rollback()
		if _, err := transaction.ExecContext(ctx, `
			CREATE TABLE transcripts (
				session_id TEXT PRIMARY KEY,
				messages BLOB NOT NULL,
				updated_at TEXT NOT NULL
			)
		`); err != nil {
			return fmt.Errorf("create transcript table: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
			return fmt.Errorf("write transcript schema version: %w", err)
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("commit transcript migration: %w", err)
		}
	case schemaVersion:
	default:
		return fmt.Errorf(
			"transcript schema version %d is newer than supported version %d",
			version,
			schemaVersion,
		)
	}
	return nil
}

// Save atomically replaces one session's complete transcript snapshot.
func (store *Store) Save(ctx context.Context, sessionID string, messages []agent.Message) error {
	if store == nil || store.db == nil {
		return errors.New("transcript store is required")
	}
	if ctx == nil {
		return errors.New("transcript context is required")
	}
	sessionID, err := validateSessionID(sessionID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("encode transcript %q: %w", sessionID, err)
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO transcripts (session_id, messages, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			messages = excluded.messages,
			updated_at = excluded.updated_at
	`, sessionID, data, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save transcript %q: %w", sessionID, err)
	}
	return nil
}

// Load returns one defensive transcript snapshot for restoration with
// agent.WithHistory.
func (store *Store) Load(ctx context.Context, sessionID string) ([]agent.Message, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("transcript store is required")
	}
	if ctx == nil {
		return nil, errors.New("transcript context is required")
	}
	sessionID, err := validateSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	var data []byte
	err = store.db.QueryRowContext(
		ctx,
		"SELECT messages FROM transcripts WHERE session_id = ?",
		sessionID,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("load transcript %q: %w", sessionID, err)
	}
	var messages []agent.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("decode transcript %q: %w", sessionID, err)
	}
	return messages, nil
}

// Delete removes one stored transcript.
func (store *Store) Delete(ctx context.Context, sessionID string) error {
	if store == nil || store.db == nil {
		return errors.New("transcript store is required")
	}
	if ctx == nil {
		return errors.New("transcript context is required")
	}
	sessionID, err := validateSessionID(sessionID)
	if err != nil {
		return err
	}
	result, err := store.db.ExecContext(
		ctx,
		"DELETE FROM transcripts WHERE session_id = ?",
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("delete transcript %q: %w", sessionID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect deleted transcript %q: %w", sessionID, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	return nil
}

// Close closes the underlying SQLite database.
func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

func validateSessionID(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("transcript session ID is required")
	}
	if len(sessionID) > 256 {
		return "", errors.New("transcript session ID exceeds 256 bytes")
	}
	return sessionID, nil
}
