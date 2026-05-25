package main

import (
	"testing"
)

func TestListTool_NoPattern(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "x")
	_ = s.Write("ns", "folder/b.md", "y")

	res, isErr := runTool(s, "list", map[string]any{"namespace": "ns"})
	if isErr {
		t.Fatalf("unexpected error: %v", res)
	}
	files := res.(map[string]any)["files"].([]FileEntry)
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(files), files)
	}
}

func TestListTool_PatternMatches(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "decisions/a.md", "x")
	_ = s.Write("ns", "decisions/b.md", "y")
	_ = s.Write("ns", "journal/c.md", "z")
	_ = s.Write("ns", "state.md", "w")

	res, _ := runTool(s, "list", map[string]any{"namespace": "ns", "pattern": "decisions/*.md"})
	files := res.(map[string]any)["files"].([]FileEntry)
	if len(files) != 2 {
		t.Errorf("expected 2 decisions files, got %d: %v", len(files), files)
	}
	for _, f := range files {
		if f.Filename != "decisions/a.md" && f.Filename != "decisions/b.md" {
			t.Errorf("unexpected file: %v", f.Filename)
		}
	}
}

func TestListTool_PatternDoublestar(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "tasks/foo/index.md", "a")
	_ = s.Write("ns", "tasks/bar/index.md", "b")
	_ = s.Write("ns", "tasks/foo/other.md", "c")
	_ = s.Write("ns", "state.md", "d")

	res, _ := runTool(s, "list", map[string]any{"namespace": "ns", "pattern": "tasks/**/index.md"})
	files := res.(map[string]any)["files"].([]FileEntry)
	if len(files) != 2 {
		t.Errorf("expected 2 index.md matches, got %d: %v", len(files), files)
	}
}

func TestListTool_PatternZeroMatches(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", "x")

	res, isErr := runTool(s, "list", map[string]any{"namespace": "ns", "pattern": "nothing/*.md"})
	if isErr {
		t.Fatalf("zero matches should not error; got %v", res)
	}
	files := res.(map[string]any)["files"].([]FileEntry)
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestListTool_PatternInvalid(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "doc.md", "x")

	_, isErr := runTool(s, "list", map[string]any{"namespace": "ns", "pattern": "[invalid"})
	if !isErr {
		t.Error("expected error for malformed glob")
	}
}
