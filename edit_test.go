package main

import (
	"strings"
	"testing"
)

func TestEditTool_HappyPath(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "alpha beta gamma")
	res, isErr := runTool(s, "edit", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
		"old_str":   "beta",
		"new_str":   "DELTA",
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
		"namespace": "ns",
		"filename":  "f.md",
		"old_str":   "foo",
		"new_str":   "bar",
	})
	if !isErr {
		t.Fatalf("expected uniqueness error, got %v", res)
	}
	m := res.(errResult)
	if !strings.Contains(m.Error, "must be unique") {
		t.Errorf("error message: %v", m.Error)
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
		"namespace": "ns",
		"filename":  "f.md",
		"old_str":   "line1\r\nline2",
		"new_str":   "X\r\nY",
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
		"namespace": "ns",
		"filename":  "f.md",
		"old_str":   "beta",
		"new_str":   "DELTA",
	})
	if !isErr {
		t.Fatalf("expected not-found error, got %v", res)
	}
}

func TestEditTool_EmptyOldString(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "anything")
	_, isErr := runTool(s, "edit", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
		"old_str":   "",
		"new_str":   "x",
	})
	if !isErr {
		t.Fatal("expected error for empty old_str")
	}
}

func TestEditTool_FileNotFound(t *testing.T) {
	s := setupStore(t)
	_, isErr := runTool(s, "edit", map[string]any{
		"namespace": "ns",
		"filename":  "missing.md",
		"old_str":   "x",
		"new_str":   "y",
	})
	if !isErr {
		t.Fatal("expected error for missing file")
	}
}

func TestWriteTool_MissingContent_Errors(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "important data")
	_, isErr := runTool(s, "force_write", map[string]any{
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
	_, isErr := runTool(s, "force_write", map[string]any{
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
	m := res.(errResult)
	if !strings.Contains(m.Error, "namespace") {
		t.Errorf("error should mention namespace, got: %q", m.Error)
	}
}

func TestWriteTool_MissingNamespace_Errors(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "important data")
	res, isErr := runTool(s, "force_write", map[string]any{
		"filename": "f.md",
		"content":  "replacement",
	})
	if !isErr {
		t.Fatal("expected error when namespace is absent")
	}
	m := res.(errResult)
	if !strings.Contains(m.Error, "namespace") {
		t.Errorf("error should mention namespace, got: %q", m.Error)
	}
	got, _, _ := s.Read("ns", "f.md")
	if got != "important data" {
		t.Errorf("file was modified despite missing namespace: %q", got)
	}
}

func TestCreateTool_NewFile_Succeeds(t *testing.T) {
	s := setupStore(t)
	_, isErr := runTool(s, "create", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
		"content":   "hello",
	})
	if isErr {
		t.Fatal("create failed on a new file")
	}
	if got, _, _ := s.Read("ns", "f.md"); got != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
}

func TestCreateTool_ExistingFile_Errors(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "important data")
	res, isErr := runTool(s, "create", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
		"content":   "clobber",
	})
	if !isErr {
		t.Fatal("expected error when creating a file that already exists")
	}
	m := res.(errResult)
	if !strings.Contains(m.Error, "exists") {
		t.Errorf("error should mention the file exists, got: %q", m.Error)
	}
	if got, _, _ := s.Read("ns", "f.md"); got != "important data" {
		t.Errorf("file was modified despite create-on-existing: %q", got)
	}
}

func TestForceWriteTool_ExistingFile_Overwrites(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "old")
	_, isErr := runTool(s, "force_write", map[string]any{
		"namespace": "ns",
		"filename":  "f.md",
		"content":   "new",
	})
	if isErr {
		t.Fatal("force_write failed on an existing file")
	}
	if got, _, _ := s.Read("ns", "f.md"); got != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}

func TestForceWriteTool_MissingFile_Errors(t *testing.T) {
	s := setupStore(t)
	res, isErr := runTool(s, "force_write", map[string]any{
		"namespace": "ns",
		"filename":  "nope.md",
		"content":   "data",
	})
	if !isErr {
		t.Fatal("expected error when force_writing a file that does not exist")
	}
	m := res.(errResult)
	if !strings.Contains(m.Error, "not found") {
		t.Errorf("error should mention not found, got: %q", m.Error)
	}
}
