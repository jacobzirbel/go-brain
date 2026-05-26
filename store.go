package main

import (
	"database/sql"
	"errors"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrDestinationExists = errors.New("destination already exists")
	ErrNoPending         = errors.New("no pending changes")
)

type FileEntry struct {
	Filename   string `json:"filename"`
	UpdatedAt  string `json:"updated_at"`
	Size       int    `json:"size"`
	HasPending bool   `json:"-"`
}

type MoveOp struct {
	SrcNamespace string
	SrcFilename  string
	DstNamespace string
	DstFilename  string
}

type Comment struct {
	ID        int64  `json:"id"`
	Namespace string `json:"namespace,omitempty"`
	Filename  string `json:"filename,omitempty"`
	Content   string `json:"content"`
	Reviewed  bool   `json:"reviewed"`
	CreatedAt string `json:"created_at"`
}

type PendingFile struct {
	Namespace          string `json:"namespace"`
	Filename           string `json:"filename"`
	UpdatedAt          string `json:"updated_at"`
	SortAt             string `json:"sort_at"`
	CommentCount       int    `json:"comment_count"`
	LastCommentSnippet string `json:"last_comment_snippet"`
}

type Store interface {
	Read(namespace, filename string) (content, updatedAt string, err error)
	ReadEntry(namespace, filename string) (content string, newVal sql.NullString, updatedAt string, err error)
	Write(namespace, filename, content string) error
	Append(namespace, filename, content string) error
	Delete(namespace, filename string) error
	Move(srcNamespace, srcFilename, dstNamespace, dstFilename string) error
	MoveMany(ops []MoveOp) error
	Copy(srcNamespace, srcFilename, dstNamespace, dstFilename string) error
	List(namespace string) ([]FileEntry, error)
	ListNamespaces() ([]string, error)

	InsertComment(namespace, filename, content string) error
	ListComments(namespace, filename string, includeReviewed bool, limit, offset int) ([]Comment, error)
	Review(namespace, filename string) error
	Reject(namespace, filename string) error
	PendingFiles() ([]PendingFile, error)
	GlobalPendingCount() (int, error)
	NamespacePendingCount(namespace string) (int, error)

	CreateSession(token string) error
	HasSession(token string) (bool, error)
	DeleteSession(token string) error
}

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	// entries schema:
	//   content = last reviewed canonical value
	//   new     = pending unreviewed (NULL when no pending review)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS entries (
		namespace  TEXT NOT NULL,
		filename   TEXT NOT NULL,
		content    TEXT NOT NULL DEFAULT '',
		new        TEXT,
		updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (namespace, filename)
	)`); err != nil {
		return nil, err
	}
	if err := migrateEntries(db); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS comments (
		id         INTEGER PRIMARY KEY,
		namespace  TEXT NOT NULL,
		filename   TEXT NOT NULL,
		content    TEXT NOT NULL,
		reviewed   BOOLEAN NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS comments_file_reviewed ON comments(namespace, filename, reviewed)`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS comments_created_desc ON comments(created_at DESC)`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS ui_sessions (
		token      TEXT PRIMARY KEY,
		created_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// migrateEntries handles two legacy schemas:
//   - pre-Phase 6: only `content` exists → add `new`.
//   - interim Phase 6: had `old + new` (content was dropped) → re-add `content`,
//     copy `old` → `content` (idempotent), drop `old`.
//
// End state: `content` (last reviewed) + `new` (pending, nullable).
// Safe to call repeatedly: every step is a no-op if the schema already matches.
func migrateEntries(db *sql.DB) error {
	cols, err := tableColumns(db, "entries")
	if err != nil {
		return err
	}
	hasContent := cols["content"]
	hasOld := cols["old"]
	hasNew := cols["new"]

	if !hasContent {
		if _, err := db.Exec(`ALTER TABLE entries ADD COLUMN content TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !hasNew {
		if _, err := db.Exec(`ALTER TABLE entries ADD COLUMN new TEXT`); err != nil {
			return err
		}
	}
	if hasOld {
		// Sync from `old` (the prior canonical column) into `content`. The
		// WHERE clause makes this idempotent even if `content` was being
		// double-written by a half-finished cutover. `IS NOT` handles NULLs
		// the way `!=` doesn't.
		if _, err := db.Exec(`UPDATE entries SET content = old WHERE content IS NOT old`); err != nil {
			return err
		}
		if _, err := db.Exec(`ALTER TABLE entries DROP COLUMN old`); err != nil {
			return err
		}
	}
	return nil
}

func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (s *SQLiteStore) CreateSession(token string) error {
	_, err := s.db.Exec(`INSERT INTO ui_sessions (token) VALUES (?)`, token)
	return err
}

func (s *SQLiteStore) HasSession(token string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT 1 FROM ui_sessions WHERE token=?`, token).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM ui_sessions WHERE token=?`, token)
	return err
}

// Read returns the source-of-truth content: COALESCE(new, content).
// Unreviewed content is valid content; review state is workflow metadata.
func (s *SQLiteStore) Read(namespace, filename string) (string, string, error) {
	var content, updatedAt string
	err := s.db.QueryRow(
		`SELECT COALESCE(new, content), updated_at FROM entries WHERE namespace=? AND filename=?`,
		namespace, filename,
	).Scan(&content, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return content, updatedAt, err
}

// ReadEntry returns the last-reviewed `content` column and the (nullable)
// pending `new` column separately — used by the UI diff view.
func (s *SQLiteStore) ReadEntry(namespace, filename string) (string, sql.NullString, string, error) {
	var content, updatedAt string
	var newVal sql.NullString
	err := s.db.QueryRow(
		`SELECT content, new, updated_at FROM entries WHERE namespace=? AND filename=?`,
		namespace, filename,
	).Scan(&content, &newVal, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", sql.NullString{}, "", ErrNotFound
	}
	return content, newVal, updatedAt, err
}

// Write sets entries.new = content. Leaves `old` untouched.
// Comment rows (if any) are inserted separately via InsertComment.
func (s *SQLiteStore) Write(namespace, filename, content string) error {
	_, err := s.db.Exec(`
		INSERT INTO entries (namespace, filename, new, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(namespace, filename) DO UPDATE SET
			new        = excluded.new,
			updated_at = datetime('now')
	`, namespace, filename, content)
	return err
}

// Append reads COALESCE(new, content), appends, and writes the result to `new`.
func (s *SQLiteStore) Append(namespace, filename, content string) error {
	_, err := s.db.Exec(`
		INSERT INTO entries (namespace, filename, new, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(namespace, filename) DO UPDATE SET
			new = CASE
				WHEN COALESCE(entries.new, entries.content) = ''
					THEN excluded.new
					ELSE COALESCE(entries.new, entries.content) || char(10) || excluded.new
			END,
			updated_at = datetime('now')
	`, namespace, filename, content)
	return err
}

func moveInTx(tx *sql.Tx, op MoveOp) error {
	if op.SrcNamespace == op.DstNamespace && op.SrcFilename == op.DstFilename {
		return nil
	}
	var exists int
	err := tx.QueryRow(
		`SELECT 1 FROM entries WHERE namespace=? AND filename=?`,
		op.DstNamespace, op.DstFilename,
	).Scan(&exists)
	if err == nil {
		return ErrDestinationExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	res, err := tx.Exec(
		`UPDATE entries SET namespace=?, filename=?, updated_at=datetime('now')
		 WHERE namespace=? AND filename=?`,
		op.DstNamespace, op.DstFilename, op.SrcNamespace, op.SrcFilename,
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
	// Comments follow the file. Same tx — partial state never visible.
	if _, err := tx.Exec(
		`UPDATE comments SET namespace=?, filename=? WHERE namespace=? AND filename=?`,
		op.DstNamespace, op.DstFilename, op.SrcNamespace, op.SrcFilename,
	); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) Move(srcNS, srcName, dstNS, dstName string) error {
	return s.MoveMany([]MoveOp{{srcNS, srcName, dstNS, dstName}})
}

func (s *SQLiteStore) MoveMany(ops []MoveOp) error {
	if len(ops) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, op := range ops {
		if err := moveInTx(tx, op); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Copy duplicates the entries row. Does NOT carry comments — the new file starts
// with an empty history per the Phase 6 decision.
func (s *SQLiteStore) Copy(srcNS, srcName, dstNS, dstName string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRow(`SELECT 1 FROM entries WHERE namespace=? AND filename=?`, dstNS, dstName).Scan(&exists)
	if err == nil {
		return ErrDestinationExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	res, err := tx.Exec(`
		INSERT INTO entries (namespace, filename, content, new, updated_at)
		SELECT ?, ?, content, new, datetime('now') FROM entries WHERE namespace=? AND filename=?
	`, dstNS, dstName, srcNS, srcName)
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
	return tx.Commit()
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
	rows, err := s.db.Query(`
		SELECT filename, updated_at, length(COALESCE(new, content)) AS size, new IS NOT NULL AS pending
		FROM entries WHERE namespace=? ORDER BY filename`,
		namespace,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []FileEntry{}
	for rows.Next() {
		var f FileEntry
		var pending int
		if err := rows.Scan(&f.Filename, &f.UpdatedAt, &f.Size, &pending); err != nil {
			return nil, err
		}
		f.HasPending = pending != 0
		files = append(files, f)
	}
	return files, rows.Err()
}

// ── Comments + review/reject ────────────────────────────────────────────────

func (s *SQLiteStore) InsertComment(namespace, filename, content string) error {
	if content == "" {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT INTO comments (namespace, filename, content) VALUES (?, ?, ?)`,
		namespace, filename, content,
	)
	return err
}

func (s *SQLiteStore) ListComments(namespace, filename string, includeReviewed bool, limit, offset int) ([]Comment, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var b strings.Builder
	args := []any{namespace}
	b.WriteString(`SELECT id, namespace, filename, content, reviewed, created_at FROM comments WHERE namespace=?`)
	if filename != "" {
		b.WriteString(` AND filename=?`)
		args = append(args, filename)
	}
	if !includeReviewed {
		b.WriteString(` AND reviewed=0`)
	}
	b.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`)
	args = append(args, limit, offset)

	rows, err := s.db.Query(b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.Namespace, &c.Filename, &c.Content, &c.Reviewed, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Review: new → old, clear new, mark file's open comments reviewed (atomic).
// Errors with ErrNoPending if there are no pending changes.
func (s *SQLiteStore) Review(namespace, filename string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var newVal sql.NullString
	err = tx.QueryRow(
		`SELECT new FROM entries WHERE namespace=? AND filename=?`,
		namespace, filename,
	).Scan(&newVal)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !newVal.Valid {
		return ErrNoPending
	}
	if _, err := tx.Exec(
		`UPDATE entries SET content = new, new = NULL WHERE namespace=? AND filename=?`,
		namespace, filename,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE comments SET reviewed=1 WHERE namespace=? AND filename=? AND reviewed=0`,
		namespace, filename,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// Reject: clear new (revert to last reviewed `content`), mark open comments reviewed.
func (s *SQLiteStore) Reject(namespace, filename string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var newVal sql.NullString
	err = tx.QueryRow(
		`SELECT new FROM entries WHERE namespace=? AND filename=?`,
		namespace, filename,
	).Scan(&newVal)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !newVal.Valid {
		return ErrNoPending
	}
	if _, err := tx.Exec(
		`UPDATE entries SET new = NULL WHERE namespace=? AND filename=?`,
		namespace, filename,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE comments SET reviewed=1 WHERE namespace=? AND filename=? AND reviewed=0`,
		namespace, filename,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// PendingFiles returns every file with new IS NOT NULL, ordered by
// most-recent-comment created_at DESC (falls back to entries.updated_at).
func (s *SQLiteStore) PendingFiles() ([]PendingFile, error) {
	rows, err := s.db.Query(`
		SELECT
			e.namespace,
			e.filename,
			e.updated_at,
			(SELECT COUNT(*) FROM comments c WHERE c.namespace=e.namespace AND c.filename=e.filename) AS comment_count,
			(SELECT content FROM comments c2 WHERE c2.namespace=e.namespace AND c2.filename=e.filename ORDER BY c2.created_at DESC, c2.id DESC LIMIT 1) AS last_comment,
			(SELECT MAX(c3.created_at) FROM comments c3 WHERE c3.namespace=e.namespace AND c3.filename=e.filename) AS last_at
		FROM entries e
		WHERE e.new IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingFile{}
	for rows.Next() {
		var p PendingFile
		var lastComment, lastAt sql.NullString
		if err := rows.Scan(&p.Namespace, &p.Filename, &p.UpdatedAt, &p.CommentCount, &lastComment, &lastAt); err != nil {
			return nil, err
		}
		if lastComment.Valid {
			p.LastCommentSnippet = lastComment.String
		}
		if lastAt.Valid {
			p.SortAt = lastAt.String
		} else {
			p.SortAt = p.UpdatedAt
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Sort newest-first by SortAt. Done in Go because the SortAt expression
	// COALESCE(MAX(comment.created_at), entries.updated_at) is awkward in the
	// outer ORDER BY when it references subquery aliases.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].SortAt > out[j-1].SortAt; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

func (s *SQLiteStore) GlobalPendingCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE new IS NOT NULL`).Scan(&n)
	return n, err
}

func (s *SQLiteStore) NamespacePendingCount(namespace string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE namespace=? AND new IS NOT NULL`, namespace).Scan(&n)
	return n, err
}
