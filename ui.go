package main

import (
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

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(baseURL, "https://"),
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

func currentSession(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	ok, err := store.HasSession(c.Value)
	if err != nil || !ok {
		return "", false
	}
	return c.Value, true
}

func uiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := currentSession(r); !ok {
			http.Redirect(w, r, "/ui/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

var uiFuncs = template.FuncMap{
	"tokens": func(n int) string { return formatThousands(n / 4) },
	"nodeCtx": func(ns string, n *treeNode) map[string]any {
		return map[string]any{"NS": ns, "Node": n}
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

func handleUILoginGet(w http.ResponseWriter, r *http.Request) {
	renderUI(w, "login", map[string]any{"Error": r.URL.Query().Get("error")})
}

func handleUILoginPost(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	pw := r.FormValue("password")
	if frontendPassword == "" || pw != frontendPassword {
		http.Redirect(w, r, "/ui/login?error=1", http.StatusFound)
		return
	}
	tok := randHex(32)
	if err := store.CreateSession(tok); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, tok)
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

func handleUILogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		_ = store.DeleteSession(c.Value)
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/ui/login", http.StatusFound)
}

type treeNode struct {
	Name     string
	FullPath string
	IsFile   bool
	File     FileEntry
	Children []*treeNode
	Size     int
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
		node.Size = f.Size
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

func rollupAndSort(n *treeNode) {
	for _, c := range n.Children {
		rollupAndSort(c)
		if !n.IsFile {
			n.Size += c.Size
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

type namespaceGroup struct {
	Namespace string
	Tree      *treeNode
	TotalSize int
}

func handleUIHome(w http.ResponseWriter, r *http.Request) {
	namespaces, err := store.ListNamespaces()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	groups := make([]namespaceGroup, 0, len(namespaces))
	grandTotal := 0
	for _, ns := range namespaces {
		files, err := store.List(ns)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tree := buildTree(files)
		grandTotal += tree.Size
		groups = append(groups, namespaceGroup{Namespace: ns, Tree: tree, TotalSize: tree.Size})
	}
	renderUI(w, "home", map[string]any{"Groups": groups, "GrandTotal": grandTotal})
}

func handleUIFile(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	content, updatedAt, err := store.Read(ns, name)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Namespace": ns,
		"Filename":  name,
		"Content":   content,
		"UpdatedAt": updatedAt,
		"Size":      len(content),
	}
	if strings.HasSuffix(strings.ToLower(name), ".md") {
		rendered, err := renderMarkdown(content)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data["Rendered"] = rendered
	}
	renderUI(w, "file", data)
}

func handleUIEditGet(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	content, _, err := store.Read(ns, name)
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
	})
}

func handleUIEditPost(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	r.ParseForm()
	content := r.FormValue("content")
	if err := store.Write(ns, name, content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/file?"+url.Values{"ns": {ns}, "name": {name}}.Encode(), http.StatusFound)
}

func handleUIDelete(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	if err := store.Delete(ns, name); err != nil && !errors.Is(err, ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

func handleUINewGet(w http.ResponseWriter, r *http.Request) {
	renderUI(w, "new", map[string]any{
		"Namespace": r.URL.Query().Get("ns"),
	})
}

func handleUINewPost(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	ns := strings.TrimSpace(r.FormValue("namespace"))
	name := strings.TrimSpace(r.FormValue("filename"))
	content := r.FormValue("content")
	if ns == "" || name == "" {
		http.Error(w, "namespace and filename are required", http.StatusBadRequest)
		return
	}
	if err := store.Write(ns, name, content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/file?"+url.Values{"ns": {ns}, "name": {name}}.Encode(), http.StatusFound)
}

func registerUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/login", handleUILoginGet)
	mux.HandleFunc("POST /ui/login", handleUILoginPost)
	mux.HandleFunc("GET /ui/logout", handleUILogout)
	mux.HandleFunc("GET /ui/", uiAuth(handleUIHome))
	mux.HandleFunc("GET /ui/file", uiAuth(handleUIFile))
	mux.HandleFunc("GET /ui/edit", uiAuth(handleUIEditGet))
	mux.HandleFunc("POST /ui/edit", uiAuth(handleUIEditPost))
	mux.HandleFunc("POST /ui/delete", uiAuth(handleUIDelete))
	mux.HandleFunc("GET /ui/new", uiAuth(handleUINewGet))
	mux.HandleFunc("POST /ui/new", uiAuth(handleUINewPost))
}

const uiCSS = `
  * { box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; max-width: 900px; margin: 24px auto; padding: 0 16px; color: #1f2937; font-size: 16px; line-height: 1.5; }
  h1 { font-size: 24px; }
  h2 { font-size: 20px; }
  h1, h2, h3 { margin: 0 0 12px; }
  a { color: #2563eb; text-decoration: none; }
  a:active { opacity: 0.6; }
  @media (hover: hover) { a:hover { text-decoration: underline; } }
  header { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; padding-bottom: 12px; border-bottom: 1px solid #e5e7eb; margin-bottom: 20px; }
  header h1 { font-size: 20px; }
  header nav { display: flex; gap: 12px; }
  .ns { background: #f9fafb; padding: 14px 16px; border-radius: 10px; margin-bottom: 14px; border: 1px solid #e5e7eb; }
  .ns h3 { font-size: 13px; text-transform: uppercase; letter-spacing: 0.05em; color: #6b7280; margin-bottom: 10px; display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .ns ul { list-style: none; margin: 0; padding: 0; }
  .ns li { padding: 10px 0; border-top: 1px solid #eef0f2; line-height: 1.4; }
  .ns li:first-child { border-top: none; }
  .ns li a { font-size: 16px; word-break: break-word; }
  .meta { color: #6b7280; font-size: 13px; }
  pre { background: #f9fafb; padding: 14px; border-radius: 8px; overflow-x: auto; white-space: pre-wrap; word-break: break-word; border: 1px solid #e5e7eb; font-size: 14px; line-height: 1.45; }
  textarea, input[type=text], input[type=password] { width: 100%; padding: 12px; font-size: 16px; box-sizing: border-box; border: 1px solid #d1d5db; border-radius: 8px; font-family: inherit; -webkit-appearance: none; }
  textarea { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; min-height: 320px; font-size: 14px; }
  button, .btn { background: #2563eb; color: white; border: none; padding: 12px 18px; border-radius: 8px; cursor: pointer; font-size: 16px; display: inline-block; text-align: center; min-height: 44px; line-height: 1.2; font-family: inherit; }
  @media (hover: hover) { button:hover, .btn:hover { background: #1d4ed8; text-decoration: none; } }
  .btn-danger { background: #dc2626; }
  .btn-secondary { background: #6b7280; }
  .actions { margin: 16px 0; display: flex; gap: 10px; flex-wrap: wrap; }
  .actions > * { flex: 1 1 auto; min-width: 120px; }
  .field { margin: 14px 0; }
  .field label { display: block; font-size: 13px; color: #6b7280; margin-bottom: 6px; }
  .error { color: #dc2626; margin: 8px 0; }
  form.inline { display: inline; flex: 1 1 auto; }
  ul.tree { list-style: none; margin: 0; padding: 0; }
  ul.tree ul.tree { padding-left: 18px; border-left: 1px solid #e5e7eb; margin-left: 4px; }
  ul.tree li { padding: 6px 0; border-top: 1px solid #eef0f2; line-height: 1.4; }
  ul.tree li:first-child { border-top: none; }
  ul.tree details > summary { cursor: pointer; list-style: none; padding: 2px 0; }
  ul.tree details > summary::-webkit-details-marker { display: none; }
  ul.tree details > summary::before { content: "▸"; display: inline-block; width: 1em; color: #9ca3af; transition: transform 0.1s; }
  ul.tree details[open] > summary::before { transform: rotate(90deg); }
  ul.tree .folder { font-weight: 600; }
  .md { line-height: 1.6; word-wrap: break-word; }
  .md h1 { font-size: 26px; margin: 24px 0 12px; padding-bottom: 6px; border-bottom: 1px solid #e5e7eb; }
  .md h2 { font-size: 22px; margin: 22px 0 10px; padding-bottom: 4px; border-bottom: 1px solid #e5e7eb; }
  .md h3 { font-size: 18px; margin: 20px 0 8px; }
  .md h4, .md h5, .md h6 { font-size: 16px; margin: 16px 0 8px; }
  .md p { margin: 0 0 12px; }
  .md ul, .md ol { margin: 0 0 12px; padding-left: 28px; }
  .md li { margin: 4px 0; }
  .md li > p { margin: 0; }
  .md code { background: #f3f4f6; padding: 2px 6px; border-radius: 4px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.9em; }
  .md pre { background: #f9fafb; padding: 14px; border-radius: 8px; overflow-x: auto; border: 1px solid #e5e7eb; }
  .md pre code { background: transparent; padding: 0; font-size: 13px; }
  .md blockquote { margin: 0 0 12px; padding: 4px 14px; border-left: 4px solid #d1d5db; color: #4b5563; }
  .md blockquote > :last-child { margin-bottom: 0; }
  .md table { border-collapse: collapse; margin: 12px 0; width: 100%; }
  .md table th, .md table td { border: 1px solid #e5e7eb; padding: 6px 10px; text-align: left; }
  .md table th { background: #f9fafb; }
  .md hr { border: none; border-top: 1px solid #e5e7eb; margin: 20px 0; }
  .md img { max-width: 100%; height: auto; border-radius: 6px; }
  .md input[type=checkbox] { margin-right: 6px; }
  @media (max-width: 600px) {
    body { margin: 12px auto; padding: 0 12px; }
    header h1 { font-size: 18px; }
    .ns { padding: 12px; }
  }
`

const chromeStart = `<!DOCTYPE html><html><head><title>go-brain</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light">
<style>` + uiCSS + `</style></head><body>
<header><h1><a href="/ui/">go-brain</a></h1><nav><a href="/ui/new">+ New</a> <a href="/ui/logout">Logout</a></nav></header>
`
const chromeEnd = `</body></html>`

const uiTemplateSrc = `
{{define "login"}}<!DOCTYPE html><html><head><title>go-brain — Login</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  * { box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; max-width: 360px; margin: 80px auto; padding: 20px; color: #1f2937; }
  h2 { margin: 0 0 16px; }
  input { width: 100%; padding: 12px; margin: 8px 0; font-size: 16px; border: 1px solid #d1d5db; border-radius: 8px; -webkit-appearance: none; }
  button { width: 100%; padding: 12px; background: #2563eb; color: white; border: none; border-radius: 8px; font-size: 16px; cursor: pointer; min-height: 44px; font-family: inherit; }
  .error { color: #dc2626; margin: 8px 0; }
</style></head><body>
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
{{range .Groups}}
<div class="ns">
  <h3>{{.Namespace}} <span class="meta" style="text-transform:none;letter-spacing:normal;font-weight:normal">≈ {{tokens .TotalSize}} tokens</span> <a href="/ui/new?ns={{.Namespace}}" style="font-size:12px;margin-left:8px">+ new</a></h3>
  {{template "treeChildren" (nodeCtx .Namespace .Tree)}}
</div>
{{end}}
` + chromeEnd + `{{end}}

{{define "treeChildren"}}{{$ns := .NS}}<ul class="tree">
  {{range .Node.Children}}
  <li>
    {{if .IsFile}}
    <a href="/ui/file?ns={{$ns | urlquery}}&name={{.FullPath | urlquery}}">{{.Name}}</a>
    <span class="meta">— {{.File.UpdatedAt}} · ≈ {{tokens .Size}} tokens</span>
    {{else}}
    <details open>
      <summary><span class="folder">{{.Name}}/</span> <span class="meta">≈ {{tokens .Size}} tokens</span></summary>
      {{template "treeChildren" (nodeCtx $ns .)}}
    </details>
    {{end}}
  </li>
  {{end}}
</ul>{{end}}

{{define "file"}}` + chromeStart + `
<p class="meta"><a href="/ui/">← all</a> / {{.Namespace}} / <strong>{{.Filename}}</strong></p>
<p class="meta">Updated {{.UpdatedAt}} · ≈ {{tokens .Size}} tokens</p>
<div class="actions">
  <a class="btn" href="/ui/edit?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}">Edit</a>
  <form class="inline" method="POST" action="/ui/delete?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}" onsubmit="return confirm('Delete {{.Filename}}?')">
    <button type="submit" class="btn-danger">Delete</button>
  </form>
</div>
{{if .Rendered}}<div class="md">{{.Rendered}}</div>{{else}}<pre>{{.Content}}</pre>{{end}}
` + chromeEnd + `{{end}}

{{define "edit"}}` + chromeStart + `
<p class="meta"><a href="/ui/">← all</a> / {{.Namespace}} / <strong>{{.Filename}}</strong> (editing)</p>
<form method="POST" action="/ui/edit?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}">
  <div class="field">
    <textarea name="content">{{.Content}}</textarea>
  </div>
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
  <div class="actions">
    <button type="submit">Create</button>
    <a class="btn btn-secondary" href="/ui/">Cancel</a>
  </div>
</form>
` + chromeEnd + `{{end}}
`
