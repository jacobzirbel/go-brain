package main

import (
	"errors"
	"fmt"
	"strings"
)

const filenameDescription = "Name of the file. Slashes create folders for display purposes, e.g. \"journal/2026-05-23.md\" or \"projects/website/notes.md\"."

const commentDescription = "Optional. Short explanation of why this change was made. Use when the change isn't self-evident from the diff."

var mcpTools = []map[string]any{
	{
		"name":        "read",
		"description": "cat <filename> — get the contents of a file in a namespace. Pass `section` to return just that section's bytes verbatim.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string", "description": "Namespace for the file"},
				"filename":  map[string]any{"type": "string", "description": filenameDescription},
				"section":   map[string]any{"type": "string", "description": "Optional. Accepts a canonical slug (\"phase-10-design\") or the heading text as written (\"Phase 10 design\"). On miss the error lists available slugs in source order."},
			},
			"required": []string{"namespace", "filename"},
		},
	},
	{
		"name":        "write",
		"description": "Overwrite a file in a namespace (creates if not exists).",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"filename":  map[string]any{"type": "string", "description": filenameDescription},
				"content":   map[string]any{"type": "string"},
				"comment":   map[string]any{"type": "string", "description": commentDescription},
			},
			"required": []string{"namespace", "filename", "content"},
		},
	},
	{
		"name":        "append",
		"description": "Append text to a file in a namespace (creates if not exists; inserts a newline first if the file is non-empty).",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"filename":  map[string]any{"type": "string", "description": filenameDescription},
				"content":   map[string]any{"type": "string"},
				"comment":   map[string]any{"type": "string", "description": commentDescription},
			},
			"required": []string{"namespace", "filename", "content"},
		},
	},
	{
		"name":        "edit",
		"description": "Replace `old_str` with `new_str` in the named file. Fails if `old_str` is missing or matches more than once. Pair with `read(section=...)` for section-scoped edits — the returned bytes are verbatim and round-trip safely.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"filename":  map[string]any{"type": "string", "description": filenameDescription},
				"old_str":   map[string]any{"type": "string", "description": "Exact bytes to find. Must occur exactly once."},
				"new_str":   map[string]any{"type": "string", "description": "Replacement bytes."},
				"comment":   map[string]any{"type": "string", "description": commentDescription},
			},
			"required": []string{"namespace", "filename", "old_str", "new_str"},
		},
	},
	{
		"name":        "namespaces",
		"description": "List all namespaces that contain at least one file.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		"name":        "tree",
		"description": "Folder structure of a namespace, with markdown headings surfaced as nested section nodes under each .md file. Section labels show the original heading text; pass that text — or the slug — to `read(section=...)`.\n\nDEFAULTS:\n- Root files expand to ## sections (depth=1).\n- Sub-folders collapse to \"name/ (N items)\" — pass `path=\"name/\"` to expand one.\n- archived/ is hidden unless include_archive=true or a path targets it; deleted/ is always hidden unless a path explicitly targets it.\n\nPARAMS:\n- `path`: literal file (\"decisions.md\"), folder (\"tasks/\"), or doublestar glob (\"**/decisions.md\"). Globs bypass folder collapse so matches render in full.\n- `depth`: 0=files only; 1=## (default); 2=## and ###; N=up to level N+1; 99=all headings and full folder recursion.\n- `include_archive`: include archived/ (not deleted/).\n- `meta`: annotate each file with last-modified date and pending flag.\n\nSections ≥400 approx tokens are annotated with their token count.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace":       map[string]any{"type": "string"},
				"path":            map[string]any{"type": "string", "description": "Optional literal filename, folder prefix (\"tasks/\"), or doublestar glob (\"**/decisions.md\", \"decisions/*.md\")."},
				"depth":           map[string]any{"type": "integer", "description": "Section heading depth. 0/1/2/.../99 — see tool description."},
				"include_archive": map[string]any{"type": "boolean", "description": "Include the archived/ prefix. Defaults to false. Does not include deleted/."},
				"meta":            map[string]any{"type": "boolean", "description": "Annotate each file with updated_at date and pending status. Defaults to false."},
			},
			"required": []string{"namespace"},
		},
	},
	{
		"name":        "copy",
		"description": "Copy a file. If new_filename ends with '/', the source basename is appended (src=\"a/b.md\", new_filename=\"archived/\" → \"archived/b.md\"). Fails if the destination exists.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace":     map[string]any{"type": "string", "description": "Source namespace"},
				"filename":      map[string]any{"type": "string", "description": "Source filename"},
				"new_namespace": map[string]any{"type": "string", "description": "Destination namespace (defaults to source)"},
				"new_filename":  map[string]any{"type": "string", "description": "Destination filename. Trailing '/' copies into a folder using the source basename."},
			},
			"required": []string{"namespace", "filename", "new_filename"},
		},
	},
	{
		"name":        "archive",
		"description": "Move a file to the archived/ prefix, excluded from search and list by default. Use for files you may reread but don't need day-to-day. Pass include_archive=true to see them again.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"filename":  map[string]any{"type": "string"},
			},
			"required": []string{"namespace", "filename"},
		},
	},
	{
		"name":        "remove",
		"description": "Delete a file",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"filename":  map[string]any{"type": "string"},
			},
			"required": []string{"namespace", "filename"},
		},
	},
	{
		"name":        "move",
		"description": "Rename or move a file. Destination may be a different namespace and/or folder (use slashes to change folders). Fails if the destination exists.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace":     map[string]any{"type": "string", "description": "Source namespace"},
				"filename":      map[string]any{"type": "string", "description": "Source filename"},
				"new_namespace": map[string]any{"type": "string", "description": "Destination namespace (defaults to source)"},
				"new_filename":  map[string]any{"type": "string", "description": "Destination filename, may contain slashes"},
			},
			"required": []string{"namespace", "filename", "new_filename"},
		},
	},
	{
		"name":        "search",
		"description": "Full-text search within a single namespace; returns matching files with snippet previews. FTS5 syntax: loose keyword by default, \"double quotes\" for phrase, AND/OR/NOT for boolean. Also matches a file's basename, not its folder path. Use `path` for glob-scoped search. Excludes archived/ (pass include_archive=true)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace":       map[string]any{"type": "string", "description": "Namespace to search. Empty or \"*\" is an error — no cross-namespace search."},
				"query":           map[string]any{"type": "string", "description": "FTS5 MATCH expression. Loose keyword by default; \"phrase\" for adjacency; AND/OR/NOT for boolean."},
				"path":            map[string]any{"type": "string", "description": "Optional doublestar glob over filename, e.g. \"tasks/**\" or \"decisions/*.md\"."},
				"limit":           map[string]any{"type": "integer", "description": "Page size. Default 20, hard cap 100."},
				"order":           map[string]any{"type": "string", "description": "\"bm25\" (default, relevance) or \"recency\" (updated_at DESC)."},
				"include_archive": map[string]any{"type": "boolean", "description": "Include the archived/ prefix. Defaults to false."},
			},
			"required": []string{"namespace", "query"},
		},
	},
	// {
	// 	"name":        "comments",
	// 	"description": "List edit-changelog comments on a file (or across a namespace if filename is omitted). Returns rows newest first. Defaults to unreviewed only, 20 most recent.",
	// 	"inputSchema": map[string]any{
	// 		"type": "object",
	// 		"properties": map[string]any{
	// 			"namespace":        map[string]any{"type": "string"},
	// 			"filename":         map[string]any{"type": "string", "description": "Optional. Scope to one file."},
	// 			"include_reviewed": map[string]any{"type": "boolean", "description": "Include reviewed comments. Defaults to false."},
	// 			"limit":            map[string]any{"type": "integer", "description": "Page size (default 20)."},
	// 			"offset":           map[string]any{"type": "integer", "description": "Pagination offset (default 0)."},
	// 		},
	// 		"required": []string{"namespace"},
	// 	},
	// },
	// {
	// 	"name":        "review",
	// 	"description": "Accept the pending change on a file: copy `new` → `old`, clear `new`, mark this file's open comments reviewed. Errors if the file has no pending change.",
	// 	"inputSchema": map[string]any{
	// 		"type": "object",
	// 		"properties": map[string]any{
	// 			"namespace": map[string]any{"type": "string"},
	// 			"filename":  map[string]any{"type": "string", "description": filenameDescription},
	// 		},
	// 		"required": []string{"namespace", "filename"},
	// 	},
	// },
	// {
	// 	"name":        "reject",
	// 	"description": "Revert the pending change on a file: clear `new`, mark this file's open comments reviewed. The content reverts to the last reviewed `old`. Errors if the file has no pending change.",
	// 	"inputSchema": map[string]any{
	// 		"type": "object",
	// 		"properties": map[string]any{
	// 			"namespace": map[string]any{"type": "string"},
	// 			"filename":  map[string]any{"type": "string", "description": filenameDescription},
	// 		},
	// 		"required": []string{"namespace", "filename"},
	// 	},
	// },
	{
		"name":        "move_many",
		"description": "mv (batch) - Rename or move multiple files atomically in a single transaction. If any move fails (e.g. destination already exists, source not found), all moves are rolled back.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"moves": map[string]any{
					"type":        "array",
					"description": "Array of move operations to perform atomically.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"namespace":     map[string]any{"type": "string", "description": "Source namespace"},
							"filename":      map[string]any{"type": "string", "description": "Source filename"},
							"new_namespace": map[string]any{"type": "string", "description": "Destination namespace (defaults to source namespace if omitted)"},
							"new_filename":  map[string]any{"type": "string", "description": "Destination filename"},
						},
						"required": []string{"namespace", "filename", "new_filename"},
					},
				},
			},
			"required": []string{"moves"},
		},
	},
}

func intArg(args map[string]any, key string, def int) int {
	v, ok := args[key]
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

func runTool(store Store, name string, args map[string]any) (result any, isError bool) {
	str := func(key string) string {
		v, _ := args[key].(string)
		return v
	}
	nsStr := func(key string) string {
		return strings.ToLower(str(key))
	}

	switch name {
	case "read":
		ns := nsStr("namespace")
		if ns == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		content, updatedAt, err := store.Read(ns, str("filename"))
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "not found"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		if sectionQ := str("section"); sectionQ != "" {
			sections := parseSections([]byte(content))
			found, avail := findSection(sections, sectionQ)
			if found == nil {
				return map[string]any{"error": "section not found", "available": avail}, true
			}
			return map[string]any{
				"content":    content[found.Start:found.End],
				"updated_at": updatedAt,
				"section":    found.Slug,
			}, false
		}
		return map[string]string{"content": content, "updated_at": updatedAt}, false

	case "edit":
		ns := nsStr("namespace")
		if ns == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		filename := str("filename")
		oldStr := normalizeLineEndings(str("old_str"))
		newStr := normalizeLineEndings(str("new_str"))
		if oldStr == "" {
			return map[string]string{"error": "old_str cannot be empty"}, true
		}
		content, _, err := store.Read(ns, filename)
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "not found"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		count := strings.Count(content, oldStr)
		if count == 0 {
			return map[string]string{"error": "old_str not found in file"}, true
		}
		if count > 1 {
			return map[string]any{"error": fmt.Sprintf("old_str matches %d times; must be unique", count), "matches": count}, true
		}
		if err := store.Write(ns, filename, strings.Replace(content, oldStr, newStr, 1)); err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		if c := str("comment"); c != "" {
			if err := store.InsertComment(ns, filename, c); err != nil {
				return map[string]string{"error": err.Error()}, true
			}
		}
		return map[string]bool{"ok": true}, false

	case "write":
		ns := nsStr("namespace")
		if ns == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		filename := str("filename")
		if _, present := args["content"]; !present || str("content") == "" {
			return map[string]string{"error": "missing required argument: content"}, true
		}
		if err := store.Write(ns, filename, str("content")); err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		if c := str("comment"); c != "" {
			if err := store.InsertComment(ns, filename, c); err != nil {
				return map[string]string{"error": err.Error()}, true
			}
		}
		return map[string]bool{"ok": true}, false

	case "append":
		ns := nsStr("namespace")
		if ns == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		filename := str("filename")
		if _, present := args["content"]; !present || str("content") == "" {
			return map[string]string{"error": "missing required argument: content"}, true
		}
		if err := store.Append(ns, filename, str("content")); err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		if c := str("comment"); c != "" {
			if err := store.InsertComment(ns, filename, c); err != nil {
				return map[string]string{"error": err.Error()}, true
			}
		}
		return map[string]bool{"ok": true}, false

	case "comments":
		ns := nsStr("namespace")
		if ns == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		filename := str("filename")
		includeReviewed, _ := args["include_reviewed"].(bool)
		limit := intArg(args, "limit", 20)
		offset := intArg(args, "offset", 0)
		rows, err := store.ListComments(ns, filename, includeReviewed, limit, offset)
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]any{"comments": rows}, false

	case "review":
		ns := nsStr("namespace")
		if ns == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		err := store.Review(ns, str("filename"))
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "not found"}, true
		}
		if errors.Is(err, ErrNoPending) {
			return map[string]string{"error": "no pending changes"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "reject":
		ns := nsStr("namespace")
		if ns == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		err := store.Reject(ns, str("filename"))
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "not found"}, true
		}
		if errors.Is(err, ErrNoPending) {
			return map[string]string{"error": "no pending changes"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "namespaces":
		nsList, err := store.ListNamespaces()
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]any{"namespaces": nsList}, false

	case "tree":
		ns := nsStr("namespace")
		if ns == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		files, err := store.List(ns)
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		path := str("path")
		includeArchive, _ := args["include_archive"].(bool)
		// deleted/ is always hidden unless path explicitly targets it.
		if !pathTargetsDeleted(path) {
			files = excludeDeleted(files)
		}
		// archive/archived/ are hidden by default; include_archive=true or an
		// explicit archive path overrides this.
		if !includeArchive && !pathTargetsArchive(path) {
			files = excludeArchive(files)
		}

		depth := treeDepthDefault
		if d, ok := args["depth"]; ok {
			switch v := d.(type) {
			case float64:
				depth = int(v)
			case int:
				depth = v
			}
		}

		meta, _ := args["meta"].(bool)
		globMode := isGlobPattern(path)
		if path != "" {
			files, err = filterByPath(files, path)
			if err != nil {
				return map[string]string{"error": fmt.Sprintf("invalid path: %v", err)}, true
			}
		}
		tree := buildTree(files)

		// Deep recursion when explicitly requested (depth=99) or for glob
		// scopes (matches can be sparse across folders; collapsing them as
		// "(N items)" would hide the user's actual query result).
		var out string
		if depth >= 99 || globMode {
			out = renderTreeWithSections(tree, ns, store, "", true, depth, meta)
		} else {
			// scopePath is empty for namespace-root; otherwise the literal
			// folder/file path. (Glob already routed above.)
			out = renderTreeShallow(tree, path, ns, store, depth, meta)
		}
		return map[string]string{"tree": strings.TrimRight(out, "\n")}, false

	case "archive":
		srcNS := nsStr("namespace")
		if srcNS == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		srcFile := str("filename")
		dstFile := "archived/" + srcFile
		err := store.Move(srcNS, srcFile, srcNS, dstFile)
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "not found"}, true
		}
		if errors.Is(err, ErrDestinationExists) {
			return map[string]string{"error": "destination already exists"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "remove":
		srcNS := nsStr("namespace")
		if srcNS == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		srcFile := str("filename")
		dstFile := "deleted/" + srcFile
		err := store.Move(srcNS, srcFile, srcNS, dstFile)
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "not found"}, true
		}
		if errors.Is(err, ErrDestinationExists) {
			return map[string]string{"error": "destination already exists"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "copy":
		srcNS := nsStr("namespace")
		if srcNS == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		dstNS := nsStr("new_namespace")
		if dstNS == "" {
			dstNS = srcNS
		}
		dstFile := str("new_filename")
		if strings.HasSuffix(dstFile, "/") {
			parts := strings.Split(str("filename"), "/")
			dstFile += parts[len(parts)-1]
		}
		err := store.Copy(srcNS, str("filename"), dstNS, dstFile)
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "source not found"}, true
		}
		if errors.Is(err, ErrDestinationExists) {
			return map[string]string{"error": "destination already exists"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "move":
		srcNS := nsStr("namespace")
		if srcNS == "" {
			return map[string]string{"error": "missing required argument: namespace"}, true
		}
		dstNS := nsStr("new_namespace")
		if dstNS == "" {
			dstNS = srcNS
		}
		err := store.Move(srcNS, str("filename"), dstNS, str("new_filename"))
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "source not found"}, true
		}
		if errors.Is(err, ErrDestinationExists) {
			return map[string]string{"error": "destination already exists"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "move_many":
		raw, _ := args["moves"].([]any)
		ops := make([]MoveOp, 0, len(raw))
		for i, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				return map[string]string{"error": fmt.Sprintf("moves[%d] is not an object", i)}, true
			}
			get := func(k string) string {
				v, _ := m[k].(string)
				return v
			}
			srcNS := strings.ToLower(get("namespace"))
			dstNS := strings.ToLower(get("new_namespace"))
			if dstNS == "" {
				dstNS = srcNS
			}
			ops = append(ops, MoveOp{
				SrcNamespace: srcNS,
				SrcFilename:  get("filename"),
				DstNamespace: dstNS,
				DstFilename:  get("new_filename"),
			})
		}
		err := store.MoveMany(ops)
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "a source file was not found; no moves were applied"}, true
		}
		if errors.Is(err, ErrDestinationExists) {
			return map[string]string{"error": "a destination already exists; no moves were applied"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]any{"ok": true, "moved": len(ops)}, false

	case "search":
		ns := nsStr("namespace")
		if ns == "" || ns == "*" {
			return map[string]string{"error": "namespace required, cross-namespace search not supported"}, true
		}
		query := str("query")
		if query == "" {
			return map[string]string{"error": "query required"}, true
		}
		includeArchive, _ := args["include_archive"].(bool)
		hits, err := store.Search(SearchOptions{
			Namespace:      ns,
			Query:          query,
			Path:           str("path"),
			Limit:          intArg(args, "limit", 20),
			Order:          str("order"),
			IncludeArchive: includeArchive,
		})
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]any{"results": hits}, false

	default:
		return map[string]string{"error": fmt.Sprintf("unknown tool: %s", name)}, true
	}
}

// sectionTokenThreshold controls when section nodes get a token-count annotation
// in tree output. Below this, the slug alone is shown; at-or-above, "(Nk)" surfaces
// kitchen-sink sections without bloating the rest of the tree.
const sectionTokenThreshold = 400

// renderTreeShallow is the Phase 11.1 default: it descends to `scopePath`
// (empty = namespace root) and renders that folder's direct children with
// `ls`-like semantics — files expand with sections, sub-folders collapse to
// "name/ (N items)" summaries. depth=99 or glob path-mode routes through
// renderTreeWithSections instead for full recursion.
func renderTreeShallow(root *treeNode, scopePath, namespace string, store Store, depth int, meta bool) string {
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
func renderTreeWithSections(node *treeNode, namespace string, store Store, prefix string, isRoot bool, depth int, meta bool) string {
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
