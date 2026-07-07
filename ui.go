package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

func renderMarkdown(src string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

const sessionCookieName = "gobrain_session"

func (s *server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(s.cfg.baseURL, "https://"),
		MaxAge:   60 * 60 * 24 * 30,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func (s *server) currentSession(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	ok, err := s.store.HasSession(c.Value)
	if err != nil || !ok {
		return "", false
	}
	return c.Value, true
}

func (s *server) uiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentSession(r); !ok {
			http.Redirect(w, r, "/ui/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

var uiFuncs = template.FuncMap{
	"tokens": func(n int) string { return formatThousands(n / 4) },
	"nodeCtx": func(ns string, n any) map[string]any {
		return map[string]any{"NS": ns, "Node": n}
	},
	"isArchiveFolder": func(name string) bool {
		switch name {
		case "archive", "archived", "deleted":
			return true
		}
		return false
	},
	"snippet": func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.ReplaceAll(s, "\n", " ")
		if len(s) > 140 {
			return s[:140] + "…"
		}
		return s
	},
	"dict": func(pairs ...any) map[string]any {
		m := make(map[string]any, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			k, _ := pairs[i].(string)
			m[k] = pairs[i+1]
		}
		return m
	},
}

func formatThousands(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

var uiTemplates = template.Must(template.New("ui").Funcs(uiFuncs).Parse(uiTemplateSrc))

func renderUI(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// chromeData is the shared header context — currently just the global
// inbox count. Every authed view computes it and threads it through the template.
type chromeData struct {
	InboxCount int
}

func (s *server) chrome() chromeData {
	n, _ := s.store.GlobalPendingCount()
	return chromeData{InboxCount: n}
}

func (s *server) handleUILoginGet(w http.ResponseWriter, r *http.Request) {
	renderUI(w, "login", map[string]any{"Error": r.URL.Query().Get("error")})
}

func (s *server) handleUILoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	pw := r.FormValue("password")
	if s.cfg.frontendPassword == "" || pw != s.cfg.frontendPassword {
		http.Redirect(w, r, "/ui/login?error=1", http.StatusFound)
		return
	}
	tok := randHex(32)
	if err := s.store.CreateSession(tok); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.setSessionCookie(w, tok)
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

func (s *server) handleUILogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		if err := s.store.DeleteSession(c.Value); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/ui/login", http.StatusFound)
}

type treeNode struct {
	Name       string
	FullPath   string
	IsFile     bool
	File       FileEntry
	Children   []*treeNode
	Size       int // bytes, excluding archive subtrees
	Pending    int // count of pending files in subtree (or 1 if leaf has pending)
	HasPending bool
}

func buildTree(files []FileEntry) *treeNode {
	root := &treeNode{}
	for _, f := range files {
		parts := splitPath(f.Filename)
		node := root
		for i, p := range parts {
			child := findChild(node, p)
			if child == nil {
				child = &treeNode{
					Name:     p,
					FullPath: strings.Join(parts[:i+1], "/"),
				}
				node.Children = append(node.Children, child)
			}
			node = child
		}
		node.IsFile = true
		node.File = f
		node.HasPending = f.HasPending
	}
	rollupAndSort(root)
	return root
}

func splitPath(s string) []string {
	parts := strings.Split(s, "/")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{s}
	}
	return out
}

func findChild(n *treeNode, name string) *treeNode {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// rollupAndSort computes Size + Pending bottom-up. Archive subtrees (folders
// named "archive", "archived", "deleted" at any level) are excluded from the
// Size and Pending totals of their ancestors so the headline numbers reflect
// active content only — matches Phase 11.1 tree-tool behavior.
func rollupAndSort(n *treeNode) {
	if n.IsFile {
		n.Size = n.File.Size
		if n.HasPending {
			n.Pending = 1
		}
	} else {
		n.Size = 0
		n.Pending = 0
		for _, c := range n.Children {
			rollupAndSort(c)
			if isArchiveName(c.Name) {
				continue
			}
			n.Size += c.Size
			n.Pending += c.Pending
		}
	}
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsFile != b.IsFile {
			return !a.IsFile
		}
		return a.Name < b.Name
	})
}

func isArchiveName(s string) bool {
	switch s {
	case "archive", "archived", "deleted":
		return true
	}
	return false
}

type namespaceGroup struct {
	Namespace    string
	Tree         *treeNode
	TotalSize    int
	PendingCount int
}

func (s *server) handleUIHome(w http.ResponseWriter, r *http.Request) {
	namespaces, err := s.store.ListNamespaces()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	groups := make([]namespaceGroup, 0, len(namespaces))
	grandTotal := 0
	for _, ns := range namespaces {
		files, err := s.store.List(ns)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tree := buildTree(files)
		grandTotal += tree.Size
		groups = append(groups, namespaceGroup{
			Namespace:    ns,
			Tree:         tree,
			TotalSize:    tree.Size,
			PendingCount: tree.Pending,
		})
	}
	// Pin `global` to the top regardless of alphabetical order; everything
	// else stays in the order ListNamespaces returned (alphabetical).
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].Namespace == "global" && groups[j].Namespace != "global"
	})
	renderUI(w, "home", map[string]any{
		"Groups":     groups,
		"GrandTotal": grandTotal,
		"Chrome":     s.chrome(),
	})
}

func (s *server) handleUIFile(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	content, newVal, updatedAt, err := s.store.ReadEntry(ns, name)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hasPending := newVal.Valid
	displayed := content
	if hasPending {
		displayed = newVal.String
	}
	fromNS, fromName, hasMovedFrom, err := s.store.MovedFrom(ns, name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Namespace":          ns,
		"Filename":           name,
		"Content":            content,
		"New":                "",
		"Displayed":          displayed,
		"UpdatedAt":          updatedAt,
		"Size":               len(displayed),
		"HasPending":         hasPending,
		"HasMovedFrom":       hasMovedFrom,
		"MovedFromNamespace": fromNS,
		"MovedFromFilename":  fromName,
		"IsArchived":         isArchivePath(name),
		"Chrome":             s.chrome(),
	}
	if hasPending {
		data["New"] = newVal.String
	}
	// Default thread: unreviewed only, 20 most recent.
	// `?history=1` flips the toggle on so reviewed comments render too.
	includeHistory := r.URL.Query().Get("history") == "1"
	comments, err := s.store.ListComments(ns, name, includeHistory, 20, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Comments"] = comments
	data["CommentsHaveMore"] = len(comments) == 20
	data["IncludeHistory"] = includeHistory

	if strings.HasSuffix(strings.ToLower(name), ".md") && !hasPending {
		rendered, err := renderMarkdown(displayed)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data["Rendered"] = rendered
	}
	renderUI(w, "file", data)
}

func (s *server) handleUIEditGet(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	content, _, err := s.store.Read(ns, name)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	renderUI(w, "edit", map[string]any{
		"Namespace": ns,
		"Filename":  name,
		"Content":   content,
		"Chrome":    s.chrome(),
	})
}

func (s *server) handleUIEditPost(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	content := r.FormValue("content")
	comment := strings.TrimSpace(r.FormValue("comment"))
	alreadyReviewed := r.FormValue("already_reviewed") == "1"

	if err := s.store.Write(ns, name, content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if comment != "" {
		if err := s.store.InsertComment(ns, name, comment); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if alreadyReviewed {
		// The move (if any) already took effect; approving just blesses the
		// current state, so the file stays where it is.
		if err := s.store.Review(ns, name); err != nil && !errors.Is(err, ErrNoPending) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/ui/file?"+url.Values{"ns": {ns}, "name": {name}}.Encode(), http.StatusFound)
}

func (s *server) handleUIArchive(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	dst := "archived/" + name
	if err := s.store.Move(ns, name, ns, dst); err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrDestinationExists) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

// The namespaces management tab owns a namespace's lifecycle: its closed bit and
// its tags. All of it is UI-only — there is no MCP equivalent for closing or
// tagging. Every action here redirects back to the tab.

func (s *server) handleUINamespaces(w http.ResponseWriter, r *http.Request) {
	records, err := s.store.NamespaceRecords()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	renderUI(w, "namespaces", map[string]any{
		"Records": records,
		"Chrome":  s.chrome(),
	})
}

func (s *server) handleUINamespaceClose(w http.ResponseWriter, r *http.Request) {
	s.setNamespaceClosed(w, r, true)
}

func (s *server) handleUINamespaceReopen(w http.ResponseWriter, r *http.Request) {
	s.setNamespaceClosed(w, r, false)
}

func (s *server) setNamespaceClosed(w http.ResponseWriter, r *http.Request, closed bool) {
	ns := r.URL.Query().Get("ns")
	if err := s.store.SetNamespaceClosed(ns, closed); err != nil && !errors.Is(err, ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/namespaces", http.StatusFound)
}

func (s *server) handleUINamespaceTagAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	ns := r.URL.Query().Get("ns")
	tag := strings.TrimSpace(r.FormValue("tag"))
	if tag != "" {
		if err := s.store.AddNamespaceTag(ns, tag); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/ui/namespaces", http.StatusFound)
}

func (s *server) handleUINamespaceTagRemove(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	tag := r.URL.Query().Get("tag")
	if err := s.store.RemoveNamespaceTag(ns, tag); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/namespaces", http.StatusFound)
}

func (s *server) handleUIDelete(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	dst := "deleted/" + name
	if err := s.store.Move(ns, name, ns, dst); err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrDestinationExists) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

func (s *server) handleUIHardDelete(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	if err := s.store.Delete(ns, name); err != nil && !errors.Is(err, ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

func (s *server) handleUINewGet(w http.ResponseWriter, r *http.Request) {
	renderUI(w, "new", map[string]any{
		"Namespace": r.URL.Query().Get("ns"),
		"Chrome":    s.chrome(),
	})
}

func (s *server) handleUINewPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	ns := strings.TrimSpace(r.FormValue("namespace"))
	name := strings.TrimSpace(r.FormValue("filename"))
	content := r.FormValue("content")
	comment := strings.TrimSpace(r.FormValue("comment"))
	alreadyReviewed := r.FormValue("already_reviewed") == "1"
	if ns == "" || name == "" {
		http.Error(w, "namespace and filename are required", http.StatusBadRequest)
		return
	}
	if err := s.store.Write(ns, name, content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if comment != "" {
		if err := s.store.InsertComment(ns, name, comment); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if alreadyReviewed {
		if err := s.store.Review(ns, name); err != nil && !errors.Is(err, ErrNoPending) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/ui/file?"+url.Values{"ns": {ns}, "name": {name}}.Encode(), http.StatusFound)
}

func (s *server) handleUIReview(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	// The move (if any) already took effect; approving just blesses the current
	// state, so the file stays where it is.
	if err := s.store.Review(ns, name); err != nil && !errors.Is(err, ErrNoPending) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.URL.Query().Get("return") == "inbox" {
		http.Redirect(w, r, "/ui/inbox", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/ui/file?"+url.Values{"ns": {ns}, "name": {name}}.Encode(), http.StatusFound)
}

// handleUIReviewFolder approves every pending file under a namespace/folder
// prefix in one shot. An empty prefix approves the whole namespace. Matching
// mirrors the inbox tree: the prefix is a folder FullPath, so a file belongs to
// it when its path equals the prefix or sits beneath `prefix/`.
func (s *server) handleUIReviewFolder(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	prefix := r.URL.Query().Get("prefix")
	files, err := s.store.PendingFiles()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, f := range files {
		if f.Namespace != ns {
			continue
		}
		if prefix != "" && f.Filename != prefix && !strings.HasPrefix(f.Filename, prefix+"/") {
			continue
		}
		if err := s.store.Review(ns, f.Filename); err != nil && !errors.Is(err, ErrNoPending) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/ui/inbox", http.StatusFound)
}

func (s *server) handleUIReject(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	// Rejecting an unreviewed move sends the file back to its origin — capture
	// it first so the redirect follows the file there.
	fromNS, fromName, hasMove, _ := s.store.MovedFrom(ns, name)
	if err := s.store.Reject(ns, name); err != nil && !errors.Is(err, ErrNoPending) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if hasMove {
		ns, name = fromNS, fromName
	}
	http.Redirect(w, r, "/ui/file?"+url.Values{"ns": {ns}, "name": {name}}.Encode(), http.StatusFound)
}

func (s *server) handleUIComments(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("file")
	includeReviewed := r.URL.Query().Get("include_reviewed") == "1"
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	comments, err := s.store.ListComments(ns, name, includeReviewed, 20, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	renderUI(w, "comments_fragment", map[string]any{
		"Comments":        comments,
		"NextOffset":      offset + len(comments),
		"HaveMore":        len(comments) == 20,
		"Namespace":       ns,
		"Filename":        name,
		"IncludeReviewed": includeReviewed,
	})
}

// inboxNode is the inbox counterpart of treeNode: a folder/file tree built from
// pending files. It carries the full PendingFile on leaves so the inbox view can
// render the moved-from tag, comment snippet, and Approve button per file.
type inboxNode struct {
	Name     string
	FullPath string // path within the namespace
	IsFile   bool
	File     PendingFile
	Children []*inboxNode
	Pending  int // count of pending file leaves in this subtree
}

// inboxGroup wraps one namespace's pending tree, like the home view's
// namespaceGroup. `global` is pinned to the top; the rest are alphabetical.
type inboxGroup struct {
	Namespace string
	Tree      *inboxNode
	Pending   int
}

func findInboxChild(n *inboxNode, name string) *inboxNode {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// buildInboxGroups turns the flat pending list into one folder tree per
// namespace. Folders sort before files, then alphabetically; single-child folder
// chains are collapsed (VS Code "compact folders") so deep paths stay shallow.
func buildInboxGroups(files []PendingFile) []inboxGroup {
	roots := map[string]*inboxNode{}
	for _, f := range files {
		root := roots[f.Namespace]
		if root == nil {
			root = &inboxNode{}
			roots[f.Namespace] = root
		}
		parts := splitPath(f.Filename)
		node := root
		for i, p := range parts {
			child := findInboxChild(node, p)
			if child == nil {
				child = &inboxNode{Name: p, FullPath: strings.Join(parts[:i+1], "/")}
				node.Children = append(node.Children, child)
			}
			node = child
		}
		node.IsFile = true
		node.File = f
	}
	groups := make([]inboxGroup, 0, len(roots))
	for ns, root := range roots {
		rollupInbox(root)
		compactInbox(root)
		groups = append(groups, inboxGroup{Namespace: ns, Tree: root, Pending: root.Pending})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Namespace == "global" {
			return groups[j].Namespace != "global"
		}
		if groups[j].Namespace == "global" {
			return false
		}
		return groups[i].Namespace < groups[j].Namespace
	})
	return groups
}

// rollupInbox computes the pending leaf count bottom-up and sorts each level
// folders-first then alphabetically.
func rollupInbox(n *inboxNode) {
	if n.IsFile {
		n.Pending = 1
		return
	}
	n.Pending = 0
	for _, c := range n.Children {
		rollupInbox(c)
		n.Pending += c.Pending
	}
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsFile != b.IsFile {
			return !a.IsFile
		}
		return a.Name < b.Name
	})
}

// compactInbox merges any folder that has a single folder child into that child,
// joining their names with "/" (e.g. `a` → `b` → c.md becomes `a/b` → c.md). The
// namespace root is never merged into, and folders holding a file are left alone.
func compactInbox(n *inboxNode) {
	for _, c := range n.Children {
		for !c.IsFile && len(c.Children) == 1 && !c.Children[0].IsFile {
			sub := c.Children[0]
			c.Name = c.Name + "/" + sub.Name
			c.FullPath = sub.FullPath
			c.Children = sub.Children
		}
		compactInbox(c)
	}
}

func (s *server) handleUIInbox(w http.ResponseWriter, r *http.Request) {
	files, err := s.store.PendingFiles()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	renderUI(w, "inbox", map[string]any{
		"Groups": buildInboxGroups(files),
		"Total":  len(files),
		"Chrome": s.chrome(),
	})
}

// handleUIExport streams a zip of all non-archive entries in a namespace.
// Slashes in filenames are replaced with '-' so the zip is flat. Content is COALESCE(new, old).
func (s *server) handleUIExport(w http.ResponseWriter, r *http.Request) {
	// path: /ui/export/{ns}.zip
	rest := strings.TrimPrefix(r.URL.Path, "/ui/export/")
	ns := strings.TrimSuffix(rest, ".zip")
	if ns == "" || strings.Contains(ns, "/") {
		http.NotFound(w, r)
		return
	}
	files, err := s.store.List(ns)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	includeArchive := r.URL.Query().Get("include_archive") == "1"
	files = excludeDeleted(files)
	if !includeArchive {
		files = excludeArchive(files)
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+ns+`.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	for _, f := range files {
		content, _, err := s.store.Read(ns, f.Filename)
		if err != nil {
			continue
		}
		fw, err := zw.Create(strings.ReplaceAll(f.Filename, "/", "-"))
		if err != nil {
			return
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			return
		}
	}
}

func (s *server) registerUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/login", s.handleUILoginGet)
	mux.HandleFunc("POST /ui/login", s.handleUILoginPost)
	mux.HandleFunc("GET /ui/logout", s.handleUILogout)
	mux.HandleFunc("GET /ui/", s.uiAuth(s.handleUIHome))
	mux.HandleFunc("GET /ui/file", s.uiAuth(s.handleUIFile))
	mux.HandleFunc("GET /ui/edit", s.uiAuth(s.handleUIEditGet))
	mux.HandleFunc("POST /ui/edit", s.uiAuth(s.handleUIEditPost))
	mux.HandleFunc("POST /ui/archive", s.uiAuth(s.handleUIArchive))
	mux.HandleFunc("GET /ui/namespaces", s.uiAuth(s.handleUINamespaces))
	mux.HandleFunc("POST /ui/ns-close", s.uiAuth(s.handleUINamespaceClose))
	mux.HandleFunc("POST /ui/ns-reopen", s.uiAuth(s.handleUINamespaceReopen))
	mux.HandleFunc("POST /ui/ns-tag-add", s.uiAuth(s.handleUINamespaceTagAdd))
	mux.HandleFunc("POST /ui/ns-tag-remove", s.uiAuth(s.handleUINamespaceTagRemove))
	mux.HandleFunc("POST /ui/delete", s.uiAuth(s.handleUIDelete))
	mux.HandleFunc("POST /ui/hard-delete", s.uiAuth(s.handleUIHardDelete))
	mux.HandleFunc("GET /ui/new", s.uiAuth(s.handleUINewGet))
	mux.HandleFunc("POST /ui/new", s.uiAuth(s.handleUINewPost))
	mux.HandleFunc("POST /ui/review", s.uiAuth(s.handleUIReview))
	mux.HandleFunc("POST /ui/review-folder", s.uiAuth(s.handleUIReviewFolder))
	mux.HandleFunc("POST /ui/reject", s.uiAuth(s.handleUIReject))
	mux.HandleFunc("GET /ui/comments", s.uiAuth(s.handleUIComments))
	mux.HandleFunc("GET /ui/inbox", s.uiAuth(s.handleUIInbox))
	mux.HandleFunc("GET /ui/export/", s.uiAuth(s.handleUIExport))
}

const uiCSS = `
  :root {
    --bg: #ffffff;
    --bg-panel: #f9fafb;
    --bg-code: #f3f4f6;
    --bg-pre: #f9fafb;
    --text: #1f2937;
    --text-muted: #6b7280;
    --text-soft: #4b5563;
    --link: #2563eb;
    --link-hover: #1d4ed8;
    --border: #e5e7eb;
    --border-soft: #eef0f2;
    --border-strong: #d1d5db;
    --btn-bg: #2563eb;
    --btn-hover: #1d4ed8;
    --btn-text: #ffffff;
    --btn-danger: #dc2626;
    --btn-secondary: #6b7280;
    --btn-success: #059669;
    --pending: #d97706;
    --banner-bg: #fef3c7;
    --banner-border: #f59e0b;
    --banner-text: #92400e;
    --th-bg: #f9fafb;
    --details-arrow: #9ca3af;
    --shadow: rgba(0,0,0,0.04);
  }
  [data-theme="dark"] {
    --bg: #18181b;
    --bg-panel: #27272a;
    --bg-code: #2a2a2e;
    --bg-pre: #1f1f22;
    --text: #e4e4e7;
    --text-muted: #a1a1aa;
    --text-soft: #d4d4d8;
    --link: #60a5fa;
    --link-hover: #93c5fd;
    --border: #3f3f46;
    --border-soft: #27272a;
    --border-strong: #52525b;
    --btn-bg: #3f3f46;
    --btn-hover: #52525b;
    --btn-text: #f4f4f5;
    --btn-danger: #b91c1c;
    --btn-secondary: #52525b;
    --btn-success: #15803d;
    --pending: #f59e0b;
    --banner-bg: #292524;
    --banner-border: #78716c;
    --banner-text: #fbbf24;
    --th-bg: #27272a;
    --details-arrow: #71717a;
    --shadow: rgba(0,0,0,0.4);
  }
  * { box-sizing: border-box; }
  html { background: var(--bg); }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; max-width: none; margin: 24px 24px; padding: 0; color: var(--text); background: var(--bg); font-size: 16px; line-height: 1.5; }
  h1 { font-size: 24px; }
  h2 { font-size: 20px; }
  h1, h2, h3 { margin: 0 0 12px; }
  a { color: var(--link); text-decoration: none; }
  a:active { opacity: 0.6; }
  @media (hover: hover) { a:hover { text-decoration: underline; color: var(--link-hover); } }
  header { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; padding-bottom: 12px; border-bottom: 1px solid var(--border); margin-bottom: 20px; }
  header h1 { font-size: 20px; }
  header nav { display: flex; gap: 12px; align-items: center; }
  header nav .inbox { display: inline-flex; align-items: center; gap: 4px; }
  .theme-toggle { background: transparent; border: 1px solid var(--border); color: var(--text-muted); padding: 4px 10px; min-height: 0; border-radius: 6px; font-size: 14px; cursor: pointer; line-height: 1; }
  @media (hover: hover) { .theme-toggle:hover { background: var(--bg-panel); color: var(--text); } }
  .ns { background: var(--bg-panel); padding: 14px 16px; border-radius: 10px; margin-bottom: 14px; border: 1px solid var(--border); }
  .ns-picker { display: flex; gap: 10px; align-items: center; margin: 0 0 14px; font-size: 13px; color: var(--text-muted); }
  .ns-picker select { background: var(--bg); color: var(--text); border: 1px solid var(--border-strong); border-radius: 6px; padding: 6px 10px; font-size: 14px; font-family: inherit; }
  .ns h3 { font-size: 13px; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-muted); margin-bottom: 10px; display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .ns ul { list-style: none; margin: 0; padding: 0; }
  .ns li { padding: 10px 0; border-top: 1px solid var(--border-soft); line-height: 1.4; }
  .ns li:first-child { border-top: none; }
  .ns li a { font-size: 16px; word-break: break-word; }
  .meta { color: var(--text-muted); font-size: 13px; }
  pre { background: var(--bg-pre); padding: 14px; border-radius: 8px; overflow-x: auto; white-space: pre-wrap; word-break: break-word; border: 1px solid var(--border); font-size: 14px; line-height: 1.45; color: var(--text); }
  textarea, input[type=text], input[type=password] { width: 100%; padding: 12px; font-size: 16px; box-sizing: border-box; border: 1px solid var(--border-strong); border-radius: 8px; font-family: inherit; -webkit-appearance: none; background: var(--bg); color: var(--text); }
  textarea { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; min-height: 320px; font-size: 14px; }
  button, .btn { background: var(--btn-bg); color: var(--btn-text); border: none; padding: 12px 18px; border-radius: 8px; cursor: pointer; font-size: 16px; display: inline-block; text-align: center; min-height: 44px; line-height: 1.2; font-family: inherit; }
  @media (hover: hover) { button:hover, .btn:hover { background: var(--btn-hover); text-decoration: none; } }
  .btn-danger { background: var(--btn-danger); }
  .btn-secondary { background: var(--btn-secondary); }
  .btn-success { background: var(--btn-success); }
  .actions { margin: 16px 0; display: flex; gap: 10px; flex-wrap: wrap; }
  .actions > * { flex: 1 1 auto; min-width: 120px; }
  .field { margin: 14px 0; }
  .field label { display: block; font-size: 13px; color: var(--text-muted); margin-bottom: 6px; }
  .field-inline { display: flex; gap: 8px; align-items: center; margin: 12px 0; font-size: 14px; color: var(--text-soft); }
  .field-inline input[type=checkbox] { width: 18px; height: 18px; margin: 0; }
  .error { color: var(--btn-danger); margin: 8px 0; }
  form.inline { display: inline; flex: 1 1 auto; }
  ul.tree { list-style: none; margin: 0; padding: 0; }
  ul.tree ul.tree { padding-left: 18px; border-left: 1px solid var(--border); margin-left: 4px; }
  ul.tree li { padding: 6px 0; border-top: 1px solid var(--border-soft); line-height: 1.4; }
  ul.tree li:first-child { border-top: none; }
  ul.tree details > summary { cursor: pointer; list-style: none; padding: 2px 0; }
  ul.tree details > summary::-webkit-details-marker { display: none; }
  ul.tree details > summary::before { content: "▸"; display: inline-block; width: 1em; color: var(--details-arrow); transition: transform 0.1s; }
  ul.tree details[open] > summary::before { transform: rotate(90deg); }
  ul.tree .folder { font-weight: 600; }
  ul.tree details > summary { display: flex; align-items: center; gap: 8px; }
  ul.tree details > summary .approve-form { margin-left: auto; }
  .inbox-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .inbox-row a { word-break: break-word; }
  .inbox-row .approve-form { margin-left: auto; }
  .approve-form { display: inline; }
  .approve { background: transparent; color: var(--btn-success); border: 1px solid var(--border-strong); padding: 3px 11px; font-size: 12px; font-weight: 600; min-height: 0; border-radius: 999px; line-height: 1.5; cursor: pointer; white-space: nowrap; transition: background 0.12s, color 0.12s, border-color 0.12s; }
  @media (hover: hover) { .approve:hover { background: var(--btn-success); border-color: var(--btn-success); color: #fff; } }
  .approve:active { opacity: 0.65; }
  .pending-dot { color: var(--pending); font-size: 14px; line-height: 1; }
  .pending-tag { color: var(--pending); font-size: 12px; font-weight: 600; }
  .ns-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 8px 0; border-bottom: 1px solid var(--border-soft); }
  .ns-row .ns-name { font-weight: 600; min-width: 150px; }
  .ns-row.closed .ns-name { color: var(--text-soft); text-decoration: line-through; }
  .tag-chip { display: inline-flex; align-items: center; gap: 4px; background: var(--bg-code); border: 1px solid var(--border); border-radius: 999px; padding: 2px 4px 2px 9px; font-size: 12px; }
  .tag-chip form { display: inline; }
  .tag-chip button { background: transparent; border: 0; padding: 0; min-height: 0; line-height: 1; font-size: 12px; color: var(--text-soft); cursor: pointer; }
  .tag-filter { display: flex; gap: 6px; flex-wrap: wrap; margin: 12px 0; }
  .tag-filter button { background: var(--bg-code); border: 1px solid var(--border-strong); border-radius: 999px; padding: 3px 11px; font-size: 12px; min-height: 0; cursor: pointer; color: var(--text); }
  .tag-filter button.active { background: var(--btn-success); border-color: var(--btn-success); color: #fff; }
  .review-banner { position: sticky; top: 0; z-index: 5; background: var(--banner-bg); border: 1px solid var(--banner-border); color: var(--banner-text); padding: 12px 14px; border-radius: 8px; margin: 16px 0; display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
  .review-banner strong { flex: 1 1 auto; }
  .review-banner form { display: inline; }
  .review-banner button { padding: 8px 14px; font-size: 14px; min-height: 0; }
  .diff-toolbar { display: flex; gap: 16px; align-items: center; margin: 8px 0 4px; font-size: 13px; color: var(--text-soft); }
  .diff-toolbar label { display: inline-flex; align-items: center; gap: 6px; cursor: pointer; }
  .diff-toolbar input[type=checkbox] { width: 16px; height: 16px; margin: 0; }
  .diff-container { border: 1px solid var(--border-strong); border-radius: 8px; overflow: hidden; margin: 4px 0 12px; }
  #diff { width: 100%; height: 480px; }
  .comments { margin-top: 24px; border-top: 1px solid var(--border); padding-top: 16px; }
  .comments h3 { font-size: 14px; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-muted); margin-bottom: 12px; display: flex; align-items: center; gap: 10px; }
  .comments .toggle-history { font-size: 12px; font-weight: normal; text-transform: none; letter-spacing: normal; }
  .comment { padding: 10px 12px; margin: 8px 0; background: var(--bg-panel); border: 1px solid var(--border); border-radius: 6px; }
  .comment.reviewed { opacity: 0.55; }
  .comment .meta { font-size: 12px; margin-bottom: 4px; }
  .comment .body { white-space: pre-wrap; word-break: break-word; }
  .md { line-height: 1.6; word-wrap: break-word; }
  .md h1 { font-size: 26px; margin: 24px 0 12px; padding-bottom: 6px; border-bottom: 1px solid var(--border); }
  .md h2 { font-size: 22px; margin: 22px 0 10px; padding-bottom: 4px; border-bottom: 1px solid var(--border); }
  .md h3 { font-size: 18px; margin: 20px 0 8px; }
  .md h4, .md h5, .md h6 { font-size: 16px; margin: 16px 0 8px; }
  .md p { margin: 0 0 12px; }
  .md ul, .md ol { margin: 0 0 12px; padding-left: 28px; }
  .md li { margin: 4px 0; }
  .md li > p { margin: 0; }
  .md code { background: var(--bg-code); padding: 2px 6px; border-radius: 4px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.9em; }
  .md pre { background: var(--bg-pre); padding: 14px; border-radius: 8px; overflow-x: auto; border: 1px solid var(--border); }
  .md pre code { background: transparent; padding: 0; font-size: 13px; }
  .md blockquote { margin: 0 0 12px; padding: 4px 14px; border-left: 4px solid var(--border-strong); color: var(--text-soft); }
  .md blockquote > :last-child { margin-bottom: 0; }
  .md table { border-collapse: collapse; margin: 12px 0; width: 100%; }
  .md table th, .md table td { border: 1px solid var(--border); padding: 6px 10px; text-align: left; }
  .md table th { background: var(--th-bg); }
  .md hr { border: none; border-top: 1px solid var(--border); margin: 20px 0; }
  .md img { max-width: 100%; height: auto; border-radius: 6px; }
  .md input[type=checkbox] { margin-right: 6px; }
  @media (max-width: 600px) {
    body { margin: 12px auto; padding: 0 12px; }
    header h1 { font-size: 18px; }
    .ns { padding: 12px; }
    #diff { height: 360px; }
  }
`

// themeBootJS runs synchronously in <head> before paint so the stored theme
// is applied before any styled content is rendered (no flash of wrong theme).
// Also installs the global toggle + Monaco-aware setter used by the chrome
// button and (on the file-view page) the diff editor.
const themeBootJS = `
(function () {
  var saved = localStorage.getItem('gb-theme');
  var prefersDark = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
  var theme = saved || (prefersDark ? 'dark' : 'light');
  document.documentElement.setAttribute('data-theme', theme);

  window.gbCurrentTheme = function () {
    return document.documentElement.getAttribute('data-theme') || 'light';
  };
  window.gbApplyTheme = function (t) {
    document.documentElement.setAttribute('data-theme', t);
    localStorage.setItem('gb-theme', t);
    if (window.monaco && window.monaco.editor) {
      window.monaco.editor.setTheme(t === 'dark' ? 'vs-dark' : 'vs');
    }
    var btn = document.getElementById('theme-toggle');
    if (btn) btn.textContent = t === 'dark' ? '☀' : '☾';
  };
  window.gbToggleTheme = function () {
    window.gbApplyTheme(window.gbCurrentTheme() === 'dark' ? 'light' : 'dark');
  };
})();
`

const chromeStart = `<!DOCTYPE html><html><head><title>go-brain</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<style>` + uiCSS + `</style>
<script>` + themeBootJS + `</script>
</head><body>
{{template "chrome_header" .Chrome}}
`
const chromeEnd = `</body></html>`

const uiTemplateSrc = `
{{define "chrome_header"}}<header>
  <h1><a href="/ui/">go-brain</a></h1>
  <nav>
    <a class="inbox" href="/ui/inbox">Inbox{{if .InboxCount}} <span class="pending-tag">({{.InboxCount}})</span>{{end}}</a>
    <a href="/ui/namespaces">Namespaces</a>
    <a href="/ui/new">+ New</a>
    <button id="theme-toggle" type="button" class="theme-toggle" aria-label="Toggle theme" onclick="window.gbToggleTheme()">☾</button>
    <a href="/ui/logout">Logout</a>
  </nav>
  <script>(function(){var b=document.getElementById('theme-toggle');if(b)b.textContent=window.gbCurrentTheme()==='dark'?'☀':'☾';})();</script>
</header>{{end}}

{{define "login"}}<!DOCTYPE html><html><head><title>go-brain — Login</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<style>` + uiCSS + `
  body { max-width: 360px; margin: 80px auto; padding: 20px; }
</style>
<script>` + themeBootJS + `</script>
</head><body>
<h2>go-brain</h2>
{{if .Error}}<p class="error">Invalid password.</p>{{end}}
<form method="POST" action="/ui/login">
  <input type="password" name="password" placeholder="Password" autofocus />
  <button type="submit">Sign in</button>
</form>
</body></html>{{end}}

{{define "home"}}` + chromeStart + `
{{if not .Groups}}<p class="meta">No files yet. <a href="/ui/new">Create one.</a></p>{{end}}
{{if .Groups}}<p class="meta">≈ {{tokens .GrandTotal}} tokens total</p>{{end}}

{{/* Render every namespace; JS below shows global (always) + one other from localStorage. */}}
{{range .Groups}}
<div class="ns" data-ns="{{.Namespace}}">
  <h3>
    {{.Namespace}}
    {{if .PendingCount}}<span class="pending-dot" title="{{.PendingCount}} pending">●</span>{{end}}
    <span class="meta" style="text-transform:none;letter-spacing:normal;font-weight:normal">≈ {{tokens .TotalSize}} tokens</span>
    <a href="/ui/new?ns={{.Namespace}}" style="font-size:12px;margin-left:8px">+ new</a>
    <a href="/ui/export/{{.Namespace}}.zip" style="font-size:12px;margin-left:4px">↓ download</a>
    <a href="/ui/export/{{.Namespace}}.zip?include_archive=1" style="font-size:12px;margin-left:4px">↓ +archive</a>
  </h3>
  {{template "treeChildren" (nodeCtx .Namespace .Tree)}}
</div>
{{end}}

<div class="ns-picker" id="ns-picker" style="display:none">
  <label for="ns-select">Other namespace:</label>
  <select id="ns-select"></select>
</div>

<script>
(function () {
  var panels = Array.from(document.querySelectorAll('.ns'));
  if (!panels.length) return;
  var picker = document.getElementById('ns-picker');
  var select = document.getElementById('ns-select');

  var globalPanel = null;
  var others = [];
  panels.forEach(function (p) {
    var name = p.getAttribute('data-ns');
    if (name === 'global') globalPanel = p;
    else others.push({ name: name, el: p });
  });

  // Hide every non-global panel by default; reveal one based on selection.
  others.forEach(function (o) { o.el.style.display = 'none'; });

  if (others.length === 0) return;  // only global exists — no picker needed

  // Insert picker right before the first non-global panel (or at the end if global is last).
  var anchor = globalPanel ? globalPanel.nextSibling : panels[0];
  if (anchor) anchor.parentNode.insertBefore(picker, anchor);
  else document.body.appendChild(picker);
  picker.style.display = '';

  // Populate dropdown.
  others.forEach(function (o) {
    var opt = document.createElement('option');
    opt.value = o.name;
    opt.textContent = o.name;
    select.appendChild(opt);
  });

  var stored = localStorage.getItem('gb-ns');
  var initial = stored && others.some(function (o) { return o.name === stored; })
    ? stored
    : others[0].name;
  select.value = initial;
  show(initial);

  select.addEventListener('change', function () {
    localStorage.setItem('gb-ns', select.value);
    show(select.value);
  });

  function show(name) {
    others.forEach(function (o) {
      o.el.style.display = (o.name === name) ? '' : 'none';
    });
  }
})();
</script>
` + chromeEnd + `{{end}}

{{define "treeChildren"}}{{$ns := .NS}}<ul class="tree">
  {{range .Node.Children}}
  <li>
    {{if .IsFile}}
    <a href="/ui/file?ns={{$ns | urlquery}}&name={{.FullPath | urlquery}}">{{.Name}}</a>
    {{if .HasPending}} <span class="pending-dot" title="pending review">●</span>{{end}}
    <span class="meta">— {{.File.UpdatedAt}} · ≈ {{tokens .Size}} tokens</span>
    {{else}}
    <details {{if not (isArchiveFolder .Name)}}open{{end}}>
      <summary>
        <span class="folder">{{.Name}}/</span>
        <span class="meta">≈ {{tokens .Size}} tokens{{if .Pending}}, <span class="pending-tag">{{.Pending}} pending</span>{{end}}</span>
      </summary>
      {{template "treeChildren" (nodeCtx $ns .)}}
    </details>
    {{end}}
  </li>
  {{end}}
</ul>{{end}}

{{define "file"}}` + chromeStart + `
<p class="meta"><a href="/ui/">← all</a> / {{.Namespace}} / <strong>{{.Filename}}</strong>{{if or .HasPending .HasMovedFrom}} <span class="pending-dot">●</span>{{end}}</p>
<p class="meta">Updated {{.UpdatedAt}} · ≈ {{tokens .Size}} tokens</p>

{{if .HasMovedFrom}}
<div class="review-banner">
  <strong>Moved here from {{.MovedFromNamespace}} / {{.MovedFromFilename}} — pending review.</strong>
  {{if not .HasPending}}
  <form class="inline" method="POST" action="/ui/review?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}">
    <button type="submit" class="btn-success">Approve</button>
  </form>
  <form class="inline" method="POST" action="/ui/reject?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}"
        onsubmit="return confirm('Reject the move? The file will be moved back to its original path.')">
    <button type="submit" class="btn-danger">Reject move</button>
  </form>
  {{end}}
</div>
{{end}}

{{if .HasPending}}
<div class="review-banner">
  <strong id="banner-text">Unreviewed changes since last review.</strong>
  <form id="form-save" method="POST" action="/ui/edit?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}">
    <input type="hidden" name="content" />
    <button type="submit" id="btn-save" disabled>Save</button>
  </form>
  <form id="form-review" method="POST" action="/ui/edit?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}">
    <input type="hidden" name="content" />
    <input type="hidden" name="already_reviewed" value="1" />
    <button type="submit" class="btn-success">Approve</button>
  </form>
  <form id="form-reject" method="POST" action="/ui/reject?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}"
        onsubmit="return confirm('Reject pending changes? Any unsaved edits will also be discarded.')">
    <button type="submit" class="btn-danger">Reject</button>
  </form>
</div>
<textarea id="content-left" style="display:none">{{.Content}}</textarea>
<textarea id="content-right" style="display:none">{{.New}}</textarea>
<div class="diff-toolbar">
  <label><input type="checkbox" id="toggle-wrap" /> Word wrap</label>
  <label><input type="checkbox" id="toggle-sbs" /> Side-by-side</label>
</div>
<div class="diff-container"><div id="diff"></div></div>
<script src="https://cdnjs.cloudflare.com/ajax/libs/monaco-editor/0.44.0/min/vs/loader.min.js"></script>
<script>
  require.config({ paths: { 'vs': 'https://cdnjs.cloudflare.com/ajax/libs/monaco-editor/0.44.0/min/vs' } });
  require(['vs/editor/editor.main'], function () {
    var oldText = document.getElementById('content-left').value;
    var newText = document.getElementById('content-right').value;
    var lang = /\.md$/i.test({{.Filename}}) ? 'markdown' : 'plaintext';

    // Persist toggle state across files; defaults: wrap on, inline view.
    var wrap = localStorage.getItem('gb-diff-wrap') !== '0';
    var sbs  = localStorage.getItem('gb-diff-sbs')  === '1';

    var wrapBox = document.getElementById('toggle-wrap');
    var sbsBox  = document.getElementById('toggle-sbs');
    wrapBox.checked = wrap;
    sbsBox.checked  = sbs;

    var initialTheme = window.gbCurrentTheme() === 'dark' ? 'vs-dark' : 'vs';
    monaco.editor.setTheme(initialTheme);

    var diff = monaco.editor.createDiffEditor(document.getElementById('diff'), {
      readOnly: false,          // modified side is editable so the user can revise during review
      renderSideBySide: sbs,
      // Without this, Monaco silently falls back to the inline view when the
      // viewport is narrow — making the "Side-by-side" toggle look broken.
      useInlineViewWhenSpaceIsLimited: false,
      automaticLayout: true,
      originalEditable: false,  // left side (last reviewed) stays read-only
      hideUnchangedRegions: { enabled: false },
      wordWrap: wrap ? 'on' : 'off',
      theme: initialTheme
    });
    diff.setModel({
      original: monaco.editor.createModel(oldText, lang),
      modified: monaco.editor.createModel(newText, lang)
    });

    // Dirty tracking: enable Save + flag the banner when the modified side
    // diverges from the server's new column.
    var saveBtn   = document.getElementById('btn-save');
    var banner    = document.getElementById('banner-text');
    var bannerOK  = 'Unreviewed changes since last review.';
    var bannerDirty = 'Unreviewed changes since last review. — edits unsaved';
    function currentText() { return diff.getModel().modified.getValue(); }
    function isDirty()     { return currentText() !== newText; }
    function refreshDirty() {
      var d = isDirty();
      saveBtn.disabled = !d;
      banner.textContent = d ? bannerDirty : bannerOK;
    }
    diff.getModel().modified.onDidChangeContent(refreshDirty);

    // Inject current diff text into Save + Review forms at submit time, and
    // mark that we're submitting on purpose so beforeunload doesn't prompt.
    var submitting = false;
    ['form-save', 'form-review', 'form-reject'].forEach(function (id) {
      var f = document.getElementById(id);
      f.addEventListener('submit', function () {
        submitting = true;
        var hidden = f.querySelector('input[name=content]');
        if (hidden) hidden.value = currentText();
      });
    });
    // Warn before nav if the user has unsaved edits — but only for real navs,
    // not for our own form submissions.
    window.addEventListener('beforeunload', function (e) {
      if (submitting) return;
      if (isDirty()) { e.preventDefault(); e.returnValue = ''; }
    });

    wrapBox.addEventListener('change', function () {
      var on = wrapBox.checked;
      localStorage.setItem('gb-diff-wrap', on ? '1' : '0');
      diff.updateOptions({ wordWrap: on ? 'on' : 'off' });
    });
    sbsBox.addEventListener('change', function () {
      var on = sbsBox.checked;
      localStorage.setItem('gb-diff-sbs', on ? '1' : '0');
      diff.updateOptions({
        renderSideBySide: on,
        useInlineViewWhenSpaceIsLimited: false
      });
      // Force re-layout; updateOptions doesn't always retrigger it.
      diff.layout();
    });
  });
</script>
{{else}}
<div class="actions">
  <a class="btn" href="/ui/edit?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}">Edit</a>
  {{if not .IsArchived}}
  <form class="inline" method="POST" action="/ui/archive?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}" onsubmit="return confirm('Archive {{.Filename}}?')">
    <button type="submit" class="btn-secondary">Archive</button>
  </form>
  {{end}}
  <form class="inline" method="POST" action="/ui/delete?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}" onsubmit="return confirm('Delete {{.Filename}}?')">
    <button type="submit" class="btn-danger">Delete</button>
  </form>
  <form class="inline" method="POST" action="/ui/hard-delete?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}" onsubmit="return confirm('Permanently erase {{.Filename}}? This cannot be undone.')">
    <button type="submit" class="btn-danger">Hard Delete</button>
  </form>
</div>
{{if .Rendered}}<div class="md">{{.Rendered}}</div>{{else}}<pre>{{.Content}}</pre>{{end}}
{{end}}

<div class="comments" id="comments-section">
  <h3>
    Comments
    {{if .IncludeHistory}}
    <a class="toggle-history" href="?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}">Hide history</a>
    {{else}}
    <a class="toggle-history" href="?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}&history=1">Show history</a>
    {{end}}
  </h3>
  <div id="comments-list">
    {{template "comments_fragment" (dict "Comments" .Comments "NextOffset" 20 "HaveMore" .CommentsHaveMore "Namespace" .Namespace "Filename" .Filename "IncludeReviewed" .IncludeHistory)}}
  </div>
</div>
` + chromeEnd + `{{end}}

{{define "comments_fragment"}}
{{if not .Comments}}<p class="meta">No comments.</p>{{end}}
{{range .Comments}}
<div class="comment {{if .Reviewed}}reviewed{{end}}">
  <div class="meta">{{.CreatedAt}}{{if .Reviewed}} · reviewed{{end}}</div>
  <div class="body">{{.Content}}</div>
</div>
{{end}}
{{if .HaveMore}}
<form method="GET" action="/ui/comments" style="margin-top:10px">
  <input type="hidden" name="ns" value="{{.Namespace}}" />
  <input type="hidden" name="file" value="{{.Filename}}" />
  <input type="hidden" name="offset" value="{{.NextOffset}}" />
  {{if .IncludeReviewed}}<input type="hidden" name="include_reviewed" value="1" />{{end}}
  <button type="submit" class="btn btn-secondary">Show more</button>
</form>
{{end}}
{{end}}

{{define "namespaces"}}` + chromeStart + `
<h2>Namespaces</h2>
<p class="meta">Close a namespace to retire it from boot, search, and the namespace list without deleting it — reopen anytime. Tags are UI-only labels for organizing. Filter by tag below.</p>

<div class="tag-filter" id="tag-filter"></div>

<div id="ns-list">
{{range .Records}}
  {{$ns := .Name}}
  <div class="ns-row{{if .Closed}} closed{{end}}" data-tags="{{range .Tags}}{{.}} {{end}}">
    <span class="ns-name">{{$ns}}</span>
    {{if .Closed}}<span class="pending-tag" style="color:var(--text-soft)">closed</span>{{end}}
    {{range .Tags}}
    <span class="tag-chip">{{.}}
      <form method="POST" action="/ui/ns-tag-remove?ns={{$ns | urlquery}}&tag={{. | urlquery}}"><button type="submit" title="remove tag">✕</button></form>
    </span>
    {{end}}
    <form class="inline" method="POST" action="/ui/ns-tag-add?ns={{$ns | urlquery}}">
      <input type="text" name="tag" placeholder="+ tag" style="width:88px;padding:4px 8px;font-size:12px" />
    </form>
    <span style="margin-left:auto">
    {{if .Closed}}
      <form class="inline" method="POST" action="/ui/ns-reopen?ns={{$ns | urlquery}}"><button type="submit" style="font-size:12px">↑ reopen</button></form>
    {{else}}
      <form class="inline" method="POST" action="/ui/ns-close?ns={{$ns | urlquery}}"
            onsubmit="return confirm('Close {{$ns}}? Hidden from boot, search, and the namespace list until reopened.')"><button type="submit" style="font-size:12px">✕ close</button></form>
    {{end}}
    </span>
  </div>
{{end}}
</div>

<script>
(function () {
  var rows = Array.from(document.querySelectorAll('.ns-row'));
  var bar = document.getElementById('tag-filter');
  var seen = {};
  rows.forEach(function (r) {
    (r.getAttribute('data-tags') || '').trim().split(/\s+/).forEach(function (t) { if (t) seen[t] = true; });
  });
  var tags = Object.keys(seen).sort();
  if (!tags.length) return;
  var active = null;
  function apply() {
    rows.forEach(function (r) {
      var rt = (r.getAttribute('data-tags') || '').trim().split(/\s+/);
      r.style.display = (!active || rt.indexOf(active) >= 0) ? '' : 'none';
    });
    Array.from(bar.children).forEach(function (b) { b.classList.toggle('active', b.dataset.tag === active); });
  }
  tags.forEach(function (t) {
    var b = document.createElement('button');
    b.textContent = t; b.dataset.tag = t;
    b.onclick = function () { active = (active === t) ? null : t; apply(); };
    bar.appendChild(b);
  });
})();
</script>
` + chromeEnd + `{{end}}

{{define "inbox"}}` + chromeStart + `
<h2>Inbox{{if .Total}} <span class="meta" style="font-size:15px">({{.Total}})</span>{{end}}</h2>
{{if not .Groups}}<p class="meta">No pending changes.</p>{{end}}
{{range .Groups}}
<div class="ns" data-ns="{{.Namespace}}">
  <h3>
    {{.Namespace}}
    <span class="pending-tag">{{.Pending}} pending</span>
    <form class="approve-form" style="margin-left:auto" method="POST" action="/ui/review-folder?ns={{.Namespace | urlquery}}"
          onsubmit="return confirm('Approve all {{.Pending}} pending in {{.Namespace}}?')">
      <button type="submit" class="approve">✓ Approve all</button>
    </form>
  </h3>
  {{template "inboxChildren" (nodeCtx .Namespace .Tree)}}
</div>
{{end}}
` + chromeEnd + `{{end}}

{{define "inboxChildren"}}{{$ns := .NS}}<ul class="tree">
  {{range .Node.Children}}
  <li>
    {{if .IsFile}}
    <div class="inbox-row">
      <a href="/ui/file?ns={{$ns | urlquery}}&name={{.FullPath | urlquery}}">{{.Name}}</a>
      <span class="pending-dot">●</span>
      {{if .File.MovedFromFilename}}<span class="pending-tag">← moved from {{.File.MovedFromNamespace}} / {{.File.MovedFromFilename}}</span>{{end}}
      <form class="approve-form" method="POST" action="/ui/review?ns={{$ns | urlquery}}&name={{.FullPath | urlquery}}&return=inbox">
        <button type="submit" class="approve" title="Approve">✓</button>
      </form>
    </div>
    <div class="meta">
      {{if .File.CommentCount}}{{.File.CommentCount}} comment{{if ne .File.CommentCount 1}}s{{end}} · {{snippet .File.LastCommentSnippet}}{{else}}no comment{{end}}
      · {{.File.SortAt}}
    </div>
    {{else}}
    <details open>
      <summary>
        <span class="folder">{{.Name}}/</span>
        <span class="meta"><span class="pending-tag">{{.Pending}}</span></span>
        <form class="approve-form" method="POST" action="/ui/review-folder?ns={{$ns | urlquery}}&prefix={{.FullPath | urlquery}}"
              onsubmit="return confirm('Approve all {{.Pending}} pending in {{.Name}}/?')">
          <button type="submit" class="approve" onclick="event.stopPropagation()">✓ Approve all</button>
        </form>
      </summary>
      {{template "inboxChildren" (nodeCtx $ns .)}}
    </details>
    {{end}}
  </li>
  {{end}}
</ul>{{end}}

{{define "edit"}}` + chromeStart + `
<p class="meta"><a href="/ui/">← all</a> / {{.Namespace}} / <strong>{{.Filename}}</strong> (editing)</p>
<form method="POST" action="/ui/edit?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}">
  <div class="field">
    <textarea name="content">{{.Content}}</textarea>
  </div>
  <div class="field">
    <label>Comment (optional)</label>
    <textarea name="comment" rows="2" style="min-height:60px" placeholder="Why this change?"></textarea>
  </div>
  <label class="field-inline">
    <input type="checkbox" name="already_reviewed" value="1" />
    I've already reviewed this
  </label>
  <div class="actions">
    <button type="submit">Save</button>
    <a class="btn btn-secondary" href="/ui/file?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}">Cancel</a>
  </div>
</form>
` + chromeEnd + `{{end}}

{{define "new"}}` + chromeStart + `
<h2>New file</h2>
<form method="POST" action="/ui/new">
  <div class="field">
    <label>Namespace</label>
    <input type="text" name="namespace" value="{{.Namespace}}" required />
  </div>
  <div class="field">
    <label>Filename</label>
    <input type="text" name="filename" required />
  </div>
  <div class="field">
    <label>Content</label>
    <textarea name="content"></textarea>
  </div>
  <div class="field">
    <label>Comment (optional)</label>
    <textarea name="comment" rows="2" style="min-height:60px" placeholder="Why this change?"></textarea>
  </div>
  <label class="field-inline">
    <input type="checkbox" name="already_reviewed" value="1" />
    I've already reviewed this
  </label>
  <div class="actions">
    <button type="submit">Create</button>
    <a class="btn btn-secondary" href="/ui/">Cancel</a>
  </div>
</form>
` + chromeEnd + `{{end}}
`
