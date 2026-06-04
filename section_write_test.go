package main

import (
	"strings"
	"testing"
)

// doc used by most section-write tests:
//
//	# Doc
//
//	## Alpha
//	alpha body
//
//	### Nested
//	nested body
//
//	## Beta
//	beta body
const sectionWriteDoc = `# Doc

## Alpha

alpha body

### Nested

nested body

## Beta

beta body
`

// ── append_to_section ────────────────────────────────────────────────────────

func TestAppendToSection_ExistingSection(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionWriteDoc)

	res, isErr := runTool(s, "append_to_section", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "beta",
		"content":   "new line",
	})
	if isErr {
		t.Fatalf("expected success, got %v", res)
	}
	m := res.(map[string]any)
	if m["section"].(string) != "beta" {
		t.Errorf("returned slug = %q, want %q", m["section"], "beta")
	}
	got, _, _ := s.Read("ns", "doc.md")
	if !strings.Contains(got, "## Beta") {
		t.Error("Beta heading should still exist")
	}
	if !strings.Contains(got, "new line") {
		t.Error("appended content not found in file")
	}
	// Alpha must not be affected
	if !strings.HasPrefix(strings.SplitAfter(got, "## Alpha\n")[1], "\nalpha body") {
		t.Error("Alpha section content changed unexpectedly")
	}
	// returned content should contain both the heading and the new line
	content := m["content"].(string)
	if !strings.HasPrefix(content, "## Beta") {
		t.Errorf("returned content should start with heading: %q", content)
	}
	if !strings.Contains(content, "new line") {
		t.Errorf("returned content should contain appended text: %q", content)
	}
}

func TestAppendToSection_MissNoCreate(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionWriteDoc)

	res, isErr := runTool(s, "append_to_section", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "nonexistent",
		"content":   "x",
	})
	if !isErr {
		t.Fatalf("expected error for missing section, got %v", res)
	}
	m := res.(map[string]any)
	if !strings.Contains(m["error"].(string), "not found") {
		t.Errorf("error should mention not found: %v", m["error"])
	}
	avail, ok := m["available"].([]string)
	if !ok {
		t.Fatalf("expected available list, got %T: %v", m["available"], m["available"])
	}
	if len(avail) == 0 {
		t.Error("available slugs should be non-empty")
	}
	// file must be unchanged
	got, _, _ := s.Read("ns", "doc.md")
	if got != sectionWriteDoc {
		t.Errorf("file was modified on section miss: %q", got)
	}
}

func TestAppendToSection_MissWithCreate(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionWriteDoc)

	res, isErr := runTool(s, "append_to_section", map[string]any{
		"namespace":         "ns",
		"filename":          "doc.md",
		"section":           "Gamma",
		"content":           "gamma body",
		"create_if_missing": true,
	})
	if isErr {
		t.Fatalf("expected success with create_if_missing, got %v", res)
	}
	m := res.(map[string]any)
	if m["section"].(string) != "gamma" {
		t.Errorf("returned slug = %q, want %q", m["section"], "gamma")
	}
	got, _, _ := s.Read("ns", "doc.md")
	if !strings.Contains(got, "## Gamma") {
		t.Error("created section heading not found")
	}
	if !strings.Contains(got, "gamma body") {
		t.Error("created section content not found")
	}
	// original content must be intact
	if !strings.Contains(got, "## Alpha") || !strings.Contains(got, "## Beta") {
		t.Error("original sections were clobbered")
	}
	content := m["content"].(string)
	if !strings.HasPrefix(content, "## Gamma") {
		t.Errorf("returned content should start with heading: %q", content)
	}
}

func TestAppendToSection_AmbiguousHeading(t *testing.T) {
	s := setupStore(t)
	// Two sections share the same heading — goldmark will dedup slugs to
	// "foo" and "foo-1", but querying by heading text "Foo" is ambiguous.
	_ = s.Write("ns", "doc.md", "## Foo\n\nfirst\n\n## Foo\n\nsecond\n")

	res, isErr := runTool(s, "append_to_section", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "Foo",
		"content":   "extra",
	})
	if !isErr {
		t.Fatalf("expected ambiguity error, got %v", res)
	}
	m := res.(map[string]any)
	if !strings.Contains(m["error"].(string), "ambiguous") {
		t.Errorf("error should mention ambiguity: %v", m["error"])
	}
	conflicts, ok := m["conflicts"].([]map[string]string)
	if !ok {
		t.Fatalf("expected conflicts list, got %T: %v", m["conflicts"], m["conflicts"])
	}
	if len(conflicts) != 2 {
		t.Errorf("expected 2 conflicts, got %d: %v", len(conflicts), conflicts)
	}
	// file must be unchanged
	got, _, _ := s.Read("ns", "doc.md")
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Error("file was modified despite ambiguity error")
	}
}

// TestAppendToSection_NestedSubsectionBoundary verifies that appending to a
// parent section inserts after its nested sub-sections (not between them).
func TestAppendToSection_NestedSubsectionBoundary(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionWriteDoc)

	res, isErr := runTool(s, "append_to_section", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "alpha",
		"content":   "post-nested addition",
	})
	if isErr {
		t.Fatalf("expected success, got %v", res)
	}
	got, _, _ := s.Read("ns", "doc.md")

	// The Nested sub-section must still be inside Alpha (before Beta)
	alphaIdx := strings.Index(got, "## Alpha")
	nestedIdx := strings.Index(got, "### Nested")
	addIdx := strings.Index(got, "post-nested addition")
	betaIdx := strings.Index(got, "## Beta")

	if !(alphaIdx < nestedIdx && nestedIdx < addIdx && addIdx < betaIdx) {
		t.Errorf("order wrong: alpha=%d nested=%d addition=%d beta=%d in:\n%s",
			alphaIdx, nestedIdx, addIdx, betaIdx, got)
	}
	// returned content should contain the nested sub-section and the new line
	content := res.(map[string]any)["content"].(string)
	if !strings.Contains(content, "### Nested") {
		t.Errorf("returned section content should include sub-section: %q", content)
	}
	if !strings.Contains(content, "post-nested addition") {
		t.Errorf("returned section content should include appended text: %q", content)
	}
}

// ── upsert_section ───────────────────────────────────────────────────────────

func TestUpsertSection_ReplacesBody(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionWriteDoc)

	res, isErr := runTool(s, "upsert_section", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "beta",
		"content":   "\nreplaced body\n",
	})
	if isErr {
		t.Fatalf("expected success, got %v", res)
	}
	m := res.(map[string]any)
	if m["section"].(string) != "beta" {
		t.Errorf("returned slug = %q, want %q", m["section"], "beta")
	}
	got, _, _ := s.Read("ns", "doc.md")
	if !strings.Contains(got, "## Beta") {
		t.Error("Beta heading should be preserved")
	}
	if !strings.Contains(got, "replaced body") {
		t.Error("new body not found")
	}
	if strings.Contains(got, "beta body") {
		t.Error("old body should have been replaced")
	}
	// Alpha must be unaffected
	if !strings.Contains(got, "alpha body") {
		t.Error("Alpha section was clobbered")
	}
	content := m["content"].(string)
	if !strings.HasPrefix(content, "## Beta") {
		t.Errorf("returned content should start with heading: %q", content)
	}
	if !strings.Contains(content, "replaced body") {
		t.Errorf("returned content should contain new body: %q", content)
	}
}

func TestUpsertSection_CreatesSection(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionWriteDoc)

	res, isErr := runTool(s, "upsert_section", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "Delta",
		"content":   "\ndelta body\n",
	})
	if isErr {
		t.Fatalf("expected success, got %v", res)
	}
	m := res.(map[string]any)
	if m["section"].(string) != "delta" {
		t.Errorf("returned slug = %q, want %q", m["section"], "delta")
	}
	got, _, _ := s.Read("ns", "doc.md")
	if !strings.Contains(got, "## Delta") {
		t.Error("created section heading not found")
	}
	if !strings.Contains(got, "delta body") {
		t.Error("created section content not found")
	}
	// Original sections intact
	if !strings.Contains(got, "## Alpha") || !strings.Contains(got, "## Beta") {
		t.Error("original sections were clobbered")
	}
	content := m["content"].(string)
	if !strings.HasPrefix(content, "## Delta") {
		t.Errorf("returned content should start with heading: %q", content)
	}
}

func TestUpsertSection_AmbiguousHeading(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", "## Foo\n\nfirst\n\n## Foo\n\nsecond\n")

	res, isErr := runTool(s, "upsert_section", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "Foo",
		"content":   "replacement",
	})
	if !isErr {
		t.Fatalf("expected ambiguity error, got %v", res)
	}
	m := res.(map[string]any)
	if !strings.Contains(m["error"].(string), "ambiguous") {
		t.Errorf("error should mention ambiguity: %v", m["error"])
	}
	// file must be unchanged
	got, _, _ := s.Read("ns", "doc.md")
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Error("file was modified despite ambiguity error")
	}
}

// TestUpsertSection_NestedSubsectionBoundary verifies that upserting a parent
// section replaces only its direct body+sub-sections and does not bleed into
// sibling sections.
func TestUpsertSection_NestedSubsectionBoundary(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionWriteDoc)

	res, isErr := runTool(s, "upsert_section", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "alpha",
		"content":   "\nreplaced content\n\n### Still Nested\n\nstill here\n",
	})
	if isErr {
		t.Fatalf("expected success, got %v", res)
	}
	got, _, _ := s.Read("ns", "doc.md")
	if !strings.Contains(got, "## Alpha") {
		t.Error("Alpha heading should be preserved")
	}
	if strings.Contains(got, "alpha body") {
		t.Error("old body should be replaced")
	}
	if !strings.Contains(got, "replaced content") {
		t.Error("new body not found")
	}
	if !strings.Contains(got, "### Still Nested") {
		t.Error("new sub-section not found")
	}
	// Beta must follow Alpha cleanly
	if !strings.Contains(got, "## Beta") {
		t.Error("Beta section was clobbered")
	}
	betaIdx := strings.Index(got, "## Beta")
	alphaIdx := strings.Index(got, "## Alpha")
	if alphaIdx >= betaIdx {
		t.Errorf("Alpha should come before Beta: alpha=%d beta=%d", alphaIdx, betaIdx)
	}
	content := res.(map[string]any)["content"].(string)
	if !strings.HasPrefix(content, "## Alpha") {
		t.Errorf("returned content should start with heading: %q", content)
	}
	if strings.Contains(content, "## Beta") {
		t.Errorf("returned content leaked into Beta section: %q", content)
	}
}

// TestAppendToSection_ExactSlugDisambiguates verifies that an exact slug query
// succeeds even when the heading appears more than once (goldmark deduplicates).
func TestAppendToSection_ExactSlugDisambiguates(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", "## Foo\n\nfirst\n\n## Foo\n\nsecond\n")

	// "foo" is the exact slug of the FIRST Foo section; "foo-1" is the second.
	res, isErr := runTool(s, "append_to_section", map[string]any{
		"namespace": "ns",
		"filename":  "doc.md",
		"section":   "foo",
		"content":   "added to first",
	})
	if isErr {
		t.Fatalf("exact slug should disambiguate, got %v", res)
	}
	got, _, _ := s.Read("ns", "doc.md")
	if !strings.Contains(got, "added to first") {
		t.Error("content not appended to first section")
	}
	// second section must be untouched
	if !strings.Contains(got, "second") {
		t.Error("second section was clobbered")
	}
}
