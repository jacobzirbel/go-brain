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

	// Default: depth=1 + heading-text labels (Phase 12a). H1 (# Top) is
	// suppressed at depth=1 — filename is the conventional title.
	for _, want := range []string{"doc.md", "plain.txt", "## Phase 10 design", "## Other thing"} {
		if !strings.Contains(out, want) {
			t.Errorf("tree output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "# Top") {
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

	if !strings.Contains(out, "## Big (") {
		t.Errorf("expected big section to be annotated with tokens; got:\n%s", out)
	}
	if strings.Contains(out, "# Small (") {
		t.Errorf("small section should NOT be annotated; got:\n%s", out)
	}
}

func TestTreeTool_NonMarkdownFolderExpansion(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "folder/a.txt", "x")
	_ = s.Write("ns", "folder/b.txt", "y")

	// Phase 11.1: at root the folder collapses to (N items); explicit path
	// expansion shows contents.
	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "path": "folder/"})
	out := res.(map[string]string)["tree"]

	for _, want := range []string{"folder/", "├── a.txt", "└── b.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected expanded folder to contain %q; got:\n%s", want, out)
		}
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

	for _, want := range []string{"## Top section", "## Other top"} {
		if !strings.Contains(out, want) {
			t.Errorf("depth=1 missing %q; got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"# Title", "### Sub one", "### Sub two"} {
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

	for _, want := range []string{"## Top section", "### Sub one", "### Sub two", "## Other top"} {
		if !strings.Contains(out, want) {
			t.Errorf("depth=2 missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "# Title") {
		t.Errorf("depth=2 should still suppress H1; got:\n%s", out)
	}
}

func TestTreeTool_Depth99_AllLevels(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", nestedDoc)

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "depth": 99})
	out := res.(map[string]string)["tree"]

	for _, want := range []string{"# Title", "## Top section", "### Sub one", "### Sub two", "## Other top"} {
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

	if strings.Contains(out, "## Small (") {
		t.Errorf("dropped H1's bytes leaked into ## Small token count; got:\n%s", out)
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

	if !strings.Contains(out, "# Title") {
		t.Errorf("glob + depth=99 should include H1; got:\n%s", out)
	}
}

// ── Phase 11.1: folder collapse + archive exclusion ──────────────────────────

func TestTreeTool_FoldersCollapseByDefault(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "state.md", "# State\n\n## Now\n")
	_ = s.Write("ns", "tasks/a.md", "# A\n")
	_ = s.Write("ns", "tasks/b.md", "# B\n")
	_ = s.Write("ns", "tasks/nested/c.md", "# C\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns"})
	out := res.(map[string]string)["tree"]

	// Root file expands with sections; folder summarizes.
	if !strings.Contains(out, "state.md") {
		t.Errorf("root file missing; got:\n%s", out)
	}
	if !strings.Contains(out, "## Now") {
		t.Errorf("root file section missing; got:\n%s", out)
	}
	if !strings.Contains(out, "tasks/ (3 items)") {
		t.Errorf("expected 'tasks/ (3 items)' folder summary; got:\n%s", out)
	}
	for _, leaked := range []string{"a.md", "b.md", "c.md", "nested/"} {
		if strings.Contains(out, leaked) {
			t.Errorf("folder content leaked through summary: %q; got:\n%s", leaked, out)
		}
	}
}

func TestTreeTool_FolderExpansionViaPath(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "tasks/a.md", "# A\n\n## Inside\n")
	_ = s.Write("ns", "tasks/b.md", "# B\n")
	_ = s.Write("ns", "tasks/nested/c.md", "# C\n")
	_ = s.Write("ns", "outside.md", "x")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "path": "tasks/"})
	out := res.(map[string]string)["tree"]

	if !strings.HasPrefix(out, "tasks/") {
		t.Errorf("expected 'tasks/' header; got:\n%s", out)
	}
	for _, want := range []string{"a.md", "b.md", "## Inside", "nested/ (1 item)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "outside.md") {
		t.Errorf("outside file leaked; got:\n%s", out)
	}
	if strings.Contains(out, "c.md") {
		t.Errorf("nested sub-folder contents leaked through summary; got:\n%s", out)
	}
}

func TestTreeTool_ArchiveHiddenByDefault(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "active.md", "# Active\n")
	_ = s.Write("ns", "archive/old.md", "# Old\n")
	_ = s.Write("ns", "archived/oldish.md", "# Oldish\n")
	_ = s.Write("ns", "deleted/gone.md", "# Gone\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns"})
	out := res.(map[string]string)["tree"]

	if !strings.Contains(out, "active.md") {
		t.Errorf("active file should be visible; got:\n%s", out)
	}
	for _, hidden := range []string{"archive/", "archived/", "deleted/", "old.md", "oldish.md", "gone.md"} {
		if strings.Contains(out, hidden) {
			t.Errorf("archive prefix leaked through default: %q; got:\n%s", hidden, out)
		}
	}
}

func TestTreeTool_ArchiveVisibleViaExplicitPath(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "active.md", "# Active\n")
	_ = s.Write("ns", "archive/old.md", "# Old\n\n## History\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "path": "archive/"})
	out := res.(map[string]string)["tree"]

	for _, want := range []string{"archive/", "old.md", "## History"} {
		if !strings.Contains(out, want) {
			t.Errorf("explicit archive path missing %q; got:\n%s", want, out)
		}
	}
}

func TestTreeTool_ArchiveVisibleViaIncludeFlag(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "active.md", "# Active\n")
	_ = s.Write("ns", "archive/old.md", "# Old\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "include_archive": true})
	out := res.(map[string]string)["tree"]

	if !strings.Contains(out, "archive/") {
		t.Errorf("include_archive=true should surface archive folder; got:\n%s", out)
	}
	if !strings.Contains(out, "active.md") {
		t.Errorf("include_archive should not hide active files; got:\n%s", out)
	}
}

func TestTreeTool_ArchiveHiddenAtDepth99WithoutFlag(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "active.md", "# Active\n")
	_ = s.Write("ns", "archive/old.md", "# Old\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "depth": 99})
	out := res.(map[string]string)["tree"]

	if strings.Contains(out, "archive/") || strings.Contains(out, "old.md") {
		t.Errorf("depth=99 alone should NOT bypass archive exclusion; got:\n%s", out)
	}
}

func TestTreeTool_ArchiveAtDepth99WithFlag(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "archive/old.md", "# Old\n\n## History\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "depth": 99, "include_archive": true})
	out := res.(map[string]string)["tree"]

	for _, want := range []string{"archive/", "old.md", "## History"} {
		if !strings.Contains(out, want) {
			t.Errorf("depth=99 + include_archive missing %q; got:\n%s", want, out)
		}
	}
}

func TestTreeTool_FolderCountUsesSingular(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "folder/only.md", "x")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns"})
	out := res.(map[string]string)["tree"]

	if !strings.Contains(out, "folder/ (1 item)") {
		t.Errorf("expected singular 'item'; got:\n%s", out)
	}
}

// ── Phase 12a: heading text in tree ──────────────────────────────────────────

func TestTreeTool_EmitsHeadingText(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", "# Title\n\n## Phase 7 ⬜ — `log` tool + backend timestamping\n\nbody\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns", "depth": 99})
	out := res.(map[string]string)["tree"]

	want := "## Phase 7 ⬜ — `log` tool + backend timestamping"
	if !strings.Contains(out, want) {
		t.Errorf("expected heading text emitted verbatim; want %q; got:\n%s", want, out)
	}
	// And it should NOT show the slug-style label
	if strings.Contains(out, "## phase-7---log-tool--backend-timestamping") {
		t.Errorf("slug-style label should be suppressed in favor of heading text; got:\n%s", out)
	}
}

func TestTreeTool_HeadingsWithSymbolsRender(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", "# Doc\n\n## API: GET /foo\n\nbody\n\n## Edge cases (rare)\n\nbody\n")

	res, _ := runTool(s, "tree", map[string]any{"namespace": "ns"})
	out := res.(map[string]string)["tree"]

	for _, want := range []string{"## API: GET /foo", "## Edge cases (rare)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q; got:\n%s", want, out)
		}
	}
}
