package main

import (
	"database/sql"
	"errors"
)

// Revisions are an append-only history of every state a file has been in —
// the version-control layer under the content/new review columns. Each row is
// a full snapshot of COALESCE(new, content) after the operation, so any two
// revisions diff directly with no replay. Rows are written in the same tx as
// the mutation they record, so history can never drift from entries.
//
// History follows the file: a move rewrites the path on its existing rows and
// appends a "move" row carrying the old path. A soft delete is a move into
// deleted/, so its history survives; a hard delete purges it.

// Revision ops.
const (
	RevImport  = "import" // seeded from pre-history entries on first boot
	RevWrite   = "write"
	RevCreate  = "create"
	RevAppend  = "append"
	RevCopy    = "copy"
	RevMove    = "move"
	RevReview  = "review"
	RevReject  = "reject"
	RevRestore = "restore"
)

type Revision struct {
	ID            int64
	Namespace     string
	Filename      string
	Op            string
	Content       string
	FromNamespace string // set on move/copy
	FromFilename  string
	CreatedAt     string
	Size          int
}

func createRevisionsSchema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS revisions (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		namespace      TEXT NOT NULL,
		filename       TEXT NOT NULL,
		op             TEXT NOT NULL,
		content        TEXT NOT NULL,
		from_namespace TEXT,
		from_filename  TEXT,
		created_at     DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS revisions_path ON revisions(namespace, filename, id)`); err != nil {
		return err
	}
	return seedRevisions(db)
}

// seedRevisions gives every entry without history a starting point: its
// reviewed content, then its pending value if one exists. Idempotent — only
// entries with no revision rows at all are seeded. An empty reviewed content
// under a pending value is a never-reviewed create, so it's skipped rather than
// recorded as a spurious empty revision.
func seedRevisions(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Snapshot the unseeded set first: the reviewed-content insert would
	// otherwise make the same entries look seeded to the pending insert.
	if _, err := tx.Exec(`CREATE TEMP TABLE unseeded AS
		SELECT namespace, filename, content, new FROM entries e
		WHERE NOT EXISTS (SELECT 1 FROM revisions r WHERE r.namespace = e.namespace AND r.filename = e.filename)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO revisions (namespace, filename, op, content)
		SELECT namespace, filename, ?, content FROM temp.unseeded
		WHERE new IS NULL OR content != ''
		ORDER BY namespace, filename`, RevImport); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO revisions (namespace, filename, op, content)
		SELECT namespace, filename, ?, new FROM temp.unseeded
		WHERE new IS NOT NULL
		ORDER BY namespace, filename`, RevImport); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE temp.unseeded`); err != nil {
		return err
	}
	return tx.Commit()
}

type queryExecer interface {
	execer
	QueryRow(query string, args ...any) *sql.Row
}

// recordRevision appends a snapshot of a file's current body. Content ops
// (write/create/append/restore) that leave the body identical to the latest
// revision are skipped so no-op saves don't clutter history; lifecycle ops
// (review/reject/move/copy) always record, since the event itself is the point.
func recordRevision(tx queryExecer, namespace, filename, op, body, fromNS, fromName string) error {
	switch op {
	case RevWrite, RevCreate, RevAppend, RevRestore:
		var last string
		err := tx.QueryRow(
			`SELECT content FROM revisions WHERE namespace=? AND filename=? ORDER BY id DESC LIMIT 1`,
			namespace, filename,
		).Scan(&last)
		if err == nil && last == body {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	_, err := tx.Exec(
		`INSERT INTO revisions (namespace, filename, op, content, from_namespace, from_filename)
		 VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))`,
		namespace, filename, op, body, fromNS, fromName,
	)
	return err
}

// moveRevisions carries a file's history to its new path.
func moveRevisions(tx execer, srcNS, srcName, dstNS, dstName string) error {
	_, err := tx.Exec(
		`UPDATE revisions SET namespace=?, filename=? WHERE namespace=? AND filename=?`,
		dstNS, dstName, srcNS, srcName,
	)
	return err
}

func purgeRevisions(tx execer, namespace, filename string) error {
	_, err := tx.Exec(`DELETE FROM revisions WHERE namespace=? AND filename=?`, namespace, filename)
	return err
}

// History returns a file's revisions newest-first, without bodies.
func (s *SQLiteStore) History(namespace, filename string) ([]Revision, error) {
	rows, err := s.db.Query(`
		SELECT id, namespace, filename, op, COALESCE(from_namespace, ''), COALESCE(from_filename, ''),
		       created_at, length(content)
		FROM revisions WHERE namespace=? AND filename=? ORDER BY id DESC`,
		namespace, filename,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		var r Revision
		if err := rows.Scan(&r.ID, &r.Namespace, &r.Filename, &r.Op, &r.FromNamespace, &r.FromFilename, &r.CreatedAt, &r.Size); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRevision returns one revision with its body.
func (s *SQLiteStore) GetRevision(id int64) (Revision, error) {
	var r Revision
	err := s.db.QueryRow(`
		SELECT id, namespace, filename, op, content, COALESCE(from_namespace, ''), COALESCE(from_filename, ''),
		       created_at, length(content)
		FROM revisions WHERE id=?`, id,
	).Scan(&r.ID, &r.Namespace, &r.Filename, &r.Op, &r.Content, &r.FromNamespace, &r.FromFilename, &r.CreatedAt, &r.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	return r, err
}

// PreviousRevision returns the revision immediately before id on the same
// file, or ErrNotFound if id is the file's first.
func (s *SQLiteStore) PreviousRevision(id int64) (Revision, error) {
	var prev int64
	err := s.db.QueryRow(`
		SELECT p.id FROM revisions p JOIN revisions r ON r.id = ?
		WHERE p.namespace = r.namespace AND p.filename = r.filename AND p.id < r.id
		ORDER BY p.id DESC LIMIT 1`, id,
	).Scan(&prev)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	if err != nil {
		return Revision{}, err
	}
	return s.GetRevision(prev)
}

// RestoreRevision writes an old revision's body back as the file's pending
// value, so the restore goes through review like any other change. Recreates
// the entry if it no longer exists at the revision's path.
func (s *SQLiteStore) RestoreRevision(id int64) (Revision, error) {
	rev, err := s.GetRevision(id)
	if err != nil {
		return Revision{}, err
	}
	return rev, s.writeOp(rev.Namespace, rev.Filename, rev.Content, RevRestore)
}
