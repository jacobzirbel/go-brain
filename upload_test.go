package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── folder upload: check endpoint ───────────────────────────────────────────

// uploadMux stands up the UI routes with a real session, like the export tests.
func uploadMux(t *testing.T, s *SQLiteStore) (*http.ServeMux, *http.Cookie) {
	t.Helper()
	srv := newServer(config{}, s)
	tok := "tok"
	// Tests may post more than once against one store; reuse the session.
	if ok, _ := s.HasSession(tok); !ok {
		if err := s.CreateSession(tok); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	srv.registerUIRoutes(mux)
	return mux, &http.Cookie{Name: sessionCookieName, Value: tok}
}

type checkFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type checkResponse struct {
	OK           bool             `json:"ok"`
	Create       int              `json:"create"`
	NewNamespace bool             `json:"new_namespace"`
	Conflicts    []uploadConflict `json:"conflicts"`
	Duplicates   [][]string       `json:"duplicates"`
	Oversized    []string         `json:"oversized"`
	Invalid      []string         `json:"invalid"`
}

func postCheck(t *testing.T, s *SQLiteStore, ns string, files ...checkFile) checkResponse {
	t.Helper()
	mux, cookie := uploadMux(t, s)
	body, _ := json.Marshal(map[string]any{"namespace": ns, "files": files})
	req := httptest.NewRequest("POST", "/ui/upload/check", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("check: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out checkResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("check: bad json %q: %v", rr.Body.String(), err)
	}
	return out
}

func TestUploadCheck_CleanBatch(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "other.md", "x")

	got := postCheck(t, s, "ns",
		checkFile{"notes/a.md", 10},
		checkFile{"notes/sub/b.md", 20},
	)
	if !got.OK || got.Create != 2 || got.NewNamespace {
		t.Fatalf("want ok, 2 creates, existing namespace; got %+v", got)
	}
	if len(got.Conflicts)+len(got.Duplicates)+len(got.Oversized)+len(got.Invalid) != 0 {
		t.Fatalf("want no blockers, got %+v", got)
	}
}

func TestUploadCheck_ExactAndCaseOnlyConflicts(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "notes/a.md", "x")
	_ = s.Write("ns", "notes/todo.md", "x")

	got := postCheck(t, s, "ns",
		checkFile{"notes/a.md", 1},
		checkFile{"Notes/Todo.md", 1},
		checkFile{"notes/new.md", 1},
	)
	if got.OK {
		t.Fatal("conflicts must block")
	}
	want := []uploadConflict{
		{Path: "notes/a.md", Existing: "notes/a.md"},
		{Path: "Notes/Todo.md", Existing: "notes/todo.md"},
	}
	if len(got.Conflicts) != 2 || got.Conflicts[0] != want[0] || got.Conflicts[1] != want[1] {
		t.Fatalf("conflicts: want %+v, got %+v", want, got.Conflicts)
	}
}

func TestUploadCheck_DuplicatesInBatchIgnoringCase(t *testing.T) {
	s := setupStore(t)
	got := postCheck(t, s, "ns",
		checkFile{"notes/A.md", 1},
		checkFile{"notes/a.md", 1},
		checkFile{"notes/b.md", 1},
	)
	if got.OK {
		t.Fatal("duplicates must block")
	}
	if len(got.Duplicates) != 1 || len(got.Duplicates[0]) != 2 ||
		got.Duplicates[0][0] != "notes/A.md" || got.Duplicates[0][1] != "notes/a.md" {
		t.Fatalf("duplicates: got %+v", got.Duplicates)
	}
}

func TestUploadCheck_OversizedAndInvalidReportedTogether(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "x/taken.md", "x")
	got := postCheck(t, s, "ns",
		checkFile{"x/big.md", 1<<20 + 1},
		checkFile{"x/ok.md", 1 << 20},
		checkFile{"x/pic.png", 1},
		checkFile{"x/.hidden/h.md", 1},
		checkFile{"x/../escape.md", 1},
		checkFile{"x/taken.md", 1},
	)
	if got.OK {
		t.Fatal("blockers must block")
	}
	if len(got.Oversized) != 1 || got.Oversized[0] != "x/big.md" {
		t.Fatalf("oversized: got %+v", got.Oversized)
	}
	if len(got.Invalid) != 3 {
		t.Fatalf("invalid: want png, dot-path and .. rejected, got %+v", got.Invalid)
	}
	if len(got.Conflicts) != 1 {
		t.Fatalf("conflict should be reported alongside the rest, got %+v", got.Conflicts)
	}
}

func TestUploadCheck_NewNamespaceFlaggedAndNothingWritten(t *testing.T) {
	s := setupStore(t)
	got := postCheck(t, s, "Fresh", checkFile{"a.md", 1})
	if !got.OK || !got.NewNamespace {
		t.Fatalf("want ok + new namespace, got %+v", got)
	}
	nss, _ := s.ListNamespacesIncludingClosed()
	for _, n := range nss {
		if n == "fresh" || n == "Fresh" {
			t.Fatal("check must not create the namespace")
		}
	}
	if _, _, err := s.Read("fresh", "a.md"); err != ErrNotFound {
		t.Fatalf("check must not write files, got err=%v", err)
	}
}

func TestUploadCheck_ArchivedAndDeletedPathsAccepted(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "live")
	got := postCheck(t, s, "ns",
		checkFile{"archived/a.md", 1},
		checkFile{"deleted/b.md", 1},
	)
	if !got.OK || got.Create != 2 {
		t.Fatalf("archived/deleted paths should upload as-is, got %+v", got)
	}
}

// ── folder upload: upload endpoint ──────────────────────────────────────────

type upFile struct{ path, content string }

func postUpload(t *testing.T, s *SQLiteStore, fields map[string]string, files ...upFile) *httptest.ResponseRecorder {
	t.Helper()
	mux, cookie := uploadMux(t, s)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for _, f := range files {
		_ = mw.WriteField("path", f.path)
		fw, _ := mw.CreateFormFile("file", f.path)
		_, _ = fw.Write([]byte(f.content))
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/ui/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestUpload_CreatesFilesWithStructureCommentAndPending(t *testing.T) {
	s := setupStore(t)
	rr := postUpload(t, s, map[string]string{"namespace": "ns", "comment": "import notes"},
		upFile{"notes/a.md", "alpha"},
		upFile{"notes/sub/b.md", "beta"},
	)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d: %s", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "/ui/?ns=ns&open=notes" {
		t.Fatalf("landing: got %q", loc)
	}
	for _, f := range []upFile{{"notes/a.md", "alpha"}, {"notes/sub/b.md", "beta"}} {
		content, newVal, _, err := s.ReadEntry("ns", f.path)
		if err != nil {
			t.Fatalf("%s: %v", f.path, err)
		}
		if !newVal.Valid || newVal.String != f.content || content != "" {
			t.Fatalf("%s: want pending %q, got content=%q new=%+v", f.path, f.content, content, newVal)
		}
		cs, _ := s.ListComments("ns", f.path, false, 20, 0)
		if len(cs) != 1 || cs[0].Content != "import notes" {
			t.Fatalf("%s: want the batch comment, got %+v", f.path, cs)
		}
	}
}

func TestUpload_AlreadyReviewedLandsReviewed(t *testing.T) {
	s := setupStore(t)
	rr := postUpload(t, s, map[string]string{"namespace": "ns", "comment": "c", "already_reviewed": "1"},
		upFile{"a.md", "alpha"},
	)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d: %s", rr.Code, rr.Body.String())
	}
	content, newVal, _, err := s.ReadEntry("ns", "a.md")
	if err != nil || newVal.Valid || content != "alpha" {
		t.Fatalf("want reviewed content, got content=%q new=%+v err=%v", content, newVal, err)
	}
	if n, _ := s.NamespacePendingCount("ns"); n != 0 {
		t.Fatalf("want nothing pending, got %d", n)
	}
	if open, _ := s.ListComments("ns", "a.md", false, 20, 0); len(open) != 0 {
		t.Fatalf("comment should be reviewed, got %+v", open)
	}
	if all, _ := s.ListComments("ns", "a.md", true, 20, 0); len(all) != 1 {
		t.Fatalf("comment should still exist, got %+v", all)
	}
}

func TestUpload_BlankCommentAddsNone(t *testing.T) {
	s := setupStore(t)
	postUpload(t, s, map[string]string{"namespace": "ns"}, upFile{"a.md", "x"})
	if cs, _ := s.ListComments("ns", "a.md", true, 20, 0); len(cs) != 0 {
		t.Fatalf("want no comment, got %+v", cs)
	}
}

func TestUpload_CreatesMissingNamespaceAndIsSearchable(t *testing.T) {
	s := setupStore(t)
	rr := postUpload(t, s, map[string]string{"namespace": "Brand-New"}, upFile{"a.md", "zebra crossing"})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d: %s", rr.Code, rr.Body.String())
	}
	nss, _ := s.ListNamespaces()
	found := false
	for _, n := range nss {
		found = found || n == "brand-new"
	}
	if !found {
		t.Fatalf("namespace not created: %v", nss)
	}
	hits, err := s.Search(SearchOptions{Namespace: "brand-new", Query: "zebra"})
	if err != nil || len(hits) != 1 || hits[0].Filename != "a.md" {
		t.Fatalf("want uploaded file in search, got %+v err=%v", hits, err)
	}
}

func TestUpload_ClosedNamespaceAccepted(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "x")
	_ = s.SetNamespaceClosed("ns", true)
	rr := postUpload(t, s, map[string]string{"namespace": "ns"}, upFile{"b.md", "y"})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("want 303, got %d: %s", rr.Code, rr.Body.String())
	}
	if c, _, err := s.Read("ns", "b.md"); err != nil || c != "y" {
		t.Fatalf("want b.md written, got %q err=%v", c, err)
	}
}

func TestUpload_OneConflictWritesNothing(t *testing.T) {
	s := setupStore(t)
	// Simulates a file created between the check and the upload.
	_ = s.Write("ns", "notes/Taken.md", "original")
	rr := postUpload(t, s, map[string]string{"namespace": "ns", "comment": "c"},
		upFile{"notes/free.md", "new"},
		upFile{"notes/taken.md", "clobber"},
	)
	if rr.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rr.Code, rr.Body.String())
	}
	var got checkResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || len(got.Conflicts) != 1 {
		t.Fatalf("want the conflict reported, got %s", rr.Body.String())
	}
	if _, _, err := s.Read("ns", "notes/free.md"); err != ErrNotFound {
		t.Fatalf("nothing should be written, got err=%v", err)
	}
	if c, _, _ := s.Read("ns", "notes/Taken.md"); c != "original" {
		t.Fatalf("existing file touched: %q", c)
	}
	if cs, _ := s.ListComments("ns", "", true, 20, 0); len(cs) != 0 {
		t.Fatalf("no comments should be written, got %+v", cs)
	}
}

func TestUpload_ServerRejectsNonMarkdownAndOversized(t *testing.T) {
	s := setupStore(t)
	for name, f := range map[string]upFile{
		"non-md":    {"pic.png", "x"},
		"oversized": {"big.md", strings.Repeat("x", 1<<20+1)},
	} {
		rr := postUpload(t, s, map[string]string{"namespace": "ns"}, upFile{"ok.md", "fine"}, f)
		if rr.Code != http.StatusConflict {
			t.Fatalf("%s: want 409, got %d: %s", name, rr.Code, rr.Body.String())
		}
		if _, _, err := s.Read("ns", "ok.md"); err != ErrNotFound {
			t.Fatalf("%s: nothing should be written, got err=%v", name, err)
		}
	}
}

func TestUpload_LooseFilesLandAtNamespaceRoot(t *testing.T) {
	s := setupStore(t)
	rr := postUpload(t, s, map[string]string{"namespace": "ns"}, upFile{"a.md", "x"}, upFile{"b.md", "y"})
	if loc := rr.Header().Get("Location"); loc != "/ui/?ns=ns" {
		t.Fatalf("landing: got %q", loc)
	}
}

func TestManualEntry_LowercasesNamespace(t *testing.T) {
	s := setupStore(t)
	mux, cookie := uploadMux(t, s)
	form := "namespace=MixedCase&filename=a.md&content=x"
	req := httptest.NewRequest("POST", "/ui/new", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if c, _, err := s.Read("mixedcase", "a.md"); err != nil || c != "x" {
		t.Fatalf("want file in lowercased namespace, got %q err=%v (status %d)", c, err, rr.Code)
	}
}
