package main

import (
	"strings"
	"testing"
)

func TestTreeTool_IncludesSections(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionDoc)
	_ = s.Write("ns", "plain.txt", "not parsed")

	res, isErr := runTool(s, "tree", map[string]any{"namespace": "ns"})
	if isErr {
		t.Fatalf("tree error: %v", res)
	}
	out := res.(map[string]string)["tree"]

	// Default depth=1 → files + ## only; H1 (# top) is suppressed by convention.
	for _, want := range []string{"doc.md", "plain.txt", "## phase-10-design", "## other-thing"} {
		if !strings.Contains(out, want) {
			t.Errorf("tree output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "# top") {
		t.Errorf("default depth=1 should suppress H1; got:\n%s", out)
	}
}

func TestTreeTool_TokenAnnotationOnLargeSection(t *testing.T) {
	s := setupStore(t)
	// 400 tokens ≈ 1600 chars. Build a section over the threshold.
	big := strings.Repeat("word word word word ", 100) // 2000 chars body
	doc := "# Small\n\nshort\n\n## Big\n\n" + big + "\n"
	_ = s.Write("ns", "doc.md", doc)

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns"})
	out := res.(map[string]string)["tree"]

	if !strings.Contains(out, "## big (") {
		t.Errorf("expected big section to be annotated with tokens; got:\n%s", out)
	}
	if strings.Contains(out, "# small (") {
		t.Errorf("small section should NOT be annotated; got:\n%s", out)
	}
}

func TestTreeTool_BackwardCompatibleForNonMarkdown(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "folder/a.txt", "x")
	_ = s.Write("ns", "folder/b.txt", "y")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns"})
	out := res.(map[string]string)["tree"]

	want := "folder/\n├── a.txt\n└── b.txt"
	if !strings.Contains(out, want) {
		t.Errorf("expected non-md tree unchanged; got:\n%s", out)
	}
}

// ── depth control ────────────────────────────────────────────────────────────

const nestedDoc = `# Title

intro

## Top section

body

### Sub one

deep

### Sub two

more

## Other top

body
`

func TestTreeTool_Depth0_NoHeadings(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", nestedDoc)

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "depth": 0})
	out := res.(map[string]string)["tree"]

	if strings.Contains(out, "#") {
		t.Errorf("depth=0 should emit no heading lines; got:\n%s", out)
	}
	if !strings.Contains(out, "doc.md") {
		t.Errorf("depth=0 still shows files; got:\n%s", out)
	}
}

func TestTreeTool_Depth1_OnlyH2(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", nestedDoc)

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "depth": 1})
	out := res.(map[string]string)["tree"]

	for _, want := range []string{"## top-section", "## other-top"} {
		if !strings.Contains(out, want) {
			t.Errorf("depth=1 missing %q; got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"# title", "### sub-one", "### sub-two"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("depth=1 should not emit %q; got:\n%s", unwanted, out)
		}
	}
}

func TestTreeTool_Depth2_H2AndH3(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", nestedDoc)

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "depth": 2})
	out := res.(map[string]string)["tree"]

	for _, want := range []string{"## top-section", "### sub-one", "### sub-two", "## other-top"} {
		if !strings.Contains(out, want) {
			t.Errorf("depth=2 missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "# title") {
		t.Errorf("depth=2 should still suppress H1; got:\n%s", out)
	}
}

func TestTreeTool_Depth99_AllLevels(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", nestedDoc)

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "depth": 99})
	out := res.(map[string]string)["tree"]

	for _, want := range []string{"# title", "## top-section", "### sub-one", "### sub-two", "## other-top"} {
		if !strings.Contains(out, want) {
			t.Errorf("depth=99 missing %q; got:\n%s", want, out)
		}
	}
}

func TestTreeTool_DepthPreservesTokenAccuracy(t *testing.T) {
	// When a dropped H1 sits before a kept H2, the H2's token count should
	// reflect only its own bytes — not bleed in the H1's body. This guards
	// against the depth filter distorting annotations.
	s := setupStore(t)
	big := strings.Repeat("word word word word ", 100) // ~2000 chars
	doc := "# Title\n\n" + big + "\n\n## Small\n\nshort\n"
	_ = s.Write("ns", "doc.md", doc)

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "depth": 1})
	out := res.(map[string]string)["tree"]

	if strings.Contains(out, "## small (") {
		t.Errorf("dropped H1's bytes leaked into ## small token count; got:\n%s", out)
	}
}

// ── path scoping: literal ────────────────────────────────────────────────────

func TestTreeTool_LiteralPath_SingleFile(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", sectionDoc)
	_ = s.Write("ns", "other.md", "# Other\n\n## Heading\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "path": "doc.md"})
	out := res.(map[string]string)["tree"]

	if !strings.Contains(out, "doc.md") {
		t.Errorf("literal file path should include target; got:\n%s", out)
	}
	if strings.Contains(out, "other.md") {
		t.Errorf("literal file path should exclude other files; got:\n%s", out)
	}
}

func TestTreeTool_LiteralPath_Folder(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "decisions/a.md", "# A\n")
	_ = s.Write("ns", "decisions/b.md", "# B\n")
	_ = s.Write("ns", "outside.md", "# Outside\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "path": "decisions/"})
	out := res.(map[string]string)["tree"]

	for _, want := range []string{"a.md", "b.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("folder scope missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "outside.md") {
		t.Errorf("folder scope leaked outside files; got:\n%s", out)
	}
}

func TestTreeTool_LiteralPath_FolderWithoutTrailingSlash(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "decisions/a.md", "# A\n")
	_ = s.Write("ns", "outside.md", "x")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "path": "decisions"})
	out := res.(map[string]string)["tree"]

	if !strings.Contains(out, "a.md") {
		t.Errorf("folder scope without trailing slash should still match; got:\n%s", out)
	}
	if strings.Contains(out, "outside.md") {
		t.Errorf("folder scope leaked; got:\n%s", out)
	}
}

// ── path scoping: glob ───────────────────────────────────────────────────────

func TestTreeTool_GlobPath_MatchesAcrossFolders(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "tasks/foo/index.md", "# Foo\n\n## Inside\n")
	_ = s.Write("ns", "tasks/bar/index.md", "# Bar\n\n## Inside\n")
	_ = s.Write("ns", "tasks/foo/other.md", "# Other\n")
	_ = s.Write("ns", "state.md", "# State\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "path": "tasks/**/index.md"})
	out := res.(map[string]string)["tree"]

	for _, want := range []string{"foo/", "bar/", "index.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("glob result missing %q; got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"other.md", "state.md"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("glob result leaked %q; got:\n%s", unwanted, out)
		}
	}
}

func TestTreeTool_GlobPath_MatchesZero(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", "# Doc\n")

	res, isErr := runTool(s, "tree", map[string]any{"namespace": "ns", "path": "nothing/*.md"})
	if isErr {
		t.Fatalf("zero matches should not be an error; got %v", res)
	}
	out := res.(map[string]string)["tree"]
	if strings.TrimSpace(out) != "" {
		t.Errorf("zero-match glob should render empty tree; got:\n%q", out)
	}
}

func TestTreeTool_GlobPath_MatchesOne(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "decisions/a.md", "# A\n\n## Inside\n")
	_ = s.Write("ns", "decisions/b.txt", "not md")
	_ = s.Write("ns", "other.md", "# Other\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "path": "decisions/*.md"})
	out := res.(map[string]string)["tree"]

	if !strings.Contains(out, "a.md") {
		t.Errorf("single-glob-match missing target; got:\n%s", out)
	}
	if strings.Contains(out, "b.txt") || strings.Contains(out, "other.md") {
		t.Errorf("single-glob-match leaked; got:\n%s", out)
	}
}

func TestTreeTool_GlobPath_RespectsDepth(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", nestedDoc)

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "path": "*.md", "depth": 99})
	out := res.(map[string]string)["tree"]

	if !strings.Contains(out, "# title") {
		t.Errorf("glob + depth=99 should include H1; got:\n%s", out)
	}
}
