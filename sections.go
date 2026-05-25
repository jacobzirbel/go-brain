package main

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

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
				slug = string(b)
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

// findSection matches by exact slug first, then by slugifying the query.
// Returns the section if found, else the ordered list of available slugs.
func findSection(sections []section, query string) (*section, []string) {
	for i := range sections {
		if sections[i].Slug == query {
			return &sections[i], nil
		}
	}
	slug := slugifyHeading(query)
	if slug != "" {
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
	return b.String()
}

// approxTokens matches the UI's chars/4 heuristic.
func approxTokens(n int) int { return n / 4 }
