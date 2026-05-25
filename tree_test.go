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

	for _, want := range []string{"doc.md", "plain.txt", "# top", "## phase-10-design", "## other-thing"} {
		if !strings.Contains(out, want) {
			t.Errorf("tree output missing %q\n---\n%s", want, out)
		}
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
