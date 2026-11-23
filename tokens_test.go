package main

import (
	"path/filepath"
	"testing"
)

// An access token minted before a restart must still authorize after it; the
// config secret must not be written to the database.
func TestTokens_SurviveRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")

	s1, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	srv1 := newServer(config{secretKey: "the-secret"}, s1)
	srv1.tokens.add("access-123")
	srv1.tokens.add("code:abc")
	srv1.tokens.delete("code:abc")
	s1.db.Close()

	s2, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	srv2 := newServer(config{secretKey: "rotated"}, s2)
	if !srv2.tokens.has("access-123") {
		t.Fatal("access token lost across restart")
	}
	if srv2.tokens.has("code:abc") {
		t.Fatal("deleted code should stay deleted")
	}
	if srv2.tokens.has("the-secret") {
		t.Fatal("old config secret must not be persisted")
	}
	if !srv2.tokens.has("rotated") {
		t.Fatal("current config secret should authorize")
	}
	var raw int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM oauth_tokens WHERE token_hash = 'access-123'`).Scan(&raw); err != nil || raw != 0 {
		t.Fatalf("tokens must be stored hashed (raw rows=%d err=%v)", raw, err)
	}
}
