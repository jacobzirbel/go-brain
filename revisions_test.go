package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// revOps returns a file's history oldest-first as "op:content" pairs.
func revOps(t *testing.T, s *SQLiteStore, ns, fn string) []string {
	t.Helper()
	revs, err := s.History(ns, fn)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(revs))
	for i, r := range revs {
		full, err := s.GetRevision(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[len(revs)-1-i] = full.Op + ":" + full.Content
	}
	return out
}

func assertOps(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("history:\n got  %q\n want %q", got, want)
	}
}

func TestRevisions_EveryContentChangeSnapshotted(t *testing.T) {
	s := setupStore(t)
	if err := s.Create("ns", "f.md", "a"); err != nil {
		t.Fatal(err)
	}
	_ = s.Append("ns", "f.md", "b")
	_ = s.Write("ns", "f.md", "c")
	_ = s.ForceWrite("ns", "f.md", "d")
	_ = s.Review("ns", "f.md")
	_ = s.Write("ns", "f.md", "e")
	_ = s.Reject("ns", "f.md")
	assertOps(t, revOps(t, s, "ns", "f.md"),
		"create:a", "append:a\nb", "write:c", "write:d", "review:d", "write:e", "reject:d")
}

func TestRevisions_NoOpWriteSkipped(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "same")
	_ = s.Write("ns", "f.md", "same")
	_ = s.Write("ns", "f.md", "same\r\n") // normalizes to a different body
	assertOps(t, revOps(t, s, "ns", "f.md"), "write:same", "write:same\n")
}

func TestRevisions_ToolEditAndSectionWritesRecorded(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "# A\nalpha\n")
	if _, isErr := runTool(s, "edit", map[string]any{
		"namespace": "ns", "filename": "f.md", "old_str": "alpha", "new_str": "beta",
	}); isErr {
		t.Fatal("edit failed")
	}
	if _, isErr := runTool(s, "upsert_section", map[string]any{
		"namespace": "ns", "filename": "f.md", "section": "B", "content": "gamma",
	}); isErr {
		t.Fatal("upsert_section failed")
	}
	got := revOps(t, s, "ns", "f.md")
	if len(got) != 3 || got[1] != "write:# A\nbeta\n" || !strings.Contains(got[2], "gamma") {
		t.Fatalf("unexpected history %q", got)
	}
}

func TestRevisions_HistoryFollowsMoveAndReject(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "x")
	_ = s.Review("ns", "a.md")
	if err := s.MoveForReview([]MoveOp{{"ns", "a.md", "other", "b.md"}}); err != nil {
		t.Fatal(err)
	}
	if revs, _ := s.History("ns", "a.md"); len(revs) != 0 {
		t.Fatalf("old path should have no history left, got %d", len(revs))
	}
	assertOps(t, revOps(t, s, "other", "b.md"), "write:x", "review:x", "move:x")
	revs, _ := s.History("other", "b.md")
	if revs[0].FromNamespace != "ns" || revs[0].FromFilename != "a.md" {
		t.Fatalf("move revision should carry source path, got %+v", revs[0])
	}

	// Rejecting the move sends the file — and its history — back.
	if err := s.Reject("other", "b.md"); err != nil {
		t.Fatal(err)
	}
	assertOps(t, revOps(t, s, "ns", "a.md"), "write:x", "review:x", "move:x", "move:x", "reject:x")
}

func TestRevisions_SoftDeleteKeepsHistory_HardDeletePurges(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "x")
	if _, isErr := runTool(s, "remove", map[string]any{"namespace": "ns", "filename": "f.md"}); isErr {
		t.Fatal("remove failed")
	}
	assertOps(t, revOps(t, s, "ns", "deleted/f.md"), "write:x", "move:x")

	if err := s.Delete("ns", "deleted/f.md"); err != nil {
		t.Fatal(err)
	}
	if revs, _ := s.History("ns", "deleted/f.md"); len(revs) != 0 {
		t.Fatalf("hard delete should purge history, got %d rows", len(revs))
	}
}

func TestRevisions_CopyStartsFreshHistory(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "1")
	_ = s.Write("ns", "a.md", "2")
	if err := s.Copy("ns", "a.md", "ns", "b.md"); err != nil {
		t.Fatal(err)
	}
	assertOps(t, revOps(t, s, "ns", "b.md"), "copy:2")
	assertOps(t, revOps(t, s, "ns", "a.md"), "write:1", "write:2")
}

func TestRevisions_RestoreBecomesPending(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "v1")
	_ = s.Review("ns", "f.md")
	_ = s.Write("ns", "f.md", "v2")
	_ = s.Review("ns", "f.md")
	revs, _ := s.History("ns", "f.md")
	first := revs[len(revs)-1]

	if _, err := s.RestoreRevision(first.ID); err != nil {
		t.Fatal(err)
	}
	content, newVal, _, _ := s.ReadEntry("ns", "f.md")
	if content != "v2" || !newVal.Valid || newVal.String != "v1" {
		t.Fatalf("restore should stage v1 as pending over reviewed v2; content=%q new=%v", content, newVal)
	}
	got := revOps(t, s, "ns", "f.md")
	if got[len(got)-1] != "restore:v1" {
		t.Fatalf("expected trailing restore revision, got %q", got)
	}
}

func TestRevisions_RestoreRecreatesHardDeletedPath(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "keep")
	revs, _ := s.History("ns", "f.md")
	id := revs[0].ID
	// Simulate the entry vanishing while history remains (e.g. a hand-edited DB).
	if _, err := s.db.Exec(`DELETE FROM entries WHERE namespace='ns' AND filename='f.md'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreRevision(id); err != nil {
		t.Fatal(err)
	}
	if got, _, err := s.Read("ns", "f.md"); err != nil || got != "keep" {
		t.Fatalf("restore should recreate the file; got %q err=%v", got, err)
	}
}

func TestRevisions_PreviousRevision(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "1")
	_ = s.Write("ns", "other.md", "zzz") // interleaved file must not be picked
	_ = s.Write("ns", "f.md", "2")
	revs, _ := s.History("ns", "f.md")
	prev, err := s.PreviousRevision(revs[0].ID)
	if err != nil || prev.Content != "1" {
		t.Fatalf("expected previous=1, got %+v err=%v", prev, err)
	}
	if _, err := s.PreviousRevision(revs[1].ID); err != ErrNotFound {
		t.Fatalf("first revision should have no previous, got %v", err)
	}
}

func TestRevisions_SeedsPreexistingEntries(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "old.db")
	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate a pre-revisions DB: entries in assorted review states, no history.
	_ = s.Write("ns", "reviewed.md", "r")
	_ = s.Review("ns", "reviewed.md")
	_ = s.Write("ns", "pending.md", "p1")
	_ = s.Review("ns", "pending.md")
	_ = s.Write("ns", "pending.md", "p2")
	_ = s.Write("ns", "fresh.md", "never reviewed")
	if _, err := s.db.Exec(`DROP TABLE revisions`); err != nil {
		t.Fatal(err)
	}
	s.db.Close()

	for i := 0; i < 2; i++ { // second open must be a no-op
		s, err = NewSQLiteStore(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		assertOps(t, revOps(t, s, "ns", "reviewed.md"), "import:r")
		assertOps(t, revOps(t, s, "ns", "pending.md"), "import:p1", "import:p2")
		assertOps(t, revOps(t, s, "ns", "fresh.md"), "import:never reviewed")
		s.db.Close()
	}
}

func TestRevisions_SchemaCreatedOnLegacyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE entries (
		namespace TEXT NOT NULL, filename TEXT NOT NULL, content TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL DEFAULT (datetime('now')), PRIMARY KEY (namespace, filename))`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO entries (namespace, filename, content) VALUES ('ns','f.md','legacy')`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	assertOps(t, revOps(t, s, "ns", "f.md"), "import:legacy")
}

// ── UI ──────────────────────────────────────────────────────────────────────

func uiDo(t *testing.T, s *SQLiteStore, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	srv := newServer(config{}, s)
	_ = s.CreateSession("tok")
	mux := http.NewServeMux()
	srv.registerUIRoutes(mux)
	req := httptest.NewRequest(method, target, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "tok"})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestUIHistory_ListDiffRestore(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "old body")
	_ = s.Review("ns", "f.md")
	_ = s.Write("ns", "f.md", "new body")
	revs, _ := s.History("ns", "f.md")
	newest, oldest := revs[0].ID, revs[len(revs)-1].ID

	rr := uiDo(t, s, "GET", "/ui/history?ns=ns&name=f.md")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "review") {
		t.Fatalf("history: %d %s", rr.Code, rr.Body.String())
	}

	rr = uiDo(t, s, "GET", "/ui/revision?id="+strconv.FormatInt(newest, 10))
	body := rr.Body.String()
	if rr.Code != 200 || !strings.Contains(body, "new body") || !strings.Contains(body, "old body") {
		t.Fatalf("revision diff vs previous: %d %s", rr.Code, body)
	}
	rr = uiDo(t, s, "GET", "/ui/revision?id="+strconv.FormatInt(oldest, 10))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "first revision") {
		t.Fatalf("first revision should diff against empty: %d", rr.Code)
	}

	rr = uiDo(t, s, "POST", "/ui/restore?id="+strconv.FormatInt(oldest, 10))
	if rr.Code != http.StatusFound {
		t.Fatalf("restore: %d %s", rr.Code, rr.Body.String())
	}
	if got, _, _ := s.Read("ns", "f.md"); got != "old body" {
		t.Fatalf("restore didn't apply, got %q", got)
	}

	if rr := uiDo(t, s, "GET", "/ui/history?ns=ns&name=missing.md"); rr.Code != 404 {
		t.Fatalf("history of unknown file should 404, got %d", rr.Code)
	}
	if rr := uiDo(t, s, "GET", "/ui/revision?id=99999"); rr.Code != 404 {
		t.Fatalf("unknown revision should 404, got %d", rr.Code)
	}
}
