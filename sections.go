package main

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// treeDepthDefault controls how many section-tree levels `tree` emits when the
// caller doesn't pass `depth`. 1 = `## headings only` — the daily-navigation
// default. depth=99 restores Phase 10's "all heading levels" output.
const treeDepthDefault = 1

// keepHeadingAtDepth implements the spec's depth semantics:
//
//	0     → no headings
//	1..98 → only levels 2..(depth+1)  (H1 suppressed; filename is the title)
//	99+   → all heading levels (H1–H6)
//
// The asymmetric H1 treatment is intentional: by convention the filename serves
// as the page title, so a `# Foo` heading inside the file is usually redundant.
// At full depth (99) we restore the unfiltered view.
func keepHeadingAtDepth(level, depth int) bool {
	if depth <= 0 {
		return false
	}
	if depth >= 99 {
		return true
	}
	return level >= 2 && level <= depth+1
}

func isGlobPattern(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// filterByGlob keeps files whose full slash-delimited filename matches the
// doublestar pattern. Used by `tree(path=...)` when path contains *, ?, or [.
func filterByGlob(files []FileEntry, pattern string) ([]FileEntry, error) {
	out := make([]FileEntry, 0, len(files))
	for _, f := range files {
		match, err := doublestar.Match(pattern, f.Filename)
		if err != nil {
			return nil, err
		}
		if match {
			out = append(out, f)
		}
	}
	return out, nil
}

// filterByPath scopes a tree to `path`. Detection:
//   - contains *, ?, [ → doublestar glob match
//   - otherwise → literal: file exact-match OR folder prefix match (filename starts with path+"/")
//
// Returns an empty slice (not an error) when nothing matches a glob.
func filterByPath(files []FileEntry, path string) ([]FileEntry, error) {
	if isGlobPattern(path) {
		return filterByGlob(files, path)
	}
	prefix := strings.TrimSuffix(path, "/") + "/"
	out := make([]FileEntry, 0, len(files))
	for _, f := range files {
		if f.Filename == path || strings.HasPrefix(f.Filename, prefix) {
			out = append(out, f)
		}
	}
	return out, nil
}

// A section describes one markdown heading and its body span in the source.
// Boundaries are byte-aligned to source so a returned slice matches the file
// verbatim — required for edit round-trips.
type section struct {
	Slug    string
	Heading string
	Level   int
	Start   int // byte offset, line-start of heading
	End     int // byte offset, exclusive: start of next ≤-level heading or len(source)
}

func parseSections(source []byte) []section {
	doc := markdown.Parser().Parse(text.NewReader(source))

	type entry struct {
		h     *ast.Heading
		start int
	}
	var entries []entry

	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		h, ok := c.(*ast.Heading)
		if !ok {
			continue
		}
		lines := h.Lines()
		if lines == nil || lines.Len() == 0 {
			continue
		}
		first := lines.At(0)
		entries = append(entries, entry{h: h, start: lineStartOf(source, first.Start)})
	}

	out := make([]section, 0, len(entries))
	for i, e := range entries {
		end := len(source)
		for j := i + 1; j < len(entries); j++ {
			if entries[j].h.Level <= e.h.Level {
				end = entries[j].start
				break
			}
		}
		var slug string
		if id, ok := e.h.AttributeString("id"); ok {
			if b, ok := id.([]byte); ok {
				// Goldmark generates "phase-0-" for "## Phase 0 ✅" — emoji is
				// stripped and the trailing space turns into a trailing dash.
				// Trim those off so the canonical slug is "phase-0". Dedup
				// suffixes ("-1", "-2") survive because they leave the slug
				// not ending in a dash.
				slug = strings.Trim(string(b), "-")
			}
		}
		heading := string(e.h.Lines().Value(source))
		heading = strings.TrimSpace(heading)
		out = append(out, section{
			Slug:    slug,
			Heading: heading,
			Level:   e.h.Level,
			Start:   e.start,
			End:     end,
		})
	}
	return out
}

func lineStartOf(source []byte, offset int) int {
	if offset > len(source) {
		offset = len(source)
	}
	for i := offset - 1; i >= 0; i-- {
		if source[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

// findSection matches by exact slug first, then by dash-trim (for legacy slugs
// written before Phase 12b normalization), then by slugifying the query as if
// it were heading text. Returns the section if found, else the ordered list of
// canonical slugs for the caller to retry with.
func findSection(sections []section, query string) (*section, []string) {
	for i := range sections {
		if sections[i].Slug == query {
			return &sections[i], nil
		}
	}
	// Legacy slug compatibility: cross-references written before slug
	// normalization may carry trailing dashes like "phase-0-". Strip and retry.
	if trimmed := strings.Trim(query, "-"); trimmed != "" && trimmed != query {
		for i := range sections {
			if sections[i].Slug == trimmed {
				return &sections[i], nil
			}
		}
	}
	// Heading-text fallback: caller may have pasted the displayed heading
	// (which Phase 12a's tree emits) instead of the slug.
	if slug := slugifyHeading(query); slug != "" {
		for i := range sections {
			if sections[i].Slug == slug {
				return &sections[i], nil
			}
		}
	}
	avail := make([]string, 0, len(sections))
	for _, s := range sections {
		if s.Slug != "" {
			avail = append(avail, s.Slug)
		}
	}
	return nil, avail
}

// slugifyHeading mirrors goldmark's auto-heading-id algorithm (without dedup).
// Used to normalize user-provided section queries when the exact-slug lookup misses.
func slugifyHeading(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r > 127:
			// goldmark skips multi-byte runes
			continue
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r == ' ' || r == '\t' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	// Match parseSections' slug-normalization so heading-text fallback lookups
	// produce the same canonical form as stored slugs.
	return strings.Trim(b.String(), "-")
}

// approxTokens matches the UI's chars/4 heuristic.
func approxTokens(n int) int { return n / 4 }

// archivePrefixes are hidden by default but shown when include_archive=true.
// "archived/" is canonical; "archive/" is legacy (some older namespaces use it)
// and is handled transparently without being exposed to users.
var archivePrefixes = []string{"archive/", "archived/"}

// deletedPrefixes are always hidden — include_archive=true does not expose them.
// Only an explicit path targeting deleted/ will surface these files.
var deletedPrefixes = []string{"deleted/"}

// isArchivePath returns true for any hidden prefix (archived or deleted).
// Used for export filtering — we skip both.
func isArchivePath(filename string) bool {
	for _, p := range archivePrefixes {
		if strings.HasPrefix(filename, p) {
			return true
		}
	}
	for _, p := range deletedPrefixes {
		if strings.HasPrefix(filename, p) {
			return true
		}
	}
	return false
}

// pathTargetsArchive returns true if path explicitly scopes into an archive prefix.
// Used to disable archive-exclusion when the caller obviously meant to see archived content.
// Does NOT match deleted/ — that requires an explicit deleted/ path.
func pathTargetsArchive(path string) bool {
	if path == "" {
		return false
	}
	for _, p := range archivePrefixes {
		stripped := strings.TrimSuffix(p, "/")
		if path == p || path == stripped || strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// pathTargetsDeleted returns true if path explicitly scopes into deleted/.
func pathTargetsDeleted(path string) bool {
	if path == "" {
		return false
	}
	for _, p := range deletedPrefixes {
		stripped := strings.TrimSuffix(p, "/")
		if path == p || path == stripped || strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func excludeArchive(files []FileEntry) []FileEntry {
	out := make([]FileEntry, 0, len(files))
	for _, f := range files {
		skip := false
		for _, p := range archivePrefixes {
			if strings.HasPrefix(f.Filename, p) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, f)
		}
	}
	return out
}

func excludeDeleted(files []FileEntry) []FileEntry {
	out := make([]FileEntry, 0, len(files))
	for _, f := range files {
		skip := false
		for _, p := range deletedPrefixes {
			if strings.HasPrefix(f.Filename, p) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, f)
		}
	}
	return out
}
