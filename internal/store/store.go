// Package store keeps the pingzap data in SQLite: web users and sessions,
// devices with their MQTT topics, bots with the chats they answer and the
// state of each conversation.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/mattn/go-sqlite3"
)

var (
	ErrNotFound = errors.New("não encontrado")
	// ErrConflict is returned when a unique value (username, device ID,
	// contact or group already used by another bot) is taken.
	ErrConflict = errors.New("já existe")
)

// ValidationError is caused by invalid input; its message is meant for the
// person using the web interface.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(msg string) error { return &ValidationError{Msg: msg} }

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id            INTEGER PRIMARY KEY,
	username      TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	created_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
	id           INTEGER PRIMARY KEY,
	name         TEXT NOT NULL,
	client_id    TEXT NOT NULL UNIQUE,
	token        TEXT NOT NULL UNIQUE,
	cmd_topic    TEXT NOT NULL,
	data_topic   TEXT NOT NULL,
	status_topic TEXT NOT NULL,
	status       TEXT NOT NULL DEFAULT '',
	last_seen    INTEGER NOT NULL DEFAULT 0,
	last_data    TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS bots (
	id            INTEGER PRIMARY KEY,
	name          TEXT NOT NULL,
	kind          TEXT NOT NULL CHECK (kind IN ('flow', 'alert')),
	enabled       INTEGER NOT NULL DEFAULT 1,
	device_id     INTEGER REFERENCES devices(id) ON DELETE SET NULL,
	config        TEXT NOT NULL,
	current_state TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS bot_targets (
	bot_id INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
	kind   TEXT NOT NULL CHECK (kind IN ('contact', 'group')),
	value  TEXT NOT NULL,
	label  TEXT NOT NULL DEFAULT '',
	UNIQUE (kind, value)
);
CREATE TABLE IF NOT EXISTS conversations (
	bot_id INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
	chat   TEXT NOT NULL,
	data   TEXT NOT NULL,
	PRIMARY KEY (bot_id, chat)
);
`

// Open opens (creating if needed) the database at the SQLite DSN.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	// A single connection avoids "database is locked" between writers and
	// keeps PRAGMAs from the DSN applied to every query.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create the schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func isUnique(err error) bool {
	var se sqlite3.Error
	return errors.As(err, &se) && se.ExtendedCode == sqlite3.ErrConstraintUnique
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func randomToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
