package main

import (
	"os"
	"path/filepath"
	"testing"
)

func setupStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewSQLiteStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return s
}

func TestMoveTool_RenameInSameNamespace(t *testing.T) {
	s := setupStore(t)
	if err := s.Write("ns", "old/path.md", "hi"); err != nil {
		t.Fatal(err)
	}
	res, isErr := runTool(s, "move", map[string]any{
		"namespace":    "ns",
		"filename":     "old/path.md",
		"new_filename": "new/path.md",
	})
	if isErr {
		t.Fatalf("move returned error: %v", res)
	}
	content, _, err := s.Read("ns", "new/path.md")
	if err != nil {
		t.Fatalf("read new path failed: %v", err)
	}
	if content != "hi" {
		t.Fatalf("content lost: %q", content)
	}
	if _, _, err := s.Read("ns", "old/path.md"); err != ErrNotFound {
		t.Fatalf("expected old path gone, got err=%v", err)
	}
}

func TestMoveTool_CrossNamespace(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("a", "f.md", "x")
	res, isErr := runTool(s, "move", map[string]any{
		"namespace":     "a",
		"filename":      "f.md",
		"new_namespace": "b",
		"new_filename":  "folder/f.md",
	})
	if isErr {
		t.Fatalf("move returned error: %v", res)
	}
	if _, _, err := s.Read("b", "folder/f.md"); err != nil {
		t.Fatalf("cross-namespace move failed: %v", err)
	}
}

func TestMoveTool_DestinationExists(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "1")
	_ = s.Write("ns", "b.md", "2")
	res, isErr := runTool(s, "move", map[string]any{
		"namespace":    "ns",
		"filename":     "a.md",
		"new_filename": "b.md",
	})
	if !isErr {
		t.Fatalf("expected error, got %v", res)
	}
	c, _, _ := s.Read("ns", "b.md")
	if c != "2" {
		t.Fatalf("destination clobbered: %q", c)
	}
	c, _, _ = s.Read("ns", "a.md")
	if c != "1" {
		t.Fatalf("source corrupted: %q", c)
	}
}

func TestMoveTool_NotFound(t *testing.T) {
	s := setupStore(t)
	_, isErr := runTool(s, "move", map[string]any{
		"namespace":    "ns",
		"filename":     "nope.md",
		"new_filename": "anywhere.md",
	})
	if !isErr {
		t.Fatal("expected error for missing source")
	}
}

func TestDeleteTool(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "folder/x.md", "y")
	_, isErr := runTool(s, "delete", map[string]any{
		"namespace": "ns",
		"filename":  "folder/x.md",
	})
	if isErr {
		t.Fatal("delete returned error")
	}
	if _, _, err := s.Read("ns", "folder/x.md"); err != ErrNotFound {
		t.Fatalf("expected gone, got %v", err)
	}
}
