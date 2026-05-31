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
		"old_str": "beta",
		"new_str": "DELTA",
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
		"old_str": "foo",
		"new_str": "bar",
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

func TestWrite_NormalizesCRLF(t *testing.T) {
	s := setupStore(t)
	if err := s.Write("ns", "f.md", "a\r\nb\r\nc"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Read("ns", "f.md")
	if got != "a\nb\nc" {
		t.Errorf("stored content not LF: %q", got)
	}
}

func TestAppend_NormalizesCRLF(t *testing.T) {
	s := setupStore(t)
	if err := s.Write("ns", "f.md", "a\nb"); err != nil {
		t.Fatal(err)
	}
	if err := s.Append("ns", "f.md", "c\r\nd"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Read("ns", "f.md")
	if got != "a\nb\nc\nd" {
		t.Errorf("appended content not LF: %q", got)
	}
}

func TestEditTool_CRLFOldStringMatchesLFContent(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "line1\nline2\nline3")
	res, isErr := runTool(s, "edit", map[string]any{
		"namespace":  "ns",
		"filename":   "f.md",
		"old_str": "line1\r\nline2",
		"new_str": "X\r\nY",
	})
	if isErr {
		t.Fatalf("expected success, got %v", res)
	}
	got, _, _ := s.Read("ns", "f.md")
	if got != "X\nY\nline3" {
		t.Errorf("content after CRLF edit: %q", got)
	}
}

func TestEditTool_NotFound(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "alpha gamma")
	res, isErr := runTool(s, "edit", map[string]any{
		"namespace":  "ns",
		"filename":   "f.md",
		"old_str": "beta",
		"new_str": "DELTA",
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
		"old_str": "",
		"new_str": "x",
	})
	if !isErr {
		t.Fatal("expected error for empty old_str")
	}
}

func TestEditTool_FileNotFound(t *testing.T) {
	s := setupStore(t)
	_, isErr := runTool(s, "edit", map[string]any{
		"namespace":  "ns",
		"filename":   "missing.md",
		"old_str": "x",
		"new_str": "y",
	})
	if !isErr {
		t.Fatal("expected error for missing file")
	}
}

func TestWriteTool_MissingContent_Errors(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "important data")
	_, isErr := runTool(s, "write", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
	})
	if !isErr {
		t.Fatal("expected error when content key is absent")
	}
	got, _, _ := s.Read("ns", "f.md")
	if got != "important data" {
		t.Errorf("file was modified despite missing content: %q", got)
	}
}

func TestWriteTool_EmptyContent_Errors(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "important data")
	_, isErr := runTool(s, "write", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
		"content":   "",
	})
	if !isErr {
		t.Fatal("expected error when content is empty string")
	}
	got, _, _ := s.Read("ns", "f.md")
	if got != "important data" {
		t.Errorf("file was modified despite empty content: %q", got)
	}
}

func TestAppendTool_MissingContent_Errors(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "existing")
	_, isErr := runTool(s, "append", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
	})
	if !isErr {
		t.Fatal("expected error when content key is absent")
	}
	got, _, _ := s.Read("ns", "f.md")
	if got != "existing" {
		t.Errorf("file was modified despite missing content: %q", got)
	}
}

func TestReadTool_MissingNamespace_Errors(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "data")
	res, isErr := runTool(s, "read", map[string]any{"filename": "f.md"})
	if !isErr {
		t.Fatal("expected error when namespace is absent")
	}
	m := res.(map[string]string)
	if !strings.Contains(m["error"], "namespace") {
		t.Errorf("error should mention namespace, got: %q", m["error"])
	}
}

func TestWriteTool_MissingNamespace_Errors(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "important data")
	res, isErr := runTool(s, "write", map[string]any{
		"filename": "f.md",
		"content":  "replacement",
	})
	if !isErr {
		t.Fatal("expected error when namespace is absent")
	}
	m := res.(map[string]string)
	if !strings.Contains(m["error"], "namespace") {
		t.Errorf("error should mention namespace, got: %q", m["error"])
	}
	got, _, _ := s.Read("ns", "f.md")
	if got != "important data" {
		t.Errorf("file was modified despite missing namespace: %q", got)
	}
}
