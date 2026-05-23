package main

import (
	"database/sql"
	"errors"

	_ "github.com/mattn/go-sqlite3"
)

var ErrNotFound = errors.New("not found")

type FileEntry struct {
	Filename  string `json:"filename"`
	UpdatedAt string `json:"updated_at"`
	Size      int    `json:"size"`
}

type Store interface {
	Read(namespace, filename string) (content, updatedAt string, err error)
	Write(namespace, filename, content string) error
	Append(namespace, filename, content string) error
	Delete(namespace, filename string) error
	List(namespace string) ([]FileEntry, error)
	ListNamespaces() ([]string, error)
}

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS entries (
		namespace  TEXT NOT NULL,
		filename   TEXT NOT NULL,
		content    TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (namespace, filename)
	)`)
	if err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Read(namespace, filename string) (string, string, error) {
	var content, updatedAt string
	err := s.db.QueryRow(
		`SELECT content, updated_at FROM entries WHERE namespace=? AND filename=?`,
		namespace, filename,
	).Scan(&content, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return content, updatedAt, err
}

func (s *SQLiteStore) Write(namespace, filename, content string) error {
	_, err := s.db.Exec(`
		INSERT INTO entries (namespace, filename, content, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(namespace, filename) DO UPDATE SET
			content    = excluded.content,
			updated_at = datetime('now')
	`, namespace, filename, content)
	return err
}

func (s *SQLiteStore) Append(namespace, filename, content string) error {
	_, err := s.db.Exec(`
		INSERT INTO entries (namespace, filename, content, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(namespace, filename) DO UPDATE SET
			content    = CASE WHEN entries.content = ''
			                  THEN excluded.content
			                  ELSE entries.content || char(10) || excluded.content END,
			updated_at = datetime('now')
	`, namespace, filename, content)
	return err
}

func (s *SQLiteStore) Delete(namespace, filename string) error {
	res, err := s.db.Exec(
		`DELETE FROM entries WHERE namespace=? AND filename=?`,
		namespace, filename,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) ListNamespaces() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT namespace FROM entries ORDER BY namespace`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var ns string
		if err := rows.Scan(&ns); err != nil {
			return nil, err
		}
		out = append(out, ns)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) List(namespace string) ([]FileEntry, error) {
	rows, err := s.db.Query(
		`SELECT filename, updated_at, length(content) FROM entries WHERE namespace=? ORDER BY filename`,
		namespace,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []FileEntry{}
	for rows.Next() {
		var f FileEntry
		if err := rows.Scan(&f.Filename, &f.UpdatedAt, &f.Size); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
