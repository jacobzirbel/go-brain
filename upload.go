package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Folder upload: JZ drops a folder on /ui/new and every .md file in it lands in
// a namespace at its relative path. Uploads only ever create — any path that
// would overwrite an existing file (compared case-insensitively) blocks the
// whole batch, and the write is a single transaction so it's all-or-nothing.

// maxUploadFileBytes caps each uploaded .md file. Enforced in the browser too,
// but the server is the one that counts.
const maxUploadFileBytes = 1 << 20

// UploadSpec is one file as the check step sees it: a path and a size, no body.
type UploadSpec struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// UploadDoc is one file as the upload step writes it.
type UploadDoc struct {
	Path    string
	Content string
}

type uploadConflict struct {
	Path     string `json:"path"`     // the uploaded path
	Existing string `json:"existing"` // the existing filename it collides with
}

// UploadPlan is the pre-flight summary. Any non-empty blocker list means the
// batch can't be uploaded.
type UploadPlan struct {
	Create       int              `json:"create"`
	NewNamespace bool             `json:"new_namespace"`
	Conflicts    []uploadConflict `json:"conflicts"`
	Duplicates   [][]string       `json:"duplicates"` // groups of paths equal ignoring case
	Oversized    []string         `json:"oversized"`
	Invalid      []string         `json:"invalid"` // not a plain relative .md path
}

func (p UploadPlan) OK() bool {
	return p.Create > 0 && len(p.Conflicts) == 0 && len(p.Duplicates) == 0 &&
		len(p.Oversized) == 0 && len(p.Invalid) == 0
}

// UploadBlockedError is returned by Upload when the plan has blockers; it
// carries the full plan so callers can report every problem at once.
type UploadBlockedError struct{ Plan UploadPlan }

func (e *UploadBlockedError) Error() string { return "upload blocked" }

// validUploadPath accepts a relative, slash-separated path to a .md file with
// no empty, `.`/`..`, or dot-prefixed segments.
func validUploadPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || strings.HasPrefix(seg, ".") {
			return false
		}
	}
	return strings.HasSuffix(strings.ToLower(p), ".md")
}

type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// planUpload validates a batch against the namespace as seen through q. It
// writes nothing, so the check step and the upload transaction share it.
func planUpload(q querier, namespace string, specs []UploadSpec) (UploadPlan, error) {
	plan := UploadPlan{
		Conflicts:  []uploadConflict{},
		Duplicates: [][]string{},
		Oversized:  []string{},
		Invalid:    []string{},
	}

	var one int
	err := q.QueryRow(`SELECT 1 FROM namespaces WHERE name=?`, namespace).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		plan.NewNamespace = true
	case err != nil:
		return plan, err
	}

	existing := map[string]string{}
	rows, err := q.Query(`SELECT filename FROM entries WHERE namespace=?`, namespace)
	if err != nil {
		return plan, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return plan, err
		}
		existing[strings.ToLower(name)] = name
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return plan, err
	}

	groups := map[string][]string{}
	var order []string
	for _, f := range specs {
		if !validUploadPath(f.Path) {
			plan.Invalid = append(plan.Invalid, f.Path)
			continue
		}
		if f.Size > maxUploadFileBytes {
			plan.Oversized = append(plan.Oversized, f.Path)
		}
		key := strings.ToLower(f.Path)
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], f.Path)
	}
	for _, key := range order {
		paths := groups[key]
		if len(paths) > 1 {
			plan.Duplicates = append(plan.Duplicates, paths)
		}
		for _, p := range paths {
			if name, ok := existing[key]; ok {
				plan.Conflicts = append(plan.Conflicts, uploadConflict{Path: p, Existing: name})
			}
		}
	}
	plan.Create = len(specs) - len(plan.Invalid)
	return plan, nil
}

// PlanUpload is the read-only pre-flight check for a batch.
func (s *SQLiteStore) PlanUpload(namespace string, specs []UploadSpec) (UploadPlan, error) {
	return planUpload(s.db, namespace, specs)
}

// Upload creates every file in one transaction: re-plans inside the tx (so a
// file created since the check still blocks), creates the namespace if
// missing, attaches the comment to each file, and — when reviewed is set —
// lands each file already reviewed, as manual entry's checkbox does.
func (s *SQLiteStore) Upload(namespace string, docs []UploadDoc, comment string, reviewed bool) error {
	specs := make([]UploadSpec, len(docs))
	for i, d := range docs {
		specs[i] = UploadSpec{Path: d.Path, Size: int64(len(d.Content))}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	plan, err := planUpload(tx, namespace, specs)
	if err != nil {
		return err
	}
	if !plan.OK() {
		return &UploadBlockedError{Plan: plan}
	}
	if err := upsertNamespace(tx, namespace); err != nil {
		return err
	}
	for _, d := range docs {
		content := normalizeLineEndings(d.Content)
		var rowid int64
		if reviewed {
			err = tx.QueryRow(`
				INSERT INTO entries (namespace, filename, content, new, updated_at)
				VALUES (?, ?, ?, NULL, datetime('now'))
				RETURNING rowid
			`, namespace, d.Path, content).Scan(&rowid)
		} else {
			err = tx.QueryRow(`
				INSERT INTO entries (namespace, filename, new, updated_at)
				VALUES (?, ?, ?, datetime('now'))
				RETURNING rowid
			`, namespace, d.Path, content).Scan(&rowid)
		}
		if err != nil {
			return err
		}
		if err := upsertFTS(tx, rowid, namespace, d.Path, content); err != nil {
			return err
		}
		if comment != "" {
			if _, err := tx.Exec(
				`INSERT INTO comments (namespace, filename, content, reviewed) VALUES (?, ?, ?, ?)`,
				namespace, d.Path, comment, reviewed,
			); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// uiNamespace normalizes a UI namespace field (upload and manual entry).
// Namespace names are case-insensitive, so fold to lowercase as the MCP tools do.
func uiNamespace(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

type uploadCheckResponse struct {
	OK bool `json:"ok"`
	UploadPlan
}

// handleUIUploadCheck takes {namespace, files: [{path, size}]} — paths only, no
// contents — and returns the plan.
func (s *server) handleUIUploadCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Namespace string       `json:"namespace"`
		Files     []UploadSpec `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	ns := uiNamespace(req.Namespace)
	if ns == "" {
		http.Error(w, "namespace is required", http.StatusBadRequest)
		return
	}
	plan, err := s.store.PlanUpload(ns, req.Files)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, uploadCheckResponse{OK: plan.OK(), UploadPlan: plan})
}

// handleUIUpload takes a multipart form: namespace, comment, already_reviewed,
// and parallel `path` values / `file` parts. Blocked batches get 409 with the
// plan as JSON; success redirects to the namespace's tree at the uploaded
// root folder.
func (s *server) handleUIUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	ns := uiNamespace(r.FormValue("namespace"))
	comment := strings.TrimSpace(r.FormValue("comment"))
	reviewed := r.FormValue("already_reviewed") == "1"
	paths := r.MultipartForm.Value["path"]
	parts := r.MultipartForm.File["file"]
	if ns == "" || len(paths) == 0 || len(paths) != len(parts) {
		http.Error(w, "namespace and matching path/file pairs are required", http.StatusBadRequest)
		return
	}
	docs := make([]UploadDoc, len(paths))
	for i, fh := range parts {
		f, err := fh.Open()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Read one byte past the cap so an oversized file is still seen as
		// oversized by the plan without buffering all of it.
		b, err := io.ReadAll(io.LimitReader(f, maxUploadFileBytes+1))
		f.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		docs[i] = UploadDoc{Path: paths[i], Content: string(b)}
	}
	err := s.store.Upload(ns, docs, comment, reviewed)
	var blocked *UploadBlockedError
	if errors.As(err, &blocked) {
		writeJSON(w, http.StatusConflict, uploadCheckResponse{OK: false, UploadPlan: blocked.Plan})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, uploadLanding(ns, paths), http.StatusSeeOther)
}

// uploadLanding is the home tree with the namespace selected, opened at the
// uploaded root folder when every file shares one.
func uploadLanding(ns string, paths []string) string {
	v := url.Values{"ns": {ns}}
	roots := map[string]bool{}
	for _, p := range paths {
		root, _, found := strings.Cut(p, "/")
		if !found {
			root = ""
		}
		roots[root] = true
	}
	if len(roots) == 1 {
		for root := range roots {
			if root != "" {
				v.Set("open", root)
			}
		}
	}
	return "/ui/?" + v.Encode()
}
