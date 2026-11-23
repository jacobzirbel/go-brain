package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
)

// OAuth access tokens used to live only in memory, so every deploy (service
// restart) signed every client out. They are now persisted in SQLite, hashed,
// so a restart keeps sessions signed in. The SECRET_KEY bearer is never
// persisted: it comes from config on each start, so rotating it still works.

type tokenPersister interface {
	SaveToken(tok string) error
	HasToken(tok string) (bool, error)
	DeleteToken(tok string) error
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// EnsureTokenTable creates the oauth_tokens table if missing.
func (s *SQLiteStore) EnsureTokenTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS oauth_tokens (
		token_hash TEXT PRIMARY KEY,
		created_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`)
	return err
}

func (s *SQLiteStore) SaveToken(tok string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO oauth_tokens (token_hash) VALUES (?)`, hashToken(tok))
	return err
}

func (s *SQLiteStore) HasToken(tok string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM oauth_tokens WHERE token_hash = ?`, hashToken(tok)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *SQLiteStore) DeleteToken(tok string) error {
	_, err := s.db.Exec(`DELETE FROM oauth_tokens WHERE token_hash = ?`, hashToken(tok))
	return err
}
