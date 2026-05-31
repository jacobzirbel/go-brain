package main

import (
	"strings"
	"testing"
)

const sampleDoc = `# Top

intro paragraph

## Phase 10 design

resolved 2026-05-25.

### Section addressing

slug canonical.

### Heading rename

explicit miss.

## Other thing

unrelated body
`

func TestParseSections_TopLevel(t *testing.T) {
	secs := parseSections([]byte(sampleDoc))
	if len(secs) != 5 {
		t.Fatalf("expected 5 sections, got %d", len(secs))
	}
	wantSlugs := []string{"top", "phase-10-design", "section-addressing", "heading-rename", "other-thing"}
	for i, want := range wantSlugs {
		if secs[i].Slug != want {
			t.Errorf("section[%d] slug = %q, want %q", i, secs[i].Slug, want)
		}
	}
}

func TestParseSections_Boundaries(t *testing.T) {
	source := []byte(sampleDoc)
	secs := parseSections(source)

	phase10 := secs[1]
	if phase10.Slug != "phase-10-design" {
		t.Fatalf("setup: wrong section, got %q", phase10.Slug)
	}
	body := string(source[phase10.Start:phase10.End])
	if !strings.HasPrefix(body, "## Phase 10 design") {
		t.Errorf("body should start with heading line; got %q", body[:min(40, len(body))])
	}
	if !strings.Contains(body, "### Section addressing") {
		t.Errorf("H2 section should contain its H3 children; body=%q", body)
	}
	if strings.Contains(body, "## Other thing") {
		t.Errorf("H2 section should not include the next H2; body=%q", body)
	}
}

func TestParseSections_LeafSection(t *testing.T) {
	source := []byte(sampleDoc)
	secs := parseSections(source)

	var found *section
	for i := range secs {
		if secs[i].Slug == "section-addressing" {
			found = &secs[i]
			break
		}
	}
	if found == nil {
		t.Fatal("section-addressing not found")
	}
	body := string(source[found.Start:found.End])
	if !strings.HasPrefix(body, "### Section addressing") {
		t.Errorf("expected H3 heading line; got %q", body)
	}
	if strings.Contains(body, "### Heading rename") {
		t.Errorf("H3 section bleeds into sibling H3: %q", body)
	}
}

func TestParseSections_VerbatimRoundTrip(t *testing.T) {
	source := []byte(sampleDoc)
	secs := parseSections(source)
	for _, s := range secs {
		slice := source[s.Start:s.End]
		if !strings.Contains(string(source), string(slice)) {
			t.Errorf("section %q slice is not a verbatim substring of source", s.Slug)
		}
	}
}

func TestParseSections_DuplicateHeadings(t *testing.T) {
	src := []byte("# Foo\n\n## Bar\n\nbody\n\n## Bar\n\nother\n")
	secs := parseSections(src)
	if len(secs) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(secs))
	}
	if secs[1].Slug != "bar" || secs[2].Slug != "bar-1" {
		t.Errorf("expected dedup slugs bar/bar-1, got %q/%q", secs[1].Slug, secs[2].Slug)
	}
}

func TestParseSections_EmptyDocument(t *testing.T) {
	if got := parseSections([]byte("")); len(got) != 0 {
		t.Errorf("expected no sections for empty doc, got %v", got)
	}
	if got := parseSections([]byte("just a paragraph\n")); len(got) != 0 {
		t.Errorf("expected no sections for doc without headings, got %v", got)
	}
}

func TestFindSection_BySlug(t *testing.T) {
	secs := parseSections([]byte(sampleDoc))
	found, avail := findSection(secs, "phase-10-design")
	if found == nil {
		t.Fatalf("expected slug hit; available=%v", avail)
	}
	if found.Slug != "phase-10-design" {
		t.Errorf("got slug %q", found.Slug)
	}
}

func TestFindSection_ByHeadingText(t *testing.T) {
	secs := parseSections([]byte(sampleDoc))
	found, _ := findSection(secs, "Phase 10 design")
	if found == nil || found.Slug != "phase-10-design" {
		t.Errorf("heading-text fallback failed: %v", found)
	}
}

func TestFindSection_Miss(t *testing.T) {
	secs := parseSections([]byte(sampleDoc))
	found, avail := findSection(secs, "nonexistent")
	if found != nil {
		t.Error("expected miss")
	}
	if len(avail) != 5 {
		t.Errorf("expected 5 available slugs, got %d: %v", len(avail), avail)
	}
}

func TestSlugifyHeading(t *testing.T) {
	cases := map[string]string{
		"Phase 10 design":      "phase-10-design",
		"  Trim me  ":          "trim-me",
		"Punctuation!?":        "punctuation",
		"snake_and-dash":       "snake-and-dash",
		"UPPER lower":          "upper-lower",
	}
	for in, want := range cases {
		if got := slugifyHeading(in); got != want {
			t.Errorf("slugifyHeading(%q) = %q, want %q", in, got, want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── read tool: section semantics ─────────────────────────────────────────────

const sectionDoc = `# Top

intro

## Phase 10 design

resolved.

## Other thing

unrelated
`

func TestReadTool_FullFile(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionDoc)
	res, isErr := runTool(s, "read", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
	})
	if isErr {
		t.Fatalf("unexpected error: %v", res)
	}
	m := res.(map[string]string)
	if m["content"] != sectionDoc {
		t.Errorf("full-file content mismatch")
	}
}

func TestReadTool_BySection(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionDoc)
	res, isErr := runTool(s, "read", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "phase-10-design",
	})
	if isErr {
		t.Fatalf("unexpected error: %v", res)
	}
	m := res.(map[string]any)
	content := m["content"].(string)
	if !strings.HasPrefix(content, "## Phase 10 design") {
		t.Errorf("section content should start at heading: %q", content)
	}
	if strings.Contains(content, "## Other thing") {
		t.Errorf("section content leaked into next H2: %q", content)
	}
}

func TestReadTool_SectionRoundTripsThroughEdit(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionDoc)

	res, _ := runTool(s, "read", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "phase-10-design",
	})
	sectionBytes := res.(map[string]any)["content"].(string)

	newSection := strings.Replace(sectionBytes, "resolved.", "resolved & shipped.", 1)
	_, isErr := runTool(s, "edit", map[string]any{
		"namespace":  "ns",
		"filename":   "doc.md",
		"old_str": sectionBytes,
		"new_str": newSection,
	})
	if isErr {
		t.Fatalf("section bytes did not round-trip through edit: %v", res)
	}
	got, _, _ := s.Read("ns", "doc.md")
	if !strings.Contains(got, "resolved & shipped.") {
		t.Errorf("edited content not in file: %q", got)
	}
	if !strings.Contains(got, "## Other thing") {
		t.Errorf("edit clobbered unrelated section: %q", got)
	}
}

func TestReadTool_SectionMissReturnsAvailable(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionDoc)
	res, isErr := runTool(s, "read", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "nonexistent",
	})
	if !isErr {
		t.Fatalf("expected miss to error; got %v", res)
	}
	m := res.(map[string]any)
	avail, ok := m["available"].([]string)
	if !ok {
		t.Fatalf("expected available list, got %v (type %T)", m["available"], m["available"])
	}
	if len(avail) != 3 {
		t.Errorf("expected 3 available slugs, got %d: %v", len(avail), avail)
	}
}

func TestReadTool_SectionByHeadingText(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionDoc)
	res, isErr := runTool(s, "read", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "Phase 10 design",
	})
	if isErr {
		t.Fatalf("heading-text fallback failed: %v", res)
	}
	m := res.(map[string]any)
	if m["section"].(string) != "phase-10-design" {
		t.Errorf("expected canonical slug returned, got %q", m["section"])
	}
}

// ── Phase 12b: slug normalization + legacy compatibility ─────────────────────

func TestParseSections_TrimsTrailingDashesFromSlug(t *testing.T) {
	// Goldmark generates "phase-0-" for "## Phase 0 ✅" — the emoji strips
	// and the trailing space turns into a trailing dash. Phase 12b trims those.
	src := []byte("## Phase 0 ✅\n\nshipped\n\n## Phase 1 ❌\n\nbroken\n")
	secs := parseSections(src)
	if len(secs) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(secs))
	}
	for _, s := range secs {
		if strings.HasSuffix(s.Slug, "-") || strings.HasPrefix(s.Slug, "-") {
			t.Errorf("slug %q has unexpected leading/trailing dash", s.Slug)
		}
	}
	if secs[0].Slug != "phase-0" {
		t.Errorf("expected first slug 'phase-0', got %q", secs[0].Slug)
	}
}

func TestSlugifyHeading_NormalizesTrailingDashes(t *testing.T) {
	// Heading-text fallback must match the normalized stored slug.
	cases := map[string]string{
		"Phase 0 ✅":     "phase-0",
		"Phase 1 ❌":     "phase-1",
		"-leading":       "leading",
		"trailing-":      "trailing",
		"-both-":         "both",
		"-- double --":   "double",
	}
	for in, want := range cases {
		if got := slugifyHeading(in); got != want {
			t.Errorf("slugifyHeading(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadTool_LegacySlugWithTrailingDash(t *testing.T) {
	// Cross-references written before Phase 12b carry slugs like "phase-0-".
	// They must still resolve after normalization.
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", "## Phase 0 ✅\n\nshipped\n")

	res, isErr := runTool(s, "read", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "phase-0-",
	})
	if isErr {
		t.Fatalf("legacy slug with trailing dash should resolve; got %v", res)
	}
	m := res.(map[string]any)
	if m["section"].(string) != "phase-0" {
		t.Errorf("expected canonical slug 'phase-0' returned, got %q", m["section"])
	}
}

func TestReadTool_HeadingTextWithEmojiResolves(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", "## Phase 0 ✅\n\nshipped\n")

	res, isErr := runTool(s, "read", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "Phase 0 ✅",
	})
	if isErr {
		t.Fatalf("emoji heading text should resolve; got %v", res)
	}
	m := res.(map[string]any)
	if m["section"].(string) != "phase-0" {
		t.Errorf("expected canonical slug 'phase-0' returned, got %q", m["section"])
	}
}
