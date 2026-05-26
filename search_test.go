package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// ── schema / migration ──────────────────────────────────────────────────────

func TestFTS_SchemaExists(t *testing.T) {
	s := setupStore(t)
	var name string
	err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='entries_fts'`).Scan(&name)
	if err != nil {
		t.Fatalf("entries_fts table missing: %v", err)
	}
}

func TestFTS_BackfillFromExistingEntries(t *testing.T) {
	// Simulate a DB that already has entries but no FTS rows (the prod situation
	// on first deploy of Phase 8): create the entries table, insert rows
	// directly via raw SQL, then open via NewSQLiteStore and expect backfill.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pre.db")

	// Boot once to lay down the schema, then wipe the FTS index to simulate
	// pre-Phase-8 state with entries present.
	s1, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = s1.Write("ns", "a.md", "alpha")
	_ = s1.Write("ns", "b.md", "beta")
	if _, err := s1.db.Exec(`DELETE FROM entries_fts`); err != nil {
		t.Fatal(err)
	}
	s1.db.Close()

	// Re-open — should backfill.
	s2, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM entries_fts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("expected 2 backfilled rows, got %d", n)
	}
}

func TestFTS_BackfillIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "p.db")
	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Write("ns", "f.md", "hello")
	s.db.Close()

	s2, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s2.db.QueryRow(`SELECT COUNT(*) FROM entries_fts`).Scan(&n)
	if n != 1 {
		t.Errorf("second boot doubled FTS rows; got %d", n)
	}
}

// ── sync: write/edit/append/review/reject/move/copy/delete ──────────────────

func searchAll(t *testing.T, s *SQLiteStore, ns, query string) []SearchHit {
	t.Helper()
	hits, err := s.Search(SearchOptions{Namespace: ns, Query: query, Limit: 100})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	return hits
}

func TestFTS_WriteIndexesBody(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", "uniqueZZZQ nonce string")
	hits := searchAll(t, s, "ns", "uniqueZZZQ")
	if len(hits) != 1 || hits[0].Filename != "doc.md" {
		t.Errorf("expected doc.md hit, got %v", hits)
	}
}

func TestFTS_BasenameIndexed(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "tasks/foo/QQQunique.md", "body")
	hits := searchAll(t, s, "ns", "QQQunique")
	if len(hits) != 1 {
		t.Errorf("basename should match; got %v", hits)
	}
}

func TestFTS_FolderNameDoesNotMatch(t *testing.T) {
	// "tasks" in folder path should NOT match (folder tokens not indexed).
	s := setupStore(t)
	_ = s.Write("ns", "tasks/a.md", "alpha")
	_ = s.Write("ns", "tasks/b.md", "beta")
	_ = s.Write("ns", "decisions.md", "discussing tasks here")

	hits := searchAll(t, s, "ns", "tasks")
	// Only decisions.md (body contains "tasks") should hit.
	if len(hits) != 1 || hits[0].Filename != "decisions.md" {
		t.Errorf("expected only decisions.md (body match); got %v", hits)
	}
}

func TestFTS_NamespaceUnindexed(t *testing.T) {
	// Searching for the namespace name in another namespace returns nothing.
	s := setupStore(t)
	_ = s.Write("alpha", "x.md", "irrelevant")
	_ = s.Write("beta", "y.md", "irrelevant")
	hits := searchAll(t, s, "beta", "alpha")
	if len(hits) != 0 {
		t.Errorf("namespace name should not be searchable; got %v", hits)
	}
}

func TestFTS_OverwriteUpdatesIndex(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "FIRSTnonce")
	_ = s.Write("ns", "f.md", "SECONDnonce")
	if hits := searchAll(t, s, "ns", "FIRSTnonce"); len(hits) != 0 {
		t.Errorf("old body should not be searchable; got %v", hits)
	}
	if hits := searchAll(t, s, "ns", "SECONDnonce"); len(hits) != 1 {
		t.Errorf("new body should be searchable; got %v", hits)
	}
}

func TestFTS_AppendUpdatesIndex(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "ORIGINALnonce")
	_ = s.Append("ns", "f.md", "APPENDEDnonce")
	hits := searchAll(t, s, "ns", "APPENDEDnonce")
	if len(hits) != 1 {
		t.Errorf("append should be searchable; got %v", hits)
	}
}

func TestFTS_ReviewKeepsBodyIndexed(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "QQreviewable")
	if err := s.Review("ns", "f.md"); err != nil {
		t.Fatal(err)
	}
	hits := searchAll(t, s, "ns", "QQreviewable")
	if len(hits) != 1 {
		t.Errorf("body should remain indexed after review; got %v", hits)
	}
}

func TestFTS_RejectRevertsIndexedBody(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "ORIGINALrejnonce")
	if err := s.Review("ns", "f.md"); err != nil {
		t.Fatal(err)
	}
	_ = s.Write("ns", "f.md", "PENDINGrejnonce")
	if err := s.Reject("ns", "f.md"); err != nil {
		t.Fatal(err)
	}
	if hits := searchAll(t, s, "ns", "PENDINGrejnonce"); len(hits) != 0 {
		t.Errorf("rejected pending body should not be searchable; got %v", hits)
	}
	if hits := searchAll(t, s, "ns", "ORIGINALrejnonce"); len(hits) != 1 {
		t.Errorf("reviewed body should still be searchable after reject; got %v", hits)
	}
}

func TestFTS_MoveUpdatesBasename(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "OLDbasename.md", "static body")
	if err := s.Move("ns", "OLDbasename.md", "ns", "NEWbasename.md"); err != nil {
		t.Fatal(err)
	}
	if hits := searchAll(t, s, "ns", "OLDbasename"); len(hits) != 0 {
		t.Errorf("old basename should no longer match; got %v", hits)
	}
	if hits := searchAll(t, s, "ns", "NEWbasename"); len(hits) != 1 {
		t.Errorf("new basename should match; got %v", hits)
	}
}

func TestFTS_MoveFolderOnlyKeepsBasenameMatch(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a/SAMEbasename.md", "static body")
	if err := s.Move("ns", "a/SAMEbasename.md", "ns", "b/SAMEbasename.md"); err != nil {
		t.Fatal(err)
	}
	hits := searchAll(t, s, "ns", "SAMEbasename")
	if len(hits) != 1 || hits[0].Filename != "b/SAMEbasename.md" {
		t.Errorf("basename should still match after folder move; got %v", hits)
	}
	// path glob now reflects new folder.
	hits, _ = s.Search(SearchOptions{Namespace: "ns", Query: "SAMEbasename", Path: "b/**"})
	if len(hits) != 1 {
		t.Errorf("path=b/** should find the moved file; got %v", hits)
	}
	hits, _ = s.Search(SearchOptions{Namespace: "ns", Query: "SAMEbasename", Path: "a/**"})
	if len(hits) != 0 {
		t.Errorf("path=a/** should not match after move; got %v", hits)
	}
}

func TestFTS_CopyCreatesIndependentIndex(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "src.md", "copybody NONCE")
	if err := s.Copy("ns", "src.md", "ns", "dst.md"); err != nil {
		t.Fatal(err)
	}
	hits := searchAll(t, s, "ns", "copybody")
	if len(hits) != 2 {
		t.Errorf("both copies should be indexed; got %v", hits)
	}
	// Mutate src; dst should be unaffected in search.
	_ = s.Write("ns", "src.md", "mutated")
	hits = searchAll(t, s, "ns", "copybody")
	if len(hits) != 1 || hits[0].Filename != "dst.md" {
		t.Errorf("only dst.md should still match; got %v", hits)
	}
}

func TestFTS_DeleteRemovesFromIndex(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "DELnonce")
	if err := s.Delete("ns", "f.md"); err != nil {
		t.Fatal(err)
	}
	if hits := searchAll(t, s, "ns", "DELnonce"); len(hits) != 0 {
		t.Errorf("deleted row should not match; got %v", hits)
	}
}

// ── archive exclusion ───────────────────────────────────────────────────────

func TestFTS_ExcludesArchiveByDefault(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "active.md", "QXXnonce here")
	_ = s.Write("ns", "archive/old.md", "QXXnonce here")
	_ = s.Write("ns", "archived/oldish.md", "QXXnonce here")
	_ = s.Write("ns", "deleted/gone.md", "QXXnonce here")

	hits := searchAll(t, s, "ns", "QXXnonce")
	if len(hits) != 1 || hits[0].Filename != "active.md" {
		t.Errorf("only active.md expected; got %v", hits)
	}
}

func TestFTS_IncludeArchive(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "active.md", "ARNnonce")
	_ = s.Write("ns", "archive/old.md", "ARNnonce")

	hits, _ := s.Search(SearchOptions{Namespace: "ns", Query: "ARNnonce", IncludeArchive: true})
	if len(hits) != 2 {
		t.Errorf("expected 2 with include_archive=true; got %v", hits)
	}
}

// ── isolation, ordering, limits, errors ─────────────────────────────────────

func TestFTS_NamespaceIsolation(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("alpha", "x.md", "SHAREDnonce")
	_ = s.Write("beta", "y.md", "SHAREDnonce")
	hitsA := searchAll(t, s, "alpha", "SHAREDnonce")
	if len(hitsA) != 1 || hitsA[0].Filename != "x.md" {
		t.Errorf("alpha should only see its own; got %v", hitsA)
	}
	hitsB := searchAll(t, s, "beta", "SHAREDnonce")
	if len(hitsB) != 1 || hitsB[0].Filename != "y.md" {
		t.Errorf("beta should only see its own; got %v", hitsB)
	}
}

func TestSearchTool_EmptyNamespaceErrors(t *testing.T) {
	s := setupStore(t)
	_, isErr := runTool(s, "search", map[string]any{"namespace": "", "query": "x"})
	if !isErr {
		t.Error("empty namespace should error")
	}
}

func TestSearchTool_StarNamespaceErrors(t *testing.T) {
	s := setupStore(t)
	_, isErr := runTool(s, "search", map[string]any{"namespace": "*", "query": "x"})
	if !isErr {
		t.Error("namespace=* should error")
	}
}

func TestSearchTool_InvalidOrderErrors(t *testing.T) {
	s := setupStore(t)
	_, isErr := runTool(s, "search", map[string]any{"namespace": "ns", "query": "x", "order": "invalid"})
	if !isErr {
		t.Error("invalid order should error")
	}
}

func TestSearchTool_LimitCappedAt100(t *testing.T) {
	s := setupStore(t)
	for i := 0; i < 120; i++ {
		_ = s.Write("ns", "f"+itoa(i)+".md", "LIMnonce")
	}
	hits, err := s.Search(SearchOptions{Namespace: "ns", Query: "LIMnonce", Limit: 9999})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 100 {
		t.Errorf("limit should cap at 100; got %d", len(hits))
	}
}

func TestSearchTool_ZeroResultsIsEmptyArray(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "real content")
	res, isErr := runTool(s, "search", map[string]any{"namespace": "ns", "query": "nothingnowhere"})
	if isErr {
		t.Fatal("zero results should not be an error")
	}
	m := res.(map[string]any)
	hits, _ := m["results"].([]SearchHit)
	if hits == nil {
		t.Error("results should be empty slice, not nil")
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 results, got %d", len(hits))
	}
}

func TestSearchTool_OrderRecency(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "old.md", "RECnonce")
	_ = s.Write("ns", "new.md", "RECnonce")
	hits, _ := s.Search(SearchOptions{Namespace: "ns", Query: "RECnonce", Order: "recency"})
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits; got %v", hits)
	}
	if hits[0].UpdatedAt < hits[1].UpdatedAt {
		t.Errorf("recency order: newest first expected; got %v", hits)
	}
}

func TestSearchTool_PhraseMatch(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "adjacent.md", "alphaQ betaQ is adjacent")
	_ = s.Write("ns", "split.md", "alphaQ is here. betaQ is far.")

	loose, _ := s.Search(SearchOptions{Namespace: "ns", Query: "alphaQ betaQ"})
	if len(loose) != 2 {
		t.Errorf("loose query expected 2 hits; got %v", loose)
	}
	phrase, _ := s.Search(SearchOptions{Namespace: "ns", Query: `"alphaQ betaQ"`})
	if len(phrase) != 1 || phrase[0].Filename != "adjacent.md" {
		t.Errorf("phrase query expected only adjacent.md; got %v", phrase)
	}
}

func TestSearchTool_SnippetFormat(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "before the FFFnonce after")
	hits := searchAll(t, s, "ns", "FFFnonce")
	if len(hits) != 1 {
		t.Fatalf("expected one hit; got %v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "<FFFnonce>") {
		t.Errorf("snippet should mark the term with <>; got %q", hits[0].Snippet)
	}
}

// itoa avoids fmt for the limit test (just compactness).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
