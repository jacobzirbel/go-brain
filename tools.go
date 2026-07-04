package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

const filenameDescription = "Name of the file. Slashes create folders for display purposes, e.g. \"journal/2026-05-23.md\" or \"projects/website/notes.md\"."

const commentDescription = "Optional. Short explanation of why this change was made. Use when the change isn't self-evident from the diff."

// ── Tool schema ───────────────────────────────────────────────────────────────

type toolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

type property struct {
	Type        string       `json:"type"`
	Description string       `json:"description,omitempty"`
	Items       *inputSchema `json:"items,omitempty"`
}

var mcpTools = []toolDef{
	{
		Name:        "read",
		Description: "cat <filename> — get the contents of a file in a namespace. Pass `section` to return just that section's bytes verbatim.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace": {Type: "string", Description: "Namespace for the file"},
				"filename":  {Type: "string", Description: filenameDescription},
				"section":   {Type: "string", Description: "Optional. Accepts a canonical slug (\"phase-10-design\") or the heading text as written (\"Phase 10 design\"). On miss the error lists available slugs in source order."},
			},
			Required: []string{"namespace", "filename"},
		},
	},
	{
		Name:        "boot",
		Description: "Boot a namespace — returns its index.md and state.md.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace": {Type: "string", Description: "Namespace to boot."},
			},
			Required: []string{"namespace"},
		},
	},
	{
		Name:        "create",
		Description: "Create a new file in a namespace. Fails if the file already exists — use force_write to replace an existing file, or edit for surgical changes.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace": {Type: "string"},
				"filename":  {Type: "string", Description: filenameDescription},
				"content":   {Type: "string"},
				"comment":   {Type: "string", Description: commentDescription},
			},
			Required: []string{"namespace", "filename", "content"},
		},
	},
	{
		Name:        "force_write",
		Description: "Overwrite an existing file in a namespace, replacing its entire contents. Fails if the file does not exist — use create for new files. Prefer edit for surgical changes; reach for force_write only when replacing the whole file.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace": {Type: "string"},
				"filename":  {Type: "string", Description: filenameDescription},
				"content":   {Type: "string"},
				"comment":   {Type: "string", Description: commentDescription},
			},
			Required: []string{"namespace", "filename", "content"},
		},
	},
	{
		Name:        "append",
		Description: "Append text to a file in a namespace (creates if not exists; inserts a newline first if the file is non-empty).",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace": {Type: "string"},
				"filename":  {Type: "string", Description: filenameDescription},
				"content":   {Type: "string"},
				"comment":   {Type: "string", Description: commentDescription},
			},
			Required: []string{"namespace", "filename", "content"},
		},
	},
	{
		Name:        "edit",
		Description: "Replace `old_str` with `new_str` in the named file. Fails if `old_str` is missing or matches more than once. Pair with `read(section=...)` for section-scoped edits — the returned bytes are verbatim and round-trip safely.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace": {Type: "string"},
				"filename":  {Type: "string", Description: filenameDescription},
				"old_str":   {Type: "string", Description: "Exact bytes to find. Must occur exactly once."},
				"new_str":   {Type: "string", Description: "Replacement bytes."},
				"comment":   {Type: "string", Description: commentDescription},
			},
			Required: []string{"namespace", "filename", "old_str", "new_str"},
		},
	},
	{
		Name:        "namespaces",
		Description: "List all namespaces that contain at least one file.",
		InputSchema: inputSchema{
			Type:       "object",
			Properties: map[string]property{},
		},
	},
	{
		Name:        "tree",
		Description: "Folder structure of a namespace, with markdown headings surfaced as nested section nodes under each .md file. Section labels show the original heading text; pass that text — or the slug — to `read(section=...)`.\n\nDEFAULTS:\n- Root files expand to ## sections (depth=1).\n- Sub-folders collapse to \"name/ (N items)\" — pass `path=\"name/\"` to expand one.\n- archived/ is hidden unless include_archive=true or a path targets it; deleted/ is always hidden unless a path explicitly targets it.\n\nPARAMS:\n- `path`: literal file (\"decisions.md\"), folder (\"tasks/\"), or doublestar glob (\"**/decisions.md\"). Globs bypass folder collapse so matches render in full.\n- `depth`: 0=files only; 1=## (default); 2=## and ###; N=up to level N+1; 99=all headings and full folder recursion.\n- `include_archive`: include archived/ (not deleted/).\n- `meta`: annotate each file with last-modified date and pending flag.\n\nSections ≥400 approx tokens are annotated with their token count.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace":       {Type: "string"},
				"path":            {Type: "string", Description: "Optional literal filename, folder prefix (\"tasks/\"), or doublestar glob (\"**/decisions.md\", \"decisions/*.md\")."},
				"depth":           {Type: "integer", Description: "Section heading depth. 0/1/2/.../99 — see tool description."},
				"include_archive": {Type: "boolean", Description: "Include the archived/ prefix. Defaults to false. Does not include deleted/."},
				"meta":            {Type: "boolean", Description: "Annotate each file with updated_at date and pending status. Defaults to false."},
			},
			Required: []string{"namespace"},
		},
	},
	// The copy, comments, review, and reject tools are handled by runTool but
	// deliberately unadvertised: approver actions live in the UI.
	{
		Name:        "archive",
		Description: "Move a file to the archived/ prefix, excluded from search and list by default. Use for files you may reread but don't need day-to-day. Pass include_archive=true to see them again.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace": {Type: "string"},
				"filename":  {Type: "string"},
			},
			Required: []string{"namespace", "filename"},
		},
	},
	{
		Name:        "remove",
		Description: "Delete a file",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace": {Type: "string"},
				"filename":  {Type: "string"},
			},
			Required: []string{"namespace", "filename"},
		},
	},
	{
		Name:        "move",
		Description: "Rename or move a file. Destination may be a different namespace and/or folder (use slashes to change folders). Fails if the destination exists.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace":     {Type: "string", Description: "Source namespace"},
				"filename":      {Type: "string", Description: "Source filename"},
				"new_namespace": {Type: "string", Description: "Destination namespace (defaults to source)"},
				"new_filename":  {Type: "string", Description: "Destination filename, may contain slashes"},
			},
			Required: []string{"namespace", "filename", "new_filename"},
		},
	},
	{
		Name:        "search",
		Description: "Full-text search; returns matching files with snippet previews. FTS5 syntax: loose keyword by default, \"double quotes\" for phrase, AND/OR/NOT for boolean. Also matches a file's basename, not its folder path. Use `path` for glob-scoped search. Pass namespace=\"*\" to search every namespace at once (hits include their namespace). Excludes archived/ (pass include_archive=true)",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace":       {Type: "string", Description: "Namespace to search. Pass \"*\" to search across all namespaces."},
				"query":           {Type: "string", Description: "FTS5 MATCH expression. Loose keyword by default; \"phrase\" for adjacency; AND/OR/NOT for boolean."},
				"path":            {Type: "string", Description: "Optional doublestar glob over filename, e.g. \"tasks/**\" or \"decisions/*.md\"."},
				"limit":           {Type: "integer", Description: "Page size. Default 20, hard cap 100."},
				"order":           {Type: "string", Description: "\"bm25\" (default, relevance) or \"recency\" (updated_at DESC)."},
				"include_archive": {Type: "boolean", Description: "Include the archived/ prefix. Defaults to false."},
			},
			Required: []string{"namespace", "query"},
		},
	},
	{
		Name:        "append_to_section",
		Description: "Append content to the end of a named section's body, just before the next heading at the same or shallower depth (deeper sub-sections stay inside). On section miss, errors and lists available slugs unless create_if_missing=true, in which case a new ## section is appended at end of file. Ambiguous matches (same heading appears more than once) error rather than guess.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace":         {Type: "string"},
				"filename":          {Type: "string", Description: filenameDescription},
				"section":           {Type: "string", Description: "Canonical slug (\"phase-10-design\") or heading text (\"Phase 10 design\"). Ambiguous matches error; specify an exact slug to disambiguate."},
				"content":           {Type: "string"},
				"create_if_missing": {Type: "boolean", Description: "Create the section as a new ## heading at end of file when it doesn't exist. Defaults to false."},
				"comment":           {Type: "string", Description: commentDescription},
			},
			Required: []string{"namespace", "filename", "section", "content"},
		},
	},
	{
		Name:        "upsert_section",
		Description: "Replace the entire body of a named section with new content, leaving its heading line untouched. If the section doesn't exist, creates it as a new ## section appended at end of file. Ambiguous matches (same heading appears more than once) error rather than guess.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"namespace": {Type: "string"},
				"filename":  {Type: "string", Description: filenameDescription},
				"section":   {Type: "string", Description: "Canonical slug or heading text. Ambiguous matches error; specify an exact slug to disambiguate."},
				"content":   {Type: "string", Description: "New body for the section (everything after the heading line). May include sub-headings. Replaces the old body entirely."},
				"comment":   {Type: "string", Description: commentDescription},
			},
			Required: []string{"namespace", "filename", "section", "content"},
		},
	},
	{
		Name:        "move_many",
		Description: "mv (batch) - Rename or move multiple files atomically in a single transaction. If any move fails (e.g. destination already exists, source not found), all moves are rolled back.",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"moves": {
					Type:        "array",
					Description: "Array of move operations to perform atomically.",
					Items: &inputSchema{
						Type: "object",
						Properties: map[string]property{
							"namespace":     {Type: "string", Description: "Source namespace"},
							"filename":      {Type: "string", Description: "Source filename"},
							"new_namespace": {Type: "string", Description: "Destination namespace (defaults to source namespace if omitted)"},
							"new_filename":  {Type: "string", Description: "Destination filename"},
						},
						Required: []string{"namespace", "filename", "new_filename"},
					},
				},
			},
			Required: []string{"moves"},
		},
	},
}

// ── Tool results ──────────────────────────────────────────────────────────────

// errResult is the uniform error payload for every tool. Beyond Error, the
// optional fields carry recovery hints: candidate lists, close matches, and
// ambiguity details — present only when the specific miss produces them.
type errResult struct {
	Error       string       `json:"error"`
	Available   []string     `json:"available,omitempty"`
	Suggestions []string     `json:"suggestions,omitempty"`
	Count       int          `json:"count,omitempty"`
	Matches     int          `json:"matches,omitempty"`
	Conflicts   []sectionRef `json:"conflicts,omitempty"`
}

type sectionRef struct {
	Slug    string `json:"slug"`
	Heading string `json:"heading"`
}

type okResult struct {
	OK bool `json:"ok"`
}

type moveManyResult struct {
	OK    bool `json:"ok"`
	Moved int  `json:"moved"`
}

type readResult struct {
	Content   string `json:"content"`
	UpdatedAt string `json:"updated_at"`
	Section   string `json:"section,omitempty"`
}

type nsFile struct {
	Content   string `json:"content"`
	UpdatedAt string `json:"updated_at"`
}

type bootResult struct {
	Namespace string   `json:"namespace"`
	Index     *nsFile  `json:"index,omitempty"`
	State     *nsFile  `json:"state,omitempty"`
	Missing   []string `json:"missing,omitempty"`
}

type namespacesResult struct {
	Namespaces []string `json:"namespaces"`
}

type treeResult struct {
	Tree string `json:"tree"`
}

type searchResult struct {
	Results []SearchHit `json:"results"`
}

type commentsResult struct {
	Comments []Comment `json:"comments"`
}

type sectionResult struct {
	Section string `json:"section"`
	Content string `json:"content"`
}

// ── Argument access ───────────────────────────────────────────────────────────

type toolArgs map[string]any

func (a toolArgs) str(key string) string {
	v, _ := a[key].(string)
	return v
}

// ns returns the named argument lowercased — namespaces are case-insensitive.
func (a toolArgs) ns(key string) string {
	return strings.ToLower(a.str(key))
}

func (a toolArgs) boolean(key string) bool {
	v, _ := a[key].(bool)
	return v
}

func (a toolArgs) integer(key string, def int) int {
	v, ok := a[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return def
}

// ── Shared error builders ─────────────────────────────────────────────────────

// namespaceMissError builds the "namespace doesn't exist" recovery payload: the
// full namespace list (the same set `namespaces` returns) plus close-match
// suggestions. Shared by read and boot so a bad namespace reports identically.
func namespaceMissError(namespace string, nsList []string) errResult {
	return errResult{
		Error:       fmt.Sprintf("namespace %q does not exist", namespace),
		Available:   nsList,
		Suggestions: closeMatches(namespace, nsList, 5),
	}
}

// readMissError distinguishes the two ways a read can miss — the namespace
// doesn't exist, or it does but the file isn't in it — and returns a recovery
// hint shaped like the section-miss error: an `available` list the caller can
// retry against. The two cases are never ambiguous. The error text names which
// half of the path was wrong, a namespace miss lists every namespace (the same
// set `namespaces` returns) plus close-match `suggestions`, and a file miss
// lists the namespace's files — falling back to the closest matches plus a total
// `count` when that list is long.
func readMissError(store *SQLiteStore, namespace, filename string) errResult {
	nsList, err := store.ListNamespaces()
	if err != nil {
		return errResult{Error: err.Error()}
	}
	if !slices.Contains(nsList, namespace) {
		return namespaceMissError(namespace, nsList)
	}
	// Namespace exists, so the filename is the wrong half.
	files, err := store.List(namespace)
	if err != nil {
		return errResult{Error: err.Error()}
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Filename
	}
	resp := errResult{
		Error: fmt.Sprintf("file %q not found in namespace %q", filename, namespace),
	}
	const fileListCap = 20
	if len(names) <= fileListCap {
		resp.Available = names
	} else {
		// Too many to dump verbatim: surface the closest matches and the total
		// so the response stays compact but still points somewhere useful.
		hits := closeMatches(filename, names, 10)
		if len(hits) == 0 {
			hits = names[:fileListCap]
		}
		resp.Available = hits
		resp.Count = len(names)
	}
	return resp
}

// storeError maps the store's sentinel errors to their tool-facing messages;
// anything unrecognized passes through verbatim.
func storeError(err error) errResult {
	switch {
	case errors.Is(err, ErrNotFound):
		return errResult{Error: "not found"}
	case errors.Is(err, ErrExists):
		return errResult{Error: "file already exists"}
	case errors.Is(err, ErrDestinationExists):
		return errResult{Error: "destination already exists"}
	case errors.Is(err, ErrNoPending):
		return errResult{Error: "no pending changes"}
	}
	return errResult{Error: err.Error()}
}

// ── Dispatch ──────────────────────────────────────────────────────────────────

// toolEntry pairs a tool's handler with the validation every call must pass
// first. needsNS/needsContent hoist the checks that used to be pasted into
// every case; autoComment appends the optional changelog comment after a
// successful write so no tool forgets (or silently drops) it.
type toolEntry struct {
	needsNS      bool
	needsContent bool
	autoComment  bool
	run          func(s *SQLiteStore, a toolArgs) (any, bool)
}

var toolRunners = map[string]toolEntry{
	"read":              {needsNS: true, run: toolRead},
	"boot":              {needsNS: true, run: toolBoot},
	"create":            {needsNS: true, needsContent: true, autoComment: true, run: toolCreate},
	"force_write":       {needsNS: true, needsContent: true, autoComment: true, run: toolForceWrite},
	"append":            {needsNS: true, needsContent: true, autoComment: true, run: toolAppend},
	"edit":              {needsNS: true, autoComment: true, run: toolEdit},
	"comments":          {needsNS: true, run: toolComments},
	"review":            {needsNS: true, run: toolReview},
	"reject":            {needsNS: true, run: toolReject},
	"namespaces":        {run: toolNamespaces},
	"tree":              {needsNS: true, run: toolTree},
	"archive":           {needsNS: true, run: toolArchive},
	"remove":            {needsNS: true, run: toolRemove},
	"copy":              {needsNS: true, run: toolCopy},
	"move":              {needsNS: true, run: toolMove},
	"move_many":         {run: toolMoveMany},
	"search":            {run: toolSearch},
	"append_to_section": {needsNS: true, needsContent: true, autoComment: true, run: toolAppendToSection},
	"upsert_section":    {needsNS: true, autoComment: true, run: toolUpsertSection},
}

func runTool(store *SQLiteStore, name string, args map[string]any) (result any, isError bool) {
	entry, ok := toolRunners[name]
	if !ok {
		return errResult{Error: fmt.Sprintf("unknown tool: %s", name)}, true
	}
	a := toolArgs(args)
	if entry.needsNS && a.ns("namespace") == "" {
		return errResult{Error: "missing required argument: namespace"}, true
	}
	if entry.needsContent && a.str("content") == "" {
		return errResult{Error: "missing required argument: content"}, true
	}
	result, isError = entry.run(store, a)
	if !isError && entry.autoComment {
		if c := a.str("comment"); c != "" {
			if err := store.InsertComment(a.ns("namespace"), a.str("filename"), c); err != nil {
				return errResult{Error: err.Error()}, true
			}
		}
	}
	return result, isError
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func toolRead(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	content, updatedAt, err := s.Read(ns, a.str("filename"))
	if errors.Is(err, ErrNotFound) {
		return readMissError(s, ns, a.str("filename")), true
	}
	if err != nil {
		return errResult{Error: err.Error()}, true
	}
	if sectionQ := a.str("section"); sectionQ != "" {
		sections := parseSections([]byte(content))
		found, avail := findSection(sections, sectionQ)
		if found == nil {
			return errResult{Error: "section not found", Available: avail}, true
		}
		return readResult{
			Content:   content[found.Start:found.End],
			UpdatedAt: updatedAt,
			Section:   found.Slug,
		}, false
	}
	return readResult{Content: content, UpdatedAt: updatedAt}, false
}

func toolBoot(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	nsList, err := s.ListNamespaces()
	if err != nil {
		return errResult{Error: err.Error()}, true
	}
	if !slices.Contains(nsList, ns) {
		return namespaceMissError(ns, nsList), true
	}
	// Namespace exists: pull both session-context files in one shot. A file
	// that's absent isn't an error — it's reported in `missing` so a freshly
	// created namespace can still boot.
	out := bootResult{Namespace: ns}
	for _, fn := range []string{"index.md", "state.md"} {
		content, updatedAt, err := s.Read(ns, fn)
		if errors.Is(err, ErrNotFound) {
			out.Missing = append(out.Missing, fn)
			continue
		}
		if err != nil {
			return errResult{Error: err.Error()}, true
		}
		f := &nsFile{Content: content, UpdatedAt: updatedAt}
		if fn == "index.md" {
			out.Index = f
		} else {
			out.State = f
		}
	}
	return out, false
}

func toolEdit(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	filename := a.str("filename")
	oldStr := normalizeLineEndings(a.str("old_str"))
	newStr := normalizeLineEndings(a.str("new_str"))
	if oldStr == "" {
		return errResult{Error: "old_str cannot be empty"}, true
	}
	content, _, err := s.Read(ns, filename)
	if err != nil {
		return storeError(err), true
	}
	count := strings.Count(content, oldStr)
	if count == 0 {
		return errResult{Error: "old_str not found in file"}, true
	}
	if count > 1 {
		return errResult{
			Error:   fmt.Sprintf("old_str matches %d times; must be unique", count),
			Matches: count,
		}, true
	}
	if err := s.Write(ns, filename, strings.Replace(content, oldStr, newStr, 1)); err != nil {
		return errResult{Error: err.Error()}, true
	}
	return okResult{OK: true}, false
}

func toolCreate(s *SQLiteStore, a toolArgs) (any, bool) {
	if err := s.Create(a.ns("namespace"), a.str("filename"), a.str("content")); err != nil {
		return storeError(err), true
	}
	return okResult{OK: true}, false
}

func toolForceWrite(s *SQLiteStore, a toolArgs) (any, bool) {
	if err := s.ForceWrite(a.ns("namespace"), a.str("filename"), a.str("content")); err != nil {
		return storeError(err), true
	}
	return okResult{OK: true}, false
}

func toolAppend(s *SQLiteStore, a toolArgs) (any, bool) {
	if err := s.Append(a.ns("namespace"), a.str("filename"), a.str("content")); err != nil {
		return errResult{Error: err.Error()}, true
	}
	return okResult{OK: true}, false
}

func toolComments(s *SQLiteStore, a toolArgs) (any, bool) {
	rows, err := s.ListComments(
		a.ns("namespace"), a.str("filename"), a.boolean("include_reviewed"),
		a.integer("limit", 20), a.integer("offset", 0),
	)
	if err != nil {
		return errResult{Error: err.Error()}, true
	}
	return commentsResult{Comments: rows}, false
}

func toolReview(s *SQLiteStore, a toolArgs) (any, bool) {
	if err := s.Review(a.ns("namespace"), a.str("filename")); err != nil {
		return storeError(err), true
	}
	return okResult{OK: true}, false
}

func toolReject(s *SQLiteStore, a toolArgs) (any, bool) {
	if err := s.Reject(a.ns("namespace"), a.str("filename")); err != nil {
		return storeError(err), true
	}
	return okResult{OK: true}, false
}

func toolNamespaces(s *SQLiteStore, a toolArgs) (any, bool) {
	nsList, err := s.ListNamespaces()
	if err != nil {
		return errResult{Error: err.Error()}, true
	}
	return namespacesResult{Namespaces: nsList}, false
}

func toolTree(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	files, err := s.List(ns)
	if err != nil {
		return errResult{Error: err.Error()}, true
	}
	path := a.str("path")
	// deleted/ is always hidden unless path explicitly targets it.
	if !pathTargetsDeleted(path) {
		files = excludeDeleted(files)
	}
	// archive/archived/ are hidden by default; include_archive=true or an
	// explicit archive path overrides this.
	if !a.boolean("include_archive") && !pathTargetsArchive(path) {
		files = excludeArchive(files)
	}

	depth := a.integer("depth", treeDepthDefault)
	meta := a.boolean("meta")
	globMode := isGlobPattern(path)
	if path != "" {
		files, err = filterByPath(files, path)
		if err != nil {
			return errResult{Error: fmt.Sprintf("invalid path: %v", err)}, true
		}
	}
	tree := buildTree(files)

	// Deep recursion when explicitly requested (depth=99) or for glob
	// scopes (matches can be sparse across folders; collapsing them as
	// "(N items)" would hide the user's actual query result).
	var out string
	if depth >= 99 || globMode {
		out = renderTreeWithSections(tree, ns, s, "", true, depth, meta)
	} else {
		// scopePath is empty for namespace-root; otherwise the literal
		// folder/file path. (Glob already routed above.)
		out = renderTreeShallow(tree, path, ns, s, depth, meta)
	}
	return treeResult{Tree: strings.TrimRight(out, "\n")}, false
}

func toolArchive(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	srcFile := a.str("filename")
	if err := s.MoveForReview([]MoveOp{{ns, srcFile, ns, "archived/" + srcFile}}); err != nil {
		return storeError(err), true
	}
	return okResult{OK: true}, false
}

func toolRemove(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	srcFile := a.str("filename")
	if err := s.MoveForReview([]MoveOp{{ns, srcFile, ns, "deleted/" + srcFile}}); err != nil {
		return storeError(err), true
	}
	return okResult{OK: true}, false
}

func toolCopy(s *SQLiteStore, a toolArgs) (any, bool) {
	srcNS := a.ns("namespace")
	dstNS := a.ns("new_namespace")
	if dstNS == "" {
		dstNS = srcNS
	}
	dstFile := a.str("new_filename")
	if strings.HasSuffix(dstFile, "/") {
		parts := strings.Split(a.str("filename"), "/")
		dstFile += parts[len(parts)-1]
	}
	err := s.Copy(srcNS, a.str("filename"), dstNS, dstFile)
	if errors.Is(err, ErrNotFound) {
		return errResult{Error: "source not found"}, true
	}
	if err != nil {
		return storeError(err), true
	}
	return okResult{OK: true}, false
}

func toolMove(s *SQLiteStore, a toolArgs) (any, bool) {
	srcNS := a.ns("namespace")
	dstNS := a.ns("new_namespace")
	if dstNS == "" {
		dstNS = srcNS
	}
	err := s.MoveForReview([]MoveOp{{
		SrcNamespace: srcNS,
		SrcFilename:  a.str("filename"),
		DstNamespace: dstNS,
		DstFilename:  a.str("new_filename"),
	}})
	if errors.Is(err, ErrNotFound) {
		return errResult{Error: "source not found"}, true
	}
	if err != nil {
		return storeError(err), true
	}
	return okResult{OK: true}, false
}

func toolMoveMany(s *SQLiteStore, a toolArgs) (any, bool) {
	raw, _ := a["moves"].([]any)
	ops := make([]MoveOp, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return errResult{Error: fmt.Sprintf("moves[%d] is not an object", i)}, true
		}
		move := toolArgs(m)
		srcNS := move.ns("namespace")
		dstNS := move.ns("new_namespace")
		if dstNS == "" {
			dstNS = srcNS
		}
		ops = append(ops, MoveOp{
			SrcNamespace: srcNS,
			SrcFilename:  move.str("filename"),
			DstNamespace: dstNS,
			DstFilename:  move.str("new_filename"),
		})
	}
	err := s.MoveForReview(ops)
	if errors.Is(err, ErrNotFound) {
		return errResult{Error: "a source file was not found; no moves were applied"}, true
	}
	if errors.Is(err, ErrDestinationExists) {
		return errResult{Error: "a destination already exists; no moves were applied"}, true
	}
	if err != nil {
		return errResult{Error: err.Error()}, true
	}
	return moveManyResult{OK: true, Moved: len(ops)}, false
}

func toolSearch(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	if ns == "" {
		return errResult{Error: "missing required argument: namespace (use \"*\" to search all namespaces)"}, true
	}
	if ns == "*" {
		ns = ""
	}
	query := a.str("query")
	if query == "" {
		return errResult{Error: "query required"}, true
	}
	hits, err := s.Search(SearchOptions{
		Namespace:      ns,
		Query:          query,
		Path:           a.str("path"),
		Limit:          a.integer("limit", 20),
		Order:          a.str("order"),
		IncludeArchive: a.boolean("include_archive"),
	})
	if err != nil {
		return errResult{Error: err.Error()}, true
	}
	return searchResult{Results: hits}, false
}

func toolAppendToSection(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	filename := a.str("filename")
	sectionQ := a.str("section")
	if sectionQ == "" {
		return errResult{Error: "missing required argument: section"}, true
	}
	content := normalizeLineEndings(a.str("content"))

	existing, _, err := s.Read(ns, filename)
	if err != nil {
		return storeError(err), true
	}

	source := []byte(existing)
	secs := parseSections(source)
	found, conflicts, avail := findSectionForWrite(secs, sectionQ)

	if len(conflicts) > 0 {
		return ambiguousSectionError(sectionQ, conflicts), true
	}

	var newFile string
	var sectionSlug string

	if found == nil {
		if !a.boolean("create_if_missing") {
			return errResult{
				Error:     fmt.Sprintf("section %q not found", sectionQ),
				Available: avail,
			}, true
		}
		// Create a new ## section at end of file then append content into it.
		sectionSlug = slugifyHeading(sectionQ)
		newFile = appendNewSection(existing, sectionQ, content)
	} else {
		sectionSlug = found.Slug
		// Insert a separating newline + content before the section boundary.
		prefix := string(source[:found.End])
		if len(prefix) > 0 && prefix[len(prefix)-1] != '\n' {
			prefix += "\n"
		}
		newFile = prefix + "\n" + ensureTrailingNewline(content) + string(source[found.End:])
	}

	if err := s.Write(ns, filename, newFile); err != nil {
		return errResult{Error: err.Error()}, true
	}
	return updatedSectionResult(newFile, sectionSlug), false
}

func toolUpsertSection(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	filename := a.str("filename")
	sectionQ := a.str("section")
	if sectionQ == "" {
		return errResult{Error: "missing required argument: section"}, true
	}
	content := normalizeLineEndings(a.str("content"))

	existing, _, err := s.Read(ns, filename)
	if err != nil {
		return storeError(err), true
	}

	source := []byte(existing)
	secs := parseSections(source)
	found, conflicts, _ := findSectionForWrite(secs, sectionQ)

	if len(conflicts) > 0 {
		return ambiguousSectionError(sectionQ, conflicts), true
	}

	var newFile string
	var sectionSlug string

	if found == nil {
		// Create a new ## section at end of file.
		sectionSlug = slugifyHeading(sectionQ)
		newFile = appendNewSection(existing, sectionQ, content)
	} else {
		sectionSlug = found.Slug
		// Find the end of the heading line (first \n after Start).
		headingEnd := found.Start
		for headingEnd < found.End && source[headingEnd] != '\n' {
			headingEnd++
		}
		if headingEnd < found.End {
			headingEnd++ // include the trailing \n of the heading line
		}
		newFile = string(source[:headingEnd]) + ensureTrailingNewline(content) + string(source[found.End:])
	}

	if err := s.Write(ns, filename, newFile); err != nil {
		return errResult{Error: err.Error()}, true
	}
	return updatedSectionResult(newFile, sectionSlug), false
}

// ── Section-write helpers ─────────────────────────────────────────────────────

func ambiguousSectionError(sectionQ string, conflicts []section) errResult {
	refs := make([]sectionRef, len(conflicts))
	for i, c := range conflicts {
		refs[i] = sectionRef{Slug: c.Slug, Heading: c.Heading}
	}
	return errResult{
		Error:     fmt.Sprintf("ambiguous section %q: %d sections share this heading; specify an exact slug", sectionQ, len(conflicts)),
		Conflicts: refs,
	}
}

// appendNewSection appends `## heading` plus content at end of file, keeping
// the blank-line separation and trailing-newline invariants.
func appendNewSection(existing, heading, content string) string {
	sep := "\n\n"
	if len(existing) == 0 {
		sep = ""
	}
	return existing + sep + "## " + heading + "\n\n" + ensureTrailingNewline(content)
}

func ensureTrailingNewline(s string) string {
	if len(s) > 0 && s[len(s)-1] != '\n' {
		return s + "\n"
	}
	return s
}

// updatedSectionResult re-parses the written file and returns the section's
// final bytes so the caller can verify the write landed as intended.
func updatedSectionResult(newFile, sectionSlug string) sectionResult {
	updatedSecs := parseSections([]byte(newFile))
	updatedFound, _, _ := findSectionForWrite(updatedSecs, sectionSlug)
	res := sectionResult{Section: sectionSlug}
	if updatedFound != nil {
		res.Content = newFile[updatedFound.Start:updatedFound.End]
	}
	return res
}

// ── Tree rendering ────────────────────────────────────────────────────────────

// sectionTokenThreshold controls when section nodes get a token-count annotation
// in tree output. Below this, the slug alone is shown; at-or-above, "(Nk)" surfaces
// kitchen-sink sections without bloating the rest of the tree.
const sectionTokenThreshold = 400

// renderTreeShallow is the Phase 11.1 default: it descends to `scopePath`
// (empty = namespace root) and renders that folder's direct children with
// `ls`-like semantics — files expand with sections, sub-folders collapse to
// "name/ (N items)" summaries. depth=99 or glob path-mode routes through
// renderTreeWithSections instead for full recursion.
func renderTreeShallow(root *treeNode, scopePath, namespace string, store *SQLiteStore, depth int, meta bool) string {
	scope := root
	var header string
	if scopePath != "" {
		parts := splitPath(strings.TrimSuffix(scopePath, "/"))
		n := root
		for _, p := range parts {
			child := findChild(n, p)
			if child == nil {
				return ""
			}
			n = child
		}
		if n.IsFile {
			// single-file path: render just the file with its sections.
			// Reuse the deep renderer so connector/section logic stays in one place.
			parent := &treeNode{Children: []*treeNode{n}}
			return renderTreeWithSections(parent, namespace, store, "", true, depth, meta)
		}
		scope = n
		header = strings.TrimSuffix(scopePath, "/") + "/\n"
	}

	var sb strings.Builder
	sb.WriteString(header)
	for i, child := range scope.Children {
		isLast := i == len(scope.Children)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}
		if child.IsFile {
			label := child.Name
			if meta {
				label += fileMeta(child.File)
			}
			sb.WriteString(connector + label + "\n")
			childPrefix := "    "
			if !isLast {
				childPrefix = "│   "
			}
			if depth > 0 && strings.HasSuffix(strings.ToLower(child.Name), ".md") {
				content, _, err := store.Read(namespace, child.File.Filename)
				if err == nil {
					sb.WriteString(renderSections(parseSections([]byte(content)), childPrefix, depth))
				}
			}
		} else {
			n := countFiles(child)
			noun := "item"
			if n != 1 {
				noun = "items"
			}
			sb.WriteString(fmt.Sprintf("%s%s/ (%d %s)\n", connector, child.Name, n, noun))
		}
	}
	return sb.String()
}

// countFiles returns the count of file leaves in the subtree.
func countFiles(node *treeNode) int {
	if node.IsFile {
		return 1
	}
	n := 0
	for _, c := range node.Children {
		n += countFiles(c)
	}
	return n
}

// renderTreeWithSections renders the file/folder tree and, for `.md` files,
// surfaces markdown headings as nested section nodes. `depth` caps which
// heading levels appear — see keepHeadingAtDepth. depth=0 short-circuits the
// per-file content read entirely.
func renderTreeWithSections(node *treeNode, namespace string, store *SQLiteStore, prefix string, isRoot bool, depth int, meta bool) string {
	var sb strings.Builder
	for i, child := range node.Children {
		isLast := i == len(node.Children)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}
		if isRoot {
			connector = ""
		}
		label := child.Name
		if !child.IsFile {
			label += "/"
		} else if meta {
			label += fileMeta(child.File)
		}
		fmt.Fprintf(&sb, "%s%s%s\n", prefix, connector, label)
		childPrefix := prefix
		if !isRoot {
			if isLast {
				childPrefix += "    "
			} else {
				childPrefix += "│   "
			}
		}
		if child.IsFile {
			if depth > 0 && strings.HasSuffix(strings.ToLower(child.Name), ".md") {
				content, _, err := store.Read(namespace, child.File.Filename)
				if err == nil {
					sb.WriteString(renderSections(parseSections([]byte(content)), childPrefix, depth))
				}
			}
		} else {
			sb.WriteString(renderTreeWithSections(child, namespace, store, childPrefix, false, depth, meta))
		}
	}
	return sb.String()
}

func fileMeta(f FileEntry) string {
	date := f.UpdatedAt
	if len(date) >= 10 {
		date = date[:10]
	}
	if f.HasPending {
		return "  [" + date + " • pending]"
	}
	return "  [" + date + "]"
}

// renderSections lays out sections (in source order) as a hierarchical tree.
// `depth` filters which heading levels are emitted — token counts are still
// computed against the unfiltered section list so a dropped sibling between
// two surviving headings doesn't bleed its bytes into a neighbor's count.
func renderSections(sections []section, prefix string, depth int) string {
	if len(sections) == 0 || depth <= 0 {
		return ""
	}
	// Pre-compute ownEnd for every section against the full list. ownEnd is
	// the next heading start at any level — kept stable through depth filtering
	// so token counts remain faithful to the file as written.
	ownEnds := make([]int, len(sections))
	for i := range sections {
		if i+1 < len(sections) {
			ownEnds[i] = sections[i+1].Start
		} else {
			ownEnds[i] = sections[i].End
		}
	}

	type kept struct {
		idx int // original index into sections, for ownEnds lookup
	}
	keep := make([]kept, 0, len(sections))
	for i, s := range sections {
		if keepHeadingAtDepth(s.Level, depth) {
			keep = append(keep, kept{idx: i})
		}
	}
	if len(keep) == 0 {
		return ""
	}

	var sb strings.Builder
	type frame struct {
		level  int
		prefix string
	}
	stack := []frame{{level: 0, prefix: prefix}}

	for k, entry := range keep {
		s := sections[entry.idx]
		// pop until stack top is a strictly-shallower level
		for len(stack) > 1 && stack[len(stack)-1].level >= s.Level {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]

		// last sibling at this level among the remaining KEPT entries
		isLast := true
		for j := k + 1; j < len(keep); j++ {
			lvl := sections[keep[j].idx].Level
			if lvl < s.Level {
				break
			}
			if lvl == s.Level {
				isLast = false
				break
			}
		}
		connector := "├── "
		if isLast {
			connector = "└── "
		}

		// Phase 12a: emit the original heading text as written, not the
		// slug. The slug is an implementation detail useful for `read(section=...)`
		// machinery; humans (and cold Claudes) navigate by heading text.
		// `read(section=...)` accepts heading text directly via the slugify
		// fallback, so the displayed string is callable as-is.
		display := s.Heading
		if display == "" {
			display = s.Slug
		}
		label := strings.Repeat("#", s.Level) + " " + display
		tokens := approxTokens(ownEnds[entry.idx] - s.Start)
		if tokens >= sectionTokenThreshold {
			label += fmt.Sprintf(" (%s tokens)", thousands(tokens))
		}
		fmt.Fprintf(&sb, "%s%s%s\n", parent.prefix, connector, label)

		childPrefix := parent.prefix
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
		stack = append(stack, frame{level: s.Level, prefix: childPrefix})
	}
	return sb.String()
}

func thousands(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
