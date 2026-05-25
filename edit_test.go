package main

import (
	"strings"
	"testing"
)

func TestEditTool_HappyPath(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "alpha beta gamma")
	res, isErr := runTool(s, "edit", map[string]any{
		"namespace":  "ns",
		"filename":   "f.md",
		"old_string": "beta",
		"new_string": "DELTA",
	})
	if isErr {
		t.Fatalf("expected success, got %v", res)
	}
	got, _, _ := s.Read("ns", "f.md")
	if got != "alpha DELTA gamma" {
		t.Errorf("content after edit: %q", got)
	}
}

func TestEditTool_NotUnique(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "foo foo foo")
	res, isErr := runTool(s, "edit", map[string]any{
		"namespace":  "ns",
		"filename":   "f.md",
		"old_string": "foo",
		"new_string": "bar",
	})
	if !isErr {
		t.Fatalf("expected uniqueness error, got %v", res)
	}
	m := res.(map[string]any)
	if !strings.Contains(m["error"].(string), "must be unique") {
		t.Errorf("error message: %v", m["error"])
	}
	if got, _, _ := s.Read("ns", "f.md"); got != "foo foo foo" {
		t.Errorf("file was modified despite non-unique match: %q", got)
	}
}

func TestEditTool_NotFound(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "alpha gamma")
	res, isErr := runTool(s, "edit", map[string]any{
		"namespace":  "ns",
		"filename":   "f.md",
		"old_string": "beta",
		"new_string": "DELTA",
	})
	if !isErr {
		t.Fatalf("expected not-found error, got %v", res)
	}
}

func TestEditTool_EmptyOldString(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "anything")
	_, isErr := runTool(s, "edit", map[string]any{
		"namespace":  "ns",
		"filename":   "f.md",
		"old_string": "",
		"new_string": "x",
	})
	if !isErr {
		t.Fatal("expected error for empty old_string")
	}
}

func TestEditTool_FileNotFound(t *testing.T) {
	s := setupStore(t)
	_, isErr := runTool(s, "edit", map[string]any{
		"namespace":  "ns",
		"filename":   "missing.md",
		"old_string": "x",
		"new_string": "y",
	})
	if !isErr {
		t.Fatal("expected error for missing file")
	}
}
