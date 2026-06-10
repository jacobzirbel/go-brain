package main

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// ── write/edit/append with optional comment ─────────────────────────────────

func TestWriteTool_NoComment_NoCommentRow(t *testing.T) {
	s := setupStore(t)
	_, isErr := runTool(s, "create", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
		"content":   "hello",
	})
	if isErr {
		t.Fatal("create failed")
	}
	cs, err := s.ListComments("ns", "f.md", true, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 0 {
		t.Fatalf("expected no comment rows; got %d", len(cs))
	}
}

func TestWriteTool_WithComment_InsertsRow(t *testing.T) {
	s := setupStore(t)
	_, _ = runTool(s, "create", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
		"content":   "hello",
		"comment":   "first draft",
	})
	cs, _ := s.ListComments("ns", "f.md", true, 100, 0)
	if len(cs) != 1 || cs[0].Content != "first draft" {
		t.Fatalf("expected one comment 'first draft', got %v", cs)
	}
}

func TestEditTool_WithComment_InsertsRow(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "alpha beta")
	_, isErr := runTool(s, "edit", map[string]any{
		"namespace":  "ns",
		"filename":   "f.md",
		"old_str": "beta",
		"new_str": "GAMMA",
		"comment":    "renamed beta",
	})
	if isErr {
		t.Fatal("edit failed")
	}
	cs, _ := s.ListComments("ns", "f.md", true, 100, 0)
	if len(cs) != 1 || cs[0].Content != "renamed beta" {
		t.Fatalf("expected comment 'renamed beta', got %v", cs)
	}
}

func TestAppendTool_WithComment_InsertsRow(t *testing.T) {
	s := setupStore(t)
	_, _ = runTool(s, "append", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
		"content":   "line1",
		"comment":   "first line",
	})
	cs, _ := s.ListComments("ns", "f.md", true, 100, 0)
	if len(cs) != 1 || cs[0].Content != "first line" {
		t.Fatalf("expected one comment, got %v", cs)
	}
}

// ── new/old column semantics ────────────────────────────────────────────────

func TestWrite_BumpsNew_LeavesContent(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "v1")
	if err := s.Review("ns", "f.md"); err != nil {
		t.Fatal(err)
	}
	// Second write: new = "v2", content still "v1".
	_ = s.Write("ns", "f.md", "v2")
	content, newVal, _, err := s.ReadEntry("ns", "f.md")
	if err != nil {
		t.Fatal(err)
	}
	if content != "v1" {
		t.Errorf("expected content=v1, got %q", content)
	}
	if !newVal.Valid || newVal.String != "v2" {
		t.Errorf("expected new=v2, got %v", newVal)
	}
}

func TestRead_ReturnsNewWhenPresent_ContentOtherwise(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "pending")
	got, _, _ := s.Read("ns", "f.md")
	if got != "pending" {
		t.Errorf("expected 'pending', got %q", got)
	}
	if err := s.Review("ns", "f.md"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Read("ns", "f.md")
	if got != "pending" {
		t.Errorf("after review expected 'pending' (now in content), got %q", got)
	}
}

func TestReview_PromotesNewClearsOpenComments(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "v1")
	_ = s.InsertComment("ns", "f.md", "looks good")
	if err := s.Review("ns", "f.md"); err != nil {
		t.Fatal(err)
	}
	_, newVal, _, _ := s.ReadEntry("ns", "f.md")
	if newVal.Valid {
		t.Error("expected new=NULL after review")
	}
	cs, _ := s.ListComments("ns", "f.md", true, 100, 0)
	if len(cs) != 1 || !cs[0].Reviewed {
		t.Errorf("comment should be marked reviewed, got %v", cs)
	}
}

func TestReject_ClearsNewMarksCommentsReviewed(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "v1")
	if err := s.Review("ns", "f.md"); err != nil {
		t.Fatal(err)
	}
	_ = s.Write("ns", "f.md", "v2-bad")
	_ = s.InsertComment("ns", "f.md", "bad change")
	if err := s.Reject("ns", "f.md"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Read("ns", "f.md")
	if got != "v1" {
		t.Errorf("expected reject to revert to v1, got %q", got)
	}
	cs, _ := s.ListComments("ns", "f.md", true, 100, 0)
	if len(cs) != 1 || !cs[0].Reviewed {
		t.Errorf("comment should be marked reviewed after reject, got %v", cs)
	}
}

func TestReviewAndReject_ErrorWhenNewIsNull(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "v1")
	if err := s.Review("ns", "f.md"); err != nil {
		t.Fatal(err)
	}
	if err := s.Review("ns", "f.md"); !errors.Is(err, ErrNoPending) {
		t.Errorf("expected ErrNoPending on second review, got %v", err)
	}
	if err := s.Reject("ns", "f.md"); !errors.Is(err, ErrNoPending) {
		t.Errorf("expected ErrNoPending on reject with no pending, got %v", err)
	}
}

// ── move comments-follow ────────────────────────────────────────────────────

func TestMove_CommentsFollowFile(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "x")
	_ = s.InsertComment("ns", "a.md", "context")
	if err := s.Move("ns", "a.md", "ns", "b.md"); err != nil {
		t.Fatal(err)
	}
	atSrc, _ := s.ListComments("ns", "a.md", true, 100, 0)
	if len(atSrc) != 0 {
		t.Errorf("comments should not remain at source name; got %v", atSrc)
	}
	atDst, _ := s.ListComments("ns", "b.md", true, 100, 0)
	if len(atDst) != 1 || atDst[0].Content != "context" {
		t.Errorf("expected comment at new name; got %v", atDst)
	}
}

func TestMoveMany_CommentsFollow_RollbackOnFailure(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "A")
	_ = s.Write("ns", "b.md", "B")
	_ = s.Write("ns", "occupied.md", "X")
	_ = s.InsertComment("ns", "a.md", "comment-a")
	_ = s.InsertComment("ns", "b.md", "comment-b")

	// Second move collides → whole tx rolls back.
	err := s.MoveMany([]MoveOp{
		{SrcNamespace: "ns", SrcFilename: "a.md", DstNamespace: "ns", DstFilename: "archive/a.md"},
		{SrcNamespace: "ns", SrcFilename: "b.md", DstNamespace: "ns", DstFilename: "occupied.md"},
	})
	if !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("expected ErrDestinationExists, got %v", err)
	}
	// Comments must still be at original files.
	ca, _ := s.ListComments("ns", "a.md", true, 100, 0)
	if len(ca) != 1 {
		t.Errorf("comment-a was lost on rollback: %v", ca)
	}
	cb, _ := s.ListComments("ns", "b.md", true, 100, 0)
	if len(cb) != 1 {
		t.Errorf("comment-b was lost on rollback: %v", cb)
	}
	// And not at the proposed destinations.
	if c, _ := s.ListComments("ns", "archive/a.md", true, 100, 0); len(c) != 0 {
		t.Errorf("comments leaked to archive/a.md: %v", c)
	}
}

// ── copy does not carry comments ────────────────────────────────────────────

func TestCopy_DoesNotCarryComments(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "src.md", "hello")
	_ = s.InsertComment("ns", "src.md", "stay-with-src")

	_, isErr := runTool(s, "copy", map[string]any{
		"namespace":    "ns",
		"filename":     "src.md",
		"new_filename": "dst.md",
	})
	if isErr {
		t.Fatal("copy failed")
	}
	srcC, _ := s.ListComments("ns", "src.md", true, 100, 0)
	if len(srcC) != 1 {
		t.Errorf("source comment vanished: %v", srcC)
	}
	dstC, _ := s.ListComments("ns", "dst.md", true, 100, 0)
	if len(dstC) != 0 {
		t.Errorf("comments should NOT be copied; got %v", dstC)
	}
}

// ── archive/rm leaves comments attached (they follow the rename) ────────────

func TestArchive_CommentsStayAttached(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "x")
	_ = s.InsertComment("ns", "f.md", "stays")

	_, isErr := runTool(s, "archive", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
	})
	if isErr {
		t.Fatal("archive failed")
	}
	cs, _ := s.ListComments("ns", "archived/f.md", true, 100, 0)
	if len(cs) != 1 || cs[0].Content != "stays" {
		t.Errorf("comment should follow archived file; got %v", cs)
	}
}

// ── export zip ──────────────────────────────────────────────────────────────

func TestExport_OmitsArchive_IncludesCoalesce(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "active.md", "active-content")
	_ = s.Write("ns", "archive/old.md", "archived")
	_ = s.Write("ns", "archived/oldish.md", "archivedish")
	_ = s.Write("ns", "deleted/gone.md", "rm'd")

	// Stand up the UI with a real session so the handler accepts the request.
	store = s
	tok := "tok"
	_ = s.CreateSession(tok)

	mux := http.NewServeMux()
	registerUIRoutes(mux)
	req := httptest.NewRequest("GET", "/ui/export/ns.zip", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content-type: %q", ct)
	}
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		names[f.Name] = string(b)
	}
	if names["active.md"] != "active-content" {
		t.Errorf("active.md content wrong: %q", names["active.md"])
	}
	for _, archived := range []string{"archive/old.md", "archived/oldish.md", "deleted/gone.md"} {
		if _, ok := names[archived]; ok {
			t.Errorf("archive path leaked into zip: %s", archived)
		}
	}
}

func TestExport_ReturnsCoalesceNewOverOld(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "v1-old")
	_ = s.Review("ns", "f.md")
	_ = s.Write("ns", "f.md", "v2-pending")

	store = s
	tok := "tok2"
	_ = s.CreateSession(tok)

	mux := http.NewServeMux()
	registerUIRoutes(mux)
	req := httptest.NewRequest("GET", "/ui/export/ns.zip", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	zr, _ := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	for _, f := range zr.File {
		if f.Name == "f.md" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			if string(b) != "v2-pending" {
				t.Errorf("export should reflect COALESCE(new, content) = v2-pending; got %q", string(b))
			}
			return
		}
	}
	t.Fatal("f.md not in zip")
}

// ── migrations ──────────────────────────────────────────────────────────────

func TestMigration_PrePhase6_OnlyContentColumn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// Pre-Phase 6 schema: `content` only, no `new`, no `old`.
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE entries (
		namespace TEXT NOT NULL,
		filename  TEXT NOT NULL,
		content   TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (namespace, filename)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO entries (namespace, filename, content) VALUES (?, ?, ?)`,
		"ns", "f.md", "legacy-content"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	got, _, err := s.Read("ns", "f.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != "legacy-content" {
		t.Errorf("expected migrated content, got %q", got)
	}
	content, newVal, _, _ := s.ReadEntry("ns", "f.md")
	if content != "legacy-content" {
		t.Errorf("expected content=legacy-content, got %q", content)
	}
	if newVal.Valid {
		t.Errorf("expected new=NULL after migration, got %v", newVal)
	}

	cols, _ := tableColumns(s.db, "entries")
	if cols["old"] {
		t.Error("`old` column should not exist post-migration")
	}
	if !cols["content"] || !cols["new"] {
		t.Errorf("expected content+new columns; got %v", cols)
	}
}

func TestMigration_InterimPhase6_DropsOldCopiesIntoContent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "interim.db")

	// Interim Phase 6 schema: `old + new`, no `content`. This is the state a
	// prod DB would be in after the first Phase 6 deploy, before this refactor.
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE entries (
		namespace TEXT NOT NULL,
		filename  TEXT NOT NULL,
		old       TEXT NOT NULL DEFAULT '',
		new       TEXT,
		updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (namespace, filename)
	)`); err != nil {
		t.Fatal(err)
	}
	// Two rows: one reviewed (new IS NULL), one with pending change.
	if _, err := raw.Exec(`INSERT INTO entries (namespace, filename, old, new) VALUES (?, ?, ?, NULL)`,
		"ns", "reviewed.md", "canonical"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO entries (namespace, filename, old, new) VALUES (?, ?, ?, ?)`,
		"ns", "pending.md", "v1", "v2-pending"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Reviewed row: content=canonical, new=NULL.
	c, nv, _, _ := s.ReadEntry("ns", "reviewed.md")
	if c != "canonical" || nv.Valid {
		t.Errorf("reviewed.md: content=%q new=%v (expected canonical / NULL)", c, nv)
	}
	// Pending row: content=v1 (from old), new=v2-pending preserved.
	c, nv, _, _ = s.ReadEntry("ns", "pending.md")
	if c != "v1" {
		t.Errorf("pending.md: content=%q (expected v1)", c)
	}
	if !nv.Valid || nv.String != "v2-pending" {
		t.Errorf("pending.md: new=%v (expected v2-pending)", nv)
	}
	// Read returns COALESCE → for pending.md that's v2-pending.
	got, _, _ := s.Read("ns", "pending.md")
	if got != "v2-pending" {
		t.Errorf("Read should return COALESCE; got %q", got)
	}

	cols, _ := tableColumns(s.db, "entries")
	if cols["old"] {
		t.Error("`old` column should have been dropped")
	}
	if !cols["content"] || !cols["new"] {
		t.Errorf("expected content+new columns; got %v", cols)
	}
}

// Schema-shape test: a fresh store should have exactly the expected columns,
// no `old`. Guards against drift from the canonical schema.
func TestSchema_EntriesColumns(t *testing.T) {
	s := setupStore(t)
	cols, err := tableColumns(s.db, "entries")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"namespace":      true,
		"filename":       true,
		"content":        true,
		"new":            true,
		"move_namespace": true,
		"move_filename":  true,
		"updated_at":     true,
	}
	for k := range want {
		if !cols[k] {
			t.Errorf("missing expected column %q (got %v)", k, cols)
		}
	}
	for k := range cols {
		if !want[k] {
			t.Errorf("unexpected column %q in entries (want %v)", k, want)
		}
	}
}

// ── pending detection: List returns HasPending flag ─────────────────────────

func TestList_HasPendingFlag(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "pending.md", "v1")
	_ = s.Write("ns", "clean.md", "v1")
	_ = s.Review("ns", "clean.md")

	files, _ := s.List("ns")
	got := map[string]bool{}
	for _, f := range files {
		got[f.Filename] = f.HasPending
	}
	if !got["pending.md"] {
		t.Error("pending.md should be flagged")
	}
	if got["clean.md"] {
		t.Error("clean.md should NOT be flagged")
	}
}

func TestGlobalPendingCount(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("a", "x.md", "v1")
	_ = s.Write("b", "y.md", "v1")
	_ = s.Write("b", "z.md", "v1")
	_ = s.Review("b", "z.md")
	n, _ := s.GlobalPendingCount()
	if n != 2 {
		t.Errorf("expected 2 pending, got %d", n)
	}
}

// ── snippet helper used by inbox view ───────────────────────────────────────

func TestSnippetHelperTruncatesLongComments(t *testing.T) {
	fn := uiFuncs["snippet"].(func(string) string)
	long := strings.Repeat("x", 200)
	got := fn(long)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis on truncation, got %q", got)
	}
	if len(got) > 145 {
		t.Errorf("snippet too long: %d", len(got))
	}
}
