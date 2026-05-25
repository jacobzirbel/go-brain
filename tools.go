package main

import (
	"errors"
	"fmt"
	"strings"
)

const filenameDescription = "Name of the file. Slashes create folders for display purposes, e.g. \"journal/2026-05-23.md\" or \"projects/website/notes.md\"."

var mcpTools = []map[string]any{
	{
		"name":        "read",
		"description": "cat <filename> — get the contents of a file in a namespace. Pass `section` (heading slug or text) to return just that section.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string", "description": "Namespace for the file"},
				"filename":  map[string]any{"type": "string", "description": filenameDescription},
				"section":   map[string]any{"type": "string", "description": "Optional: a heading slug (e.g. \"phase-10-design\") or heading text. Returns just that section's bytes verbatim. On miss, the error response includes the list of available slugs in source order."},
			},
			"required": []string{"namespace", "filename"},
		},
	},
	{
		"name":        "write",
		"description": "Overwrite a file in a namespace (creates if not exists)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"filename":  map[string]any{"type": "string", "description": filenameDescription},
				"content":   map[string]any{"type": "string"},
			},
			"required": []string{"namespace", "filename", "content"},
		},
	},
	{
		"name":        "append",
		"description": "Append text to a file in a namespace (creates if not exists; adds newline before new content if file is non-empty)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"filename":  map[string]any{"type": "string", "description": filenameDescription},
				"content":   map[string]any{"type": "string"},
			},
			"required": []string{"namespace", "filename", "content"},
		},
	},
	{
		"name":        "edit",
		"description": "Surgical edit: replace `old_string` with `new_string` in the named file. Fails if `old_string` is missing or matches more than once. Compose with `read(section=...)` for section-scoped edits — the returned section bytes are verbatim and round-trip safely.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace":  map[string]any{"type": "string"},
				"filename":   map[string]any{"type": "string", "description": filenameDescription},
				"old_string": map[string]any{"type": "string", "description": "Exact bytes to find. Must occur exactly once in the file."},
				"new_string": map[string]any{"type": "string", "description": "Replacement bytes."},
			},
			"required": []string{"namespace", "filename", "old_string", "new_string"},
		},
	},
	{
		"name":        "list",
		"description": "ls - List files in a namespace. Filenames may contain slashes to indicate folders. Pass `pattern` to filter by doublestar glob (e.g. \"**/*.md\", \"decisions/*.md\").",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"pattern":   map[string]any{"type": "string", "description": "Optional doublestar glob filter (e.g. \"**/*.md\", \"decisions/*.md\"). Match is against the full slash-delimited filename. Omit for the full list."},
			},
			"required": []string{"namespace"},
		},
	},
	{
		"name":        "tree",
		"description": "tree - Return the folder structure of a namespace as a readable tree. Slashes in filenames are treated as folder separators. For `.md` files, headings are surfaced as nested section nodes (canonical slug shown). Sections with ≥400 approx tokens are annotated with token counts to surface kitchen-sink sections. `depth` defaults to 1 (## only); pass `depth=99` for the full heading nesting. `path` accepts a literal subtree (\"decisions.md\", \"tasks/\") or a doublestar glob (\"**/decisions.md\", \"tasks/**/index.md\"); matched files render with their section subtrees grouped under the namespace tree.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"path":      map[string]any{"type": "string", "description": "Optional literal filename, folder prefix (\"tasks/\"), or doublestar glob (\"**/decisions.md\"). Glob detected by presence of *, ?, or [."},
				"depth":     map[string]any{"type": "integer", "description": "Section heading depth. 0=files only; 1=## only (default); 2=## and ###; N=headings up to level N+1; 99=all heading levels."},
			},
			"required": []string{"namespace"},
		},
	},
	{
		"name":        "copy",
		"description": "cp - Copy a file. If new_filename ends with '/', the source basename is appended (e.g. src=\"a/b.md\", new_filename=\"archive/\" → \"archive/b.md\"). Fails if the destination already exists.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace":     map[string]any{"type": "string", "description": "Source namespace"},
				"filename":      map[string]any{"type": "string", "description": "Source filename"},
				"new_namespace": map[string]any{"type": "string", "description": "Destination namespace (defaults to source namespace if omitted)"},
				"new_filename":  map[string]any{"type": "string", "description": "Destination filename. Append '/' to copy into a folder using the source basename."},
			},
			"required": []string{"namespace", "filename", "new_filename"},
		},
	},
	{
		"name":        "archive",
		"description": "Archive a file by moving it under the archived/ prefix.",
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
		"description": "rm - Remove a file.",
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
		"description": "mv - Rename or move a file. The destination can be in a different namespace and/or a different folder (use slashes in the filename to change folders). Fails if the destination already exists.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace":     map[string]any{"type": "string", "description": "Source namespace"},
				"filename":      map[string]any{"type": "string", "description": "Source filename"},
				"new_namespace": map[string]any{"type": "string", "description": "Destination namespace (defaults to source namespace if omitted)"},
				"new_filename":  map[string]any{"type": "string", "description": "Destination filename, may contain slashes"},
			},
			"required": []string{"namespace", "filename", "new_filename"},
		},
	},
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

func runTool(store Store, name string, args map[string]any) (result any, isError bool) {
	str := func(key string) string {
		v, _ := args[key].(string)
		return v
	}

	switch name {
	case "read":
		content, updatedAt, err := store.Read(str("namespace"), str("filename"))
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
		ns := str("namespace")
		filename := str("filename")
		oldStr := str("old_string")
		newStr := str("new_string")
		if oldStr == "" {
			return map[string]string{"error": "old_string cannot be empty"}, true
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
			return map[string]string{"error": "old_string not found in file"}, true
		}
		if count > 1 {
			return map[string]any{"error": fmt.Sprintf("old_string matches %d times; must be unique", count), "matches": count}, true
		}
		if err := store.Write(ns, filename, strings.Replace(content, oldStr, newStr, 1)); err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "write":
		if err := store.Write(str("namespace"), str("filename"), str("content")); err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "append":
		if err := store.Append(str("namespace"), str("filename"), str("content")); err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "list":
		files, err := store.List(str("namespace"))
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		if pattern := str("pattern"); pattern != "" {
			filtered, err := filterByGlob(files, pattern)
			if err != nil {
				return map[string]string{"error": fmt.Sprintf("invalid glob pattern: %v", err)}, true
			}
			files = filtered
		}
		return map[string]any{"files": files}, false

	case "tree":
		ns := str("namespace")
		files, err := store.List(ns)
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		if path := str("path"); path != "" {
			files, err = filterByPath(files, path)
			if err != nil {
				return map[string]string{"error": fmt.Sprintf("invalid path: %v", err)}, true
			}
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
		tree := buildTree(files)
		return map[string]string{"tree": strings.TrimRight(renderTreeWithSections(tree, ns, store, "", true, depth), "\n")}, false

	case "archive":
		srcNS := str("namespace")
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
		srcNS := str("namespace")
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
		srcNS := str("namespace")
		dstNS := str("new_namespace")
		if dstNS == "" {
			dstNS = srcNS
		}
		dstFile := str("new_filename")
		if strings.HasSuffix(dstFile, "/") {
			parts := strings.Split(str("filename"), "/")
			dstFile += parts[len(parts)-1]
		}
		content, _, err := store.Read(srcNS, str("filename"))
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "source not found"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		_, _, existErr := store.Read(dstNS, dstFile)
		if existErr == nil {
			return map[string]string{"error": "destination already exists"}, true
		}
		if !errors.Is(existErr, ErrNotFound) {
			return map[string]string{"error": existErr.Error()}, true
		}
		if err := store.Write(dstNS, dstFile, content); err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "move":
		dstNS := str("new_namespace")
		if dstNS == "" {
			dstNS = str("namespace")
		}
		err := store.Move(str("namespace"), str("filename"), dstNS, str("new_filename"))
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
			srcNS := get("namespace")
			dstNS := get("new_namespace")
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

	default:
		return map[string]string{"error": fmt.Sprintf("unknown tool: %s", name)}, true
	}
}

// sectionTokenThreshold controls when section nodes get a token-count annotation
// in tree output. Below this, the slug alone is shown; at-or-above, "(Nk)" surfaces
// kitchen-sink sections without bloating the rest of the tree.
const sectionTokenThreshold = 400

// renderTreeWithSections renders the file/folder tree and, for `.md` files,
// surfaces markdown headings as nested section nodes. `depth` caps which
// heading levels appear — see keepHeadingAtDepth. depth=0 short-circuits the
// per-file content read entirely.
func renderTreeWithSections(node *treeNode, namespace string, store Store, prefix string, isRoot bool, depth int) string {
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
			sb.WriteString(renderTreeWithSections(child, namespace, store, childPrefix, false, depth))
		}
	}
	return sb.String()
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

		label := strings.Repeat("#", s.Level) + " " + s.Slug
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

