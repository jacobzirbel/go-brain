package main

import (
	"database/sql"
	"errors"
	"log"
	"path"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrExists            = errors.New("file already exists")
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

type SearchOptions struct {
	Namespace      string
	Query          string
	Path           string // optional doublestar glob over filename
	Limit          int    // hard-capped at 100 by Search
	Order          string // "bm25" (default) or "recency"
	IncludeArchive bool
	IncludeClosed  bool // include hits from closed namespaces (default: excluded)
}

type SearchHit struct {
	Namespace string  `json:"namespace,omitempty"`
	Filename  string  `json:"filename"`
	UpdatedAt string  `json:"updated_at"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
}

type PendingFile struct {
	Namespace          string `json:"namespace"`
	Filename           string `json:"filename"`
	UpdatedAt          string `json:"updated_at"`
	SortAt             string `json:"sort_at"`
	CommentCount       int    `json:"comment_count"`
	LastCommentSnippet string `json:"last_comment_snippet"`
	MovedFromNamespace string `json:"moved_from_namespace,omitempty"`
	MovedFromFilename  string `json:"moved_from_filename,omitempty"`
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
	//   moved_from_namespace/moved_from_filename = where the file was before an
	//   unreviewed move. The move has already taken effect (the file lives at
	//   namespace/filename); these record the revert target so Reject can move
	//   it back. NULL when the current location is the last reviewed one.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS entries (
		namespace            TEXT NOT NULL,
		filename             TEXT NOT NULL,
		content              TEXT NOT NULL DEFAULT '',
		new                  TEXT,
		moved_from_namespace TEXT,
		moved_from_filename  TEXT,
		updated_at           DATETIME NOT NULL DEFAULT (datetime('now')),
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
	// Namespace registry. Before this, namespaces were implicit — the DISTINCT
	// namespaces present in `entries`. Materializing them as rows gives `closed`
	// (namespace-level, distinct from the file-level archived/ prefix) and tags
	// somewhere to live, and lets an emptied namespace persist. Both feature
	// surfaces are created up front so neither needs a later migration:
	//   - closed: excludes a namespace from listing/boot/tools until reopened (UI).
	//   - namespace_tags: many-to-many labels, UI-only, inert until the tags phase.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS namespaces (
		name       TEXT PRIMARY KEY,
		closed     BOOLEAN NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS namespace_tags (
		namespace TEXT NOT NULL,
		tag       TEXT NOT NULL,
		PRIMARY KEY (namespace, tag)
	)`); err != nil {
		return nil, err
	}
	if err := syncNamespaces(db); err != nil {
		return nil, err
	}
	// Phase 8: FTS5 search index over entries.
	// namespace UNINDEXED — exists only for WHERE filtering, so a search for
	// the namespace name doesn't pollute results. basename = path.Base(filename).
	// body = COALESCE(new, content) — Phase 6's displayed value.
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
		namespace UNINDEXED,
		basename,
		body,
		tokenize = 'porter unicode61'
	)`); err != nil {
		return nil, err
	}
	if err := backfillFTSIfEmpty(db); err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// backfillFTSIfEmpty seeds entries_fts from entries on first boot.
// Idempotent: a non-empty entries_fts short-circuits — sync helpers keep it
// current after that.
func backfillFTSIfEmpty(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM entries_fts`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	rows, err := db.Query(`SELECT rowid, namespace, filename, COALESCE(new, content) FROM entries`)
	if err != nil {
		return err
	}
	defer rows.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO entries_fts(rowid, namespace, basename, body) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	count := 0
	for rows.Next() {
		var rowid int64
		var ns, fn, body string
		if err := rows.Scan(&rowid, &ns, &fn, &body); err != nil {
			return err
		}
		if _, err := stmt.Exec(rowid, ns, path.Base(fn), body); err != nil {
			return err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("fts backfill: %d rows", count)
	return nil
}

// upsertFTS replaces (or inserts) the FTS row for a given entries.rowid.
// Delete+insert is simpler than UPDATE here and works the same way regardless
// of whether the rowid existed previously.
func upsertFTS(tx execer, rowid int64, namespace, filename, body string) error {
	if _, err := tx.Exec(`DELETE FROM entries_fts WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	_, err := tx.Exec(
		`INSERT INTO entries_fts(rowid, namespace, basename, body) VALUES (?, ?, ?, ?)`,
		rowid, namespace, path.Base(filename), body,
	)
	return err
}

func deleteFTS(tx execer, rowid int64) error {
	_, err := tx.Exec(`DELETE FROM entries_fts WHERE rowid = ?`, rowid)
	return err
}

// syncNamespaces heals the registry from the source of truth on every boot:
// any namespace present in `entries` but missing a registry row gets one.
// Idempotent (INSERT OR IGNORE) and self-healing — it recovers a hand-edited DB
// and covers namespaces that predate the registry. It never deletes rows, so an
// emptied namespace keeps its registry row (and its closed/tags state).
func syncNamespaces(db *sql.DB) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO namespaces(name)
		SELECT DISTINCT namespace FROM entries`)
	return err
}

// upsertNamespace registers a namespace at the moment a write path can mint one.
// Idempotent — a no-op once the row exists, so it's safe to call on every write.
func upsertNamespace(tx execer, name string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO namespaces(name) VALUES (?)`, name)
	return err
}

// execer abstracts over *sql.DB and *sql.Tx for the sync helpers above.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// migrateEntries handles two legacy schemas:
//   - pre-Phase 6: only `content` exists → add `new`.
//   - interim Phase 6: had `old + new` (content was dropped) → re-add `content`,
//     copy `old` → `content` (idempotent), drop `old`.
//
// End state: `content` (last reviewed) + `new` (pending, nullable) +
// `moved_from_namespace`/`moved_from_filename` (revert target for an
// unreviewed move, nullable).
// Safe to call repeatedly: every step is a no-op if the schema already matches.
func migrateEntries(db *sql.DB) error {
	cols, err := tableColumns(db, "entries")
	if err != nil {
		return err
	}
	hasContent := cols["content"]
	hasOld := cols["old"]
	hasNew := cols["new"]

	if !cols["moved_from_namespace"] {
		if _, err := db.Exec(`ALTER TABLE entries ADD COLUMN moved_from_namespace TEXT`); err != nil {
			return err
		}
	}
	if !cols["moved_from_filename"] {
		if _, err := db.Exec(`ALTER TABLE entries ADD COLUMN moved_from_filename TEXT`); err != nil {
			return err
		}
	}
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

// normalizeLineEndings folds CRLF → LF so stored content is canonical LF.
// Web clients submit CRLF (browser form encoding); MCP tools submit LF. The
// edit tool's literal string match would otherwise miss across clients.
func normalizeLineEndings(s string) string {
	if !strings.Contains(s, "\r\n") {
		return s
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// Write sets entries.new = content. Leaves the canonical `content` untouched.
// Wrapped in a tx so the FTS index stays in lock-step with entries.
func (s *SQLiteStore) Write(namespace, filename, content string) error {
	content = normalizeLineEndings(content)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rowid int64
	var body string
	err = tx.QueryRow(`
		INSERT INTO entries (namespace, filename, new, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(namespace, filename) DO UPDATE SET
			new        = excluded.new,
			updated_at = datetime('now')
		RETURNING rowid, COALESCE(new, content)
	`, namespace, filename, content).Scan(&rowid, &body)
	if err != nil {
		return err
	}
	if err := upsertFTS(tx, rowid, namespace, filename, body); err != nil {
		return err
	}
	if err := upsertNamespace(tx, namespace); err != nil {
		return err
	}
	return tx.Commit()
}

// Create inserts a brand-new entry, setting entries.new = content. Fails with
// ErrExists if the file already exists — callers that mean to replace an
// existing file should use ForceWrite. Wrapped in a tx so the existence check
// and insert are atomic and the FTS index stays in lock-step.
func (s *SQLiteStore) Create(namespace, filename, content string) error {
	content = normalizeLineEndings(content)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRow(`SELECT 1 FROM entries WHERE namespace=? AND filename=?`, namespace, filename).Scan(&exists)
	if err == nil {
		return ErrExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var rowid int64
	var body string
	err = tx.QueryRow(`
		INSERT INTO entries (namespace, filename, new, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		RETURNING rowid, COALESCE(new, content)
	`, namespace, filename, content).Scan(&rowid, &body)
	if err != nil {
		return err
	}
	if err := upsertFTS(tx, rowid, namespace, filename, body); err != nil {
		return err
	}
	if err := upsertNamespace(tx, namespace); err != nil {
		return err
	}
	return tx.Commit()
}

// ForceWrite sets entries.new = content on a file that already exists. Fails with
// ErrNotFound if the file does not exist — callers that mean to create a new
// file should use Create. Named to flag that blindly replacing a whole file is a
// last resort; prefer Write-via-edit for surgical changes.
func (s *SQLiteStore) ForceWrite(namespace, filename, content string) error {
	content = normalizeLineEndings(content)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rowid int64
	var body string
	err = tx.QueryRow(`
		UPDATE entries SET new = ?, updated_at = datetime('now')
		WHERE namespace=? AND filename=?
		RETURNING rowid, COALESCE(new, content)
	`, content, namespace, filename).Scan(&rowid, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := upsertFTS(tx, rowid, namespace, filename, body); err != nil {
		return err
	}
	return tx.Commit()
}

// Append reads COALESCE(new, content), appends, and writes the result to `new`.
func (s *SQLiteStore) Append(namespace, filename, content string) error {
	content = normalizeLineEndings(content)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rowid int64
	var body string
	err = tx.QueryRow(`
		INSERT INTO entries (namespace, filename, new, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(namespace, filename) DO UPDATE SET
			new = CASE
				WHEN COALESCE(entries.new, entries.content) = ''
					THEN excluded.new
					ELSE COALESCE(entries.new, entries.content) || char(10) || excluded.new
			END,
			updated_at = datetime('now')
		RETURNING rowid, COALESCE(new, content)
	`, namespace, filename, content).Scan(&rowid, &body)
	if err != nil {
		return err
	}
	if err := upsertFTS(tx, rowid, namespace, filename, body); err != nil {
		return err
	}
	if err := upsertNamespace(tx, namespace); err != nil {
		return err
	}
	return tx.Commit()
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
	// Capture rowid + current body before the rename — rowid persists through
	// UPDATE, and we need both for the FTS re-emit.
	var rowid int64
	var body string
	err = tx.QueryRow(
		`SELECT rowid, COALESCE(new, content) FROM entries WHERE namespace=? AND filename=?`,
		op.SrcNamespace, op.SrcFilename,
	).Scan(&rowid, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE entries SET namespace=?, filename=?, updated_at=datetime('now')
		 WHERE namespace=? AND filename=?`,
		op.DstNamespace, op.DstFilename, op.SrcNamespace, op.SrcFilename,
	); err != nil {
		return err
	}
	// Comments follow the file. Same tx — partial state never visible.
	if _, err := tx.Exec(
		`UPDATE comments SET namespace=?, filename=? WHERE namespace=? AND filename=?`,
		op.DstNamespace, op.DstFilename, op.SrcNamespace, op.SrcFilename,
	); err != nil {
		return err
	}
	// FTS namespace/basename may have changed; body is unchanged.
	if err := upsertFTS(tx, rowid, op.DstNamespace, op.DstFilename, body); err != nil {
		return err
	}
	// A move into a not-yet-seen namespace mints it.
	if err := upsertNamespace(tx, op.DstNamespace); err != nil {
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

// MoveForReview applies moves immediately (like MoveMany) but stamps each file
// with a revert target so the move shows up for review: until approved it can be
// rejected, which moves the file back. Mirrors how content edits go live via the
// `new` column while the last-reviewed value is preserved for rollback.
//
// The revert target is the file's last *reviewed* location, so it survives
// repeated unreviewed moves: it's only set when currently empty. Moving a file
// back onto its revert target clears the pointer — the file is reviewed-clean
// again. Atomic: any failure rolls back the whole batch.
func (s *SQLiteStore) MoveForReview(ops []MoveOp) error {
	if len(ops) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, op := range ops {
		if op.SrcNamespace == op.DstNamespace && op.SrcFilename == op.DstFilename {
			continue
		}
		// Capture the existing revert target before relocating the row. moveInTx
		// carries the columns along with the row, so we decide afterward whether
		// to stamp it.
		var fromNS, fromName sql.NullString
		err := tx.QueryRow(
			`SELECT moved_from_namespace, moved_from_filename FROM entries WHERE namespace=? AND filename=?`,
			op.SrcNamespace, op.SrcFilename,
		).Scan(&fromNS, &fromName)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := moveInTx(tx, op); err != nil {
			return err
		}
		switch {
		case fromNS.Valid && fromNS.String == op.DstNamespace && fromName.String == op.DstFilename:
			// Moved back onto the last-reviewed location — reviewed-clean again.
			if _, err := tx.Exec(
				`UPDATE entries SET moved_from_namespace=NULL, moved_from_filename=NULL WHERE namespace=? AND filename=?`,
				op.DstNamespace, op.DstFilename,
			); err != nil {
				return err
			}
		case !fromNS.Valid:
			// First unreviewed move from a clean state — record where to revert to.
			if _, err := tx.Exec(
				`UPDATE entries SET moved_from_namespace=?, moved_from_filename=? WHERE namespace=? AND filename=?`,
				op.SrcNamespace, op.SrcFilename, op.DstNamespace, op.DstFilename,
			); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// MovedFrom reports the revert target of an unreviewed move, if the file has one.
func (s *SQLiteStore) MovedFrom(namespace, filename string) (string, string, bool, error) {
	var fromNS, fromName sql.NullString
	err := s.db.QueryRow(
		`SELECT moved_from_namespace, moved_from_filename FROM entries WHERE namespace=? AND filename=?`,
		namespace, filename,
	).Scan(&fromNS, &fromName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, ErrNotFound
	}
	if err != nil {
		return "", "", false, err
	}
	if !fromNS.Valid || !fromName.Valid {
		return "", "", false, nil
	}
	return fromNS.String, fromName.String, true, nil
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
	var newRowid int64
	var body string
	err = tx.QueryRow(`
		INSERT INTO entries (namespace, filename, content, new, updated_at)
		SELECT ?, ?, content, new, datetime('now') FROM entries WHERE namespace=? AND filename=?
		RETURNING rowid, COALESCE(new, content)
	`, dstNS, dstName, srcNS, srcName).Scan(&newRowid, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := upsertFTS(tx, newRowid, dstNS, dstName, body); err != nil {
		return err
	}
	if err := upsertNamespace(tx, dstNS); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) Delete(namespace, filename string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rowid int64
	err = tx.QueryRow(
		`SELECT rowid FROM entries WHERE namespace=? AND filename=?`,
		namespace, filename,
	).Scan(&rowid)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM entries WHERE namespace=? AND filename=?`, namespace, filename); err != nil {
		return err
	}
	if err := deleteFTS(tx, rowid); err != nil {
		return err
	}
	return tx.Commit()
}

// ListNamespaces returns the active (not closed) namespaces. This is the default
// discovery surface: boot, read-miss suggestions, and the `namespaces` tool all
// use it, so a closed namespace is invisible unless a caller opts into
// ListNamespacesIncludingClosed.
func (s *SQLiteStore) ListNamespaces() ([]string, error) {
	return s.listNamespaces(false)
}

// ListNamespacesIncludingClosed returns every registered namespace, closed or
// not — the include_closed access path.
func (s *SQLiteStore) ListNamespacesIncludingClosed() ([]string, error) {
	return s.listNamespaces(true)
}

func (s *SQLiteStore) listNamespaces(includeClosed bool) ([]string, error) {
	q := `SELECT name FROM namespaces WHERE closed = 0 ORDER BY name`
	if includeClosed {
		q = `SELECT name FROM namespaces ORDER BY name`
	}
	rows, err := s.db.Query(q)
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

// NamespaceRecord is a namespace as the management tab sees it: its lifecycle
// bit plus its tags.
type NamespaceRecord struct {
	Name   string
	Closed bool
	Tags   []string
}

// NamespaceRecords returns every namespace (closed included) with its tags,
// name-sorted. Two queries stitched in Go — cheaper than a per-row tag fetch and
// there are few enough namespaces that a join+group buys nothing.
func (s *SQLiteStore) NamespaceRecords() ([]NamespaceRecord, error) {
	tagsByNS := map[string][]string{}
	tagRows, err := s.db.Query(`SELECT namespace, tag FROM namespace_tags ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	defer tagRows.Close()
	for tagRows.Next() {
		var ns, tag string
		if err := tagRows.Scan(&ns, &tag); err != nil {
			return nil, err
		}
		tagsByNS[ns] = append(tagsByNS[ns], tag)
	}
	if err := tagRows.Err(); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`SELECT name, closed FROM namespaces ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NamespaceRecord{}
	for rows.Next() {
		var rec NamespaceRecord
		if err := rows.Scan(&rec.Name, &rec.Closed); err != nil {
			return nil, err
		}
		rec.Tags = tagsByNS[rec.Name]
		out = append(out, rec)
	}
	return out, rows.Err()
}

// AddNamespaceTag attaches a tag to a namespace. Idempotent — re-tagging is a
// no-op. Tags are UI-only, open-vocabulary labels; the caller trims/validates.
func (s *SQLiteStore) AddNamespaceTag(namespace, tag string) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO namespace_tags(namespace, tag) VALUES (?, ?)`,
		namespace, tag,
	)
	return err
}

// RemoveNamespaceTag detaches a tag. A tag that isn't present is a silent no-op.
func (s *SQLiteStore) RemoveNamespaceTag(namespace, tag string) error {
	_, err := s.db.Exec(
		`DELETE FROM namespace_tags WHERE namespace = ? AND tag = ?`,
		namespace, tag,
	)
	return err
}

// IsNamespaceClosed reports whether a namespace exists and is closed. A
// nonexistent namespace returns false — the caller's own not-found path handles
// that; this only gates the closed case.
func (s *SQLiteStore) IsNamespaceClosed(name string) (bool, error) {
	var closed bool
	err := s.db.QueryRow(`SELECT closed FROM namespaces WHERE name = ?`, name).Scan(&closed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return closed, nil
}

// SetNamespaceClosed flips a namespace's closed bit. Lossless and reversible —
// the archive/reopen toggle, driven only from the UI. Errors with ErrNotFound if
// the namespace has no registry row.
func (s *SQLiteStore) SetNamespaceClosed(name string, closed bool) error {
	res, err := s.db.Exec(`UPDATE namespaces SET closed = ? WHERE name = ?`, closed, name)
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

func (s *SQLiteStore) List(namespace string) ([]FileEntry, error) {
	rows, err := s.db.Query(`
		SELECT filename, updated_at, length(COALESCE(new, content)) AS size,
		       (new IS NOT NULL OR moved_from_filename IS NOT NULL) AS pending
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

// Review: bless the file's current state. Promotes pending content (new → content,
// clear new) and clears any move revert target — the move already took effect, so
// approving just makes the current location the reviewed one. Marks the file's
// open comments reviewed (atomic). Errors with ErrNoPending if there is neither a
// pending content change nor an unreviewed move.
func (s *SQLiteStore) Review(namespace, filename string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var newVal, fromNS, fromName sql.NullString
	err = tx.QueryRow(
		`SELECT new, moved_from_namespace, moved_from_filename FROM entries WHERE namespace=? AND filename=?`,
		namespace, filename,
	).Scan(&newVal, &fromNS, &fromName)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	hasMove := fromNS.Valid && fromName.Valid
	if !newVal.Valid && !hasMove {
		return ErrNoPending
	}
	var rowid int64
	var body string
	// Promote pending content and/or clear the revert target in one update.
	if err := tx.QueryRow(
		`UPDATE entries SET content = COALESCE(new, content), new = NULL,
		        moved_from_namespace = NULL, moved_from_filename = NULL
		 WHERE namespace=? AND filename=?
		 RETURNING rowid, content`,
		namespace, filename,
	).Scan(&rowid, &body); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE comments SET reviewed=1 WHERE namespace=? AND filename=? AND reviewed=0`,
		namespace, filename,
	); err != nil {
		return err
	}
	// Content body is conceptually unchanged (COALESCE(new,content) is stable across
	// the promotion) but the column transition counts as a write — re-emit FTS to
	// stay consistent with the rest of the sync path.
	if err := upsertFTS(tx, rowid, namespace, filename, body); err != nil {
		return err
	}
	return tx.Commit()
}

// Reject: undo the file's unreviewed changes. Clears pending content (revert to
// last reviewed `content`) and, if the file was moved without review, moves it
// back to its revert target. Marks open comments reviewed. Errors with
// ErrNoPending if there is nothing to undo, or ErrDestinationExists if the
// revert target has since been occupied.
func (s *SQLiteStore) Reject(namespace, filename string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var newVal, fromNS, fromName sql.NullString
	err = tx.QueryRow(
		`SELECT new, moved_from_namespace, moved_from_filename FROM entries WHERE namespace=? AND filename=?`,
		namespace, filename,
	).Scan(&newVal, &fromNS, &fromName)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	hasMove := fromNS.Valid && fromName.Valid
	if !newVal.Valid && !hasMove {
		return ErrNoPending
	}
	var rowid int64
	var body string
	if err := tx.QueryRow(
		`UPDATE entries SET new = NULL WHERE namespace=? AND filename=?
		 RETURNING rowid, content`,
		namespace, filename,
	).Scan(&rowid, &body); err != nil {
		return err
	}
	// Body reverts to the canonical `content` — re-emit so search reflects it.
	if err := upsertFTS(tx, rowid, namespace, filename, body); err != nil {
		return err
	}
	// Comments mark reviewed at the file's final resting place, so move first.
	finalNS, finalName := namespace, filename
	if hasMove {
		if err := moveInTx(tx, MoveOp{namespace, filename, fromNS.String, fromName.String}); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE entries SET moved_from_namespace=NULL, moved_from_filename=NULL WHERE namespace=? AND filename=?`,
			fromNS.String, fromName.String,
		); err != nil {
			return err
		}
		finalNS, finalName = fromNS.String, fromName.String
	}
	if _, err := tx.Exec(
		`UPDATE comments SET reviewed=1 WHERE namespace=? AND filename=? AND reviewed=0`,
		finalNS, finalName,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// PendingFiles returns every file needing review — pending content (new IS
// NOT NULL) or an unreviewed move — ordered by most-recent-comment created_at
// DESC (falls back to entries.updated_at).
func (s *SQLiteStore) PendingFiles() ([]PendingFile, error) {
	rows, err := s.db.Query(`
		SELECT
			e.namespace,
			e.filename,
			e.updated_at,
			e.moved_from_namespace,
			e.moved_from_filename,
			(SELECT COUNT(*) FROM comments c WHERE c.namespace=e.namespace AND c.filename=e.filename) AS comment_count,
			(SELECT content FROM comments c2 WHERE c2.namespace=e.namespace AND c2.filename=e.filename ORDER BY c2.created_at DESC, c2.id DESC LIMIT 1) AS last_comment,
			(SELECT MAX(c3.created_at) FROM comments c3 WHERE c3.namespace=e.namespace AND c3.filename=e.filename) AS last_at
		FROM entries e
		WHERE e.new IS NOT NULL OR e.moved_from_filename IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingFile{}
	for rows.Next() {
		var p PendingFile
		var fromNS, fromName, lastComment, lastAt sql.NullString
		if err := rows.Scan(&p.Namespace, &p.Filename, &p.UpdatedAt, &fromNS, &fromName, &p.CommentCount, &lastComment, &lastAt); err != nil {
			return nil, err
		}
		if fromNS.Valid && fromName.Valid {
			p.MovedFromNamespace = fromNS.String
			p.MovedFromFilename = fromName.String
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
	sort.SliceStable(out, func(i, j int) bool { return out[i].SortAt > out[j].SortAt })
	return out, nil
}

func (s *SQLiteStore) GlobalPendingCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE new IS NOT NULL OR moved_from_filename IS NOT NULL`).Scan(&n)
	return n, err
}

func (s *SQLiteStore) NamespacePendingCount(namespace string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE namespace=? AND (new IS NOT NULL OR moved_from_filename IS NOT NULL)`, namespace).Scan(&n)
	return n, err
}

// Search runs FTS5 MATCH against entries_fts. If opts.Namespace is "" or "*"
// the search spans all namespaces; otherwise it's scoped to that one. The path
// glob (if any) is applied post-SQL via doublestar.
func (s *SQLiteStore) Search(opts SearchOptions) ([]SearchHit, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	orderClause := `bm25(entries_fts) ASC`
	switch opts.Order {
	case "", "bm25":
		// default
	case "recency":
		orderClause = `e.updated_at DESC`
	default:
		return nil, errors.New(`order must be "bm25" or "recency"`)
	}

	includeArchive := 0
	if opts.IncludeArchive {
		includeArchive = 1
	}
	includeClosed := 0
	if opts.IncludeClosed {
		includeClosed = 1
	}
	crossNS := opts.Namespace == "" || opts.Namespace == "*"
	nsClause := `AND entries_fts.namespace = ?`
	if crossNS {
		nsClause = ``
	}
	// Snippet args: column 2 = body (after namespace=0, basename=1). '<' '>'
	// as match delimiters, '…' ellipsis, 16-token window.
	q := `
		SELECT
			e.namespace,
			e.filename,
			e.updated_at,
			snippet(entries_fts, 2, '<', '>', '…', 16) AS snippet,
			bm25(entries_fts) AS score
		FROM entries_fts
		JOIN entries e ON e.rowid = entries_fts.rowid
		WHERE entries_fts MATCH ?
		  ` + nsClause + `
		  AND (? = 1 OR (
			e.filename NOT GLOB 'archive/*'
			AND e.filename NOT GLOB 'archived/*'
		  ))
		  AND e.filename NOT GLOB 'deleted/*'
		  AND (? = 1 OR entries_fts.namespace NOT IN (
			SELECT name FROM namespaces WHERE closed = 1
		  ))
		ORDER BY ` + orderClause + `
		LIMIT ?`

	// If a path glob is supplied we over-fetch from SQL and filter in Go —
	// doublestar's pattern syntax isn't expressible in SQLite GLOB. Cap the
	// over-fetch at the FTS5 native row budget so a pathological glob can't
	// exhaust memory.
	rowBudget := limit
	if opts.Path != "" {
		rowBudget = 500
	}
	queryArgs := []any{opts.Query}
	if !crossNS {
		queryArgs = append(queryArgs, opts.Namespace)
	}
	queryArgs = append(queryArgs, includeArchive, includeClosed, rowBudget)
	rows, err := s.db.Query(q, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SearchHit{}
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.Namespace, &h.Filename, &h.UpdatedAt, &h.Snippet, &h.Score); err != nil {
			return nil, err
		}
		if opts.Path != "" {
			match, perr := doublestar.Match(opts.Path, h.Filename)
			if perr != nil {
				return nil, perr
			}
			if !match {
				continue
			}
		}
		out = append(out, h)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}
