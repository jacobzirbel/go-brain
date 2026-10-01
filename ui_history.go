package main

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
)

// History UI: a file's revision list, a read-only diff between any two
// revisions, and restore-as-pending. Templates are parsed into uiTemplates at
// init so they share the chrome and funcs.

func init() {
	template.Must(uiTemplates.Parse(historyTemplateSrc))
}

func revisionID(r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	return id, err == nil && id > 0
}

func (s *server) handleUIHistory(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("ns")
	name := r.URL.Query().Get("name")
	revs, err := s.store.History(ns, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(revs) == 0 {
		http.NotFound(w, r)
		return
	}
	renderUI(w, "history", map[string]any{
		"Namespace": ns,
		"Filename":  name,
		"Revisions": revs,
		"Chrome":    s.chrome(),
	})
}

// handleUIRevision diffs revision `id` against `base` — by default the
// revision just before it on the same file (or empty, for the first).
func (s *server) handleUIRevision(w http.ResponseWriter, r *http.Request) {
	id, ok := revisionID(r, "id")
	if !ok {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	rev, err := s.store.GetRevision(id)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var base Revision
	hasBase := false
	if baseID, ok := revisionID(r, "base"); ok {
		base, err = s.store.GetRevision(baseID)
		hasBase = err == nil
	} else {
		base, err = s.store.PreviousRevision(id)
		hasBase = err == nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	renderUI(w, "revision", map[string]any{
		"Rev":     rev,
		"Base":    base,
		"HasBase": hasBase,
		"Chrome":  s.chrome(),
	})
}

func (s *server) handleUIRestore(w http.ResponseWriter, r *http.Request) {
	id, ok := revisionID(r, "id")
	if !ok {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	rev, err := s.store.RestoreRevision(id)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/file?"+url.Values{"ns": {rev.Namespace}, "name": {rev.Filename}}.Encode(), http.StatusFound)
}

const historyTemplateSrc = `
{{define "history"}}` + chromeStart + `
<p class="meta"><a href="/ui/">← all</a> / {{.Namespace}} / <a href="/ui/file?ns={{.Namespace | urlquery}}&name={{.Filename | urlquery}}"><strong>{{.Filename}}</strong></a> / history</p>
<form method="GET" action="/ui/revision">
<div style="overflow-x:auto"><table class="history">
  <thead><tr><th title="compare from">from</th><th title="compare to">to</th><th>#</th><th>when</th><th>op</th><th>size</th><th></th></tr></thead>
  <tbody>
  {{range $i, $r := .Revisions}}
  <tr>
    <td><input type="radio" name="base" value="{{$r.ID}}" {{if eq $i 1}}checked{{end}} /></td>
    <td><input type="radio" name="id" value="{{$r.ID}}" {{if eq $i 0}}checked{{end}} /></td>
    <td>{{$r.ID}}</td>
    <td class="meta">{{$r.CreatedAt}}</td>
    <td>{{$r.Op}}{{if $r.FromFilename}} <span class="meta">from {{$r.FromNamespace}} / {{$r.FromFilename}}</span>{{end}}</td>
    <td class="meta">≈ {{tokens $r.Size}} tokens</td>
    <td><a href="/ui/revision?id={{$r.ID}}">diff</a></td>
  </tr>
  {{end}}
  </tbody>
</table></div>
{{if gt (len .Revisions) 1}}<div class="actions"><button type="submit">Compare selected</button></div>{{end}}
</form>
` + chromeEnd + `{{end}}

{{define "revision"}}` + chromeStart + `
<p class="meta"><a href="/ui/">← all</a> / {{.Rev.Namespace}} / <a href="/ui/file?ns={{.Rev.Namespace | urlquery}}&name={{.Rev.Filename | urlquery}}"><strong>{{.Rev.Filename}}</strong></a> / <a href="/ui/history?ns={{.Rev.Namespace | urlquery}}&name={{.Rev.Filename | urlquery}}">history</a></p>
<p class="meta">
  {{if .HasBase}}#{{.Base.ID}} {{.Base.Op}} · {{.Base.CreatedAt}}{{else}}(empty — first revision){{end}}
  → <strong>#{{.Rev.ID}} {{.Rev.Op}}</strong> · {{.Rev.CreatedAt}}
  {{if .Rev.FromFilename}} · from {{.Rev.FromNamespace}} / {{.Rev.FromFilename}}{{end}}
</p>
<div class="actions">
  <form class="inline" method="POST" action="/ui/restore?id={{.Rev.ID}}"
        onsubmit="return confirm('Restore revision #{{.Rev.ID}}? It becomes the pending version, for review like any edit.')">
    <button type="submit" class="btn-secondary">Restore this version</button>
  </form>
</div>
<textarea id="content-left" style="display:none">{{if .HasBase}}{{.Base.Content}}{{end}}</textarea>
<textarea id="content-right" style="display:none">{{.Rev.Content}}</textarea>
<div class="diff-toolbar">
  <label><input type="checkbox" id="toggle-wrap" /> Word wrap</label>
  <label><input type="checkbox" id="toggle-sbs" /> Side-by-side</label>
</div>
<div class="diff-container"><div id="diff"></div></div>
<script src="https://cdnjs.cloudflare.com/ajax/libs/monaco-editor/0.44.0/min/vs/loader.min.js"></script>
<script>
  require.config({ paths: { 'vs': 'https://cdnjs.cloudflare.com/ajax/libs/monaco-editor/0.44.0/min/vs' } });
  require(['vs/editor/editor.main'], function () {
    var lang = /\.md$/i.test({{.Rev.Filename}}) ? 'markdown' : 'plaintext';
    var wrap = localStorage.getItem('gb-diff-wrap') !== '0';
    var sbs  = localStorage.getItem('gb-diff-sbs')  === '1';
    var wrapBox = document.getElementById('toggle-wrap');
    var sbsBox  = document.getElementById('toggle-sbs');
    wrapBox.checked = wrap;
    sbsBox.checked  = sbs;
    var theme = window.gbCurrentTheme() === 'dark' ? 'vs-dark' : 'vs';
    monaco.editor.setTheme(theme);
    var diff = monaco.editor.createDiffEditor(document.getElementById('diff'), {
      readOnly: true,
      originalEditable: false,
      renderSideBySide: sbs,
      useInlineViewWhenSpaceIsLimited: false,
      automaticLayout: true,
      wordWrap: wrap ? 'on' : 'off',
      theme: theme
    });
    diff.setModel({
      original: monaco.editor.createModel(document.getElementById('content-left').value, lang),
      modified: monaco.editor.createModel(document.getElementById('content-right').value, lang)
    });
    wrapBox.addEventListener('change', function () {
      localStorage.setItem('gb-diff-wrap', wrapBox.checked ? '1' : '0');
      diff.updateOptions({ wordWrap: wrapBox.checked ? 'on' : 'off' });
    });
    sbsBox.addEventListener('change', function () {
      localStorage.setItem('gb-diff-sbs', sbsBox.checked ? '1' : '0');
      diff.updateOptions({ renderSideBySide: sbsBox.checked, useInlineViewWhenSpaceIsLimited: false });
      diff.layout();
    });
  });
</script>
` + chromeEnd + `{{end}}
`
