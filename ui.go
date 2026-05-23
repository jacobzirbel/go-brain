package main

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

const sessionCookieName = "gobrain_session"
const sessionPrefix = "ui:"

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
	if !hasToken(sessionPrefix + c.Value) {
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

var uiTemplates = template.Must(template.New("ui").Parse(uiTemplateSrc))

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
	addToken(sessionPrefix + tok)
	setSessionCookie(w, tok)
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

func handleUILogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		deleteToken(sessionPrefix + c.Value)
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/ui/login", http.StatusFound)
}

type namespaceGroup struct {
	Namespace string
	Files     []FileEntry
}

func handleUIHome(w http.ResponseWriter, r *http.Request) {
	namespaces, err := store.ListNamespaces()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	groups := make([]namespaceGroup, 0, len(namespaces))
	for _, ns := range namespaces {
		files, err := store.List(ns)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		groups = append(groups, namespaceGroup{Namespace: ns, Files: files})
	}
	renderUI(w, "home", map[string]any{"Groups": groups})
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
	renderUI(w, "file", map[string]any{
		"Namespace": ns,
		"Filename":  name,
		"Content":   content,
		"UpdatedAt": updatedAt,
	})
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
  body { font-family: -apple-system, BlinkMacSystemFont, sans-serif; max-width: 900px; margin: 30px auto; padding: 0 20px; color: #1f2937; }
  h1, h2, h3 { margin: 0 0 12px; }
  a { color: #2563eb; text-decoration: none; }
  a:hover { text-decoration: underline; }
  header { display: flex; justify-content: space-between; align-items: center; padding-bottom: 12px; border-bottom: 1px solid #e5e7eb; margin-bottom: 20px; }
  header nav a { margin-left: 16px; }
  .ns { background: #f9fafb; padding: 12px 16px; border-radius: 8px; margin-bottom: 16px; border: 1px solid #e5e7eb; }
  .ns h3 { font-size: 14px; text-transform: uppercase; letter-spacing: 0.05em; color: #6b7280; margin-bottom: 8px; }
  .ns ul { margin: 0; padding-left: 20px; }
  .ns li { margin: 4px 0; }
  .meta { color: #6b7280; font-size: 13px; }
  pre { background: #f9fafb; padding: 16px; border-radius: 8px; overflow-x: auto; white-space: pre-wrap; word-break: break-word; border: 1px solid #e5e7eb; }
  textarea, input[type=text], input[type=password] { width: 100%; padding: 10px; font-size: 14px; box-sizing: border-box; border: 1px solid #d1d5db; border-radius: 6px; font-family: inherit; }
  textarea { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; min-height: 400px; }
  button, .btn { background: #2563eb; color: white; border: none; padding: 8px 14px; border-radius: 6px; cursor: pointer; font-size: 14px; display: inline-block; }
  button:hover, .btn:hover { background: #1d4ed8; text-decoration: none; }
  .btn-danger { background: #dc2626; }
  .btn-danger:hover { background: #b91c1c; }
  .btn-secondary { background: #6b7280; }
  .btn-secondary:hover { background: #4b5563; }
  .actions { margin: 16px 0; display: flex; gap: 8px; }
  .field { margin: 12px 0; }
  .field label { display: block; font-size: 13px; color: #6b7280; margin-bottom: 4px; }
  .error { color: #dc2626; margin: 8px 0; }
  form.inline { display: inline; }
`

const chromeStart = `<!DOCTYPE html><html><head><title>go-brain</title><style>` + uiCSS + `</style></head><body>
<header><h1><a href="/ui/">go-brain</a></h1><nav><a href="/ui/new">+ New</a> <a href="/ui/logout">Logout</a></nav></header>
`
const chromeEnd = `</body></html>`

const uiTemplateSrc = `
{{define "login"}}<!DOCTYPE html><html><head><title>go-brain — Login</title><style>
  body { font-family: -apple-system, sans-serif; max-width: 360px; margin: 100px auto; padding: 20px; }
  input { width: 100%; padding: 10px; margin: 8px 0; box-sizing: border-box; font-size: 16px; border: 1px solid #d1d5db; border-radius: 6px; }
  button { width: 100%; padding: 10px; background: #2563eb; color: white; border: none; border-radius: 6px; font-size: 16px; cursor: pointer; }
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
{{range .Groups}}
<div class="ns">
  <h3>{{.Namespace}} <a href="/ui/new?ns={{.Namespace}}" style="font-size:12px;margin-left:8px">+ new</a></h3>
  <ul>
    {{range .Files}}
    <li><a href="/ui/file?ns={{$.Namespace | urlquery}}&name={{.Filename | urlquery}}">{{.Filename}}</a> <span class="meta">— {{.UpdatedAt}}</span></li>
    {{end}}
  </ul>
</div>
{{end}}
` + chromeEnd + `{{end}}

{{define "file"}}` + chromeStart + `
<p class="meta"><a href="/ui/">← all</a> / {{.Namespace}} / <strong>{{.Filename}}</strong></p>
<p class="meta">Updated {{.UpdatedAt}}</p>
<div class="actions">
  <a class="btn" href="/ui/edit?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}">Edit</a>
  <form class="inline" method="POST" action="/ui/delete?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}" onsubmit="return confirm('Delete {{.Filename}}?')">
    <button type="submit" class="btn-danger">Delete</button>
  </form>
</div>
<pre>{{.Content}}</pre>
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
