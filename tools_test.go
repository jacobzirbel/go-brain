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

func TestMoveManyTool_ArchivesAFolder(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("brain", "journal/may-22.md", "one")
	_ = s.Write("brain", "journal/may-23.md", "two")
	_ = s.Write("brain", "state.md", "untouched")
	res, isErr := runTool(s, "move_many", map[string]any{
		"moves": []any{
			map[string]any{
				"namespace":    "brain",
				"filename":     "journal/may-22.md",
				"new_filename": "archive/journal/may-22.md",
			},
			map[string]any{
				"namespace":    "brain",
				"filename":     "journal/may-23.md",
				"new_filename": "archive/journal/may-23.md",
			},
		},
	})
	if isErr {
		t.Fatalf("move_many returned error: %v", res)
	}
	for _, name := range []string{"archive/journal/may-22.md", "archive/journal/may-23.md"} {
		if _, _, err := s.Read("brain", name); err != nil {
			t.Fatalf("missing destination %s: %v", name, err)
		}
	}
	if _, _, err := s.Read("brain", "journal/may-22.md"); err != ErrNotFound {
		t.Fatalf("expected source gone, got %v", err)
	}
	if c, _, _ := s.Read("brain", "state.md"); c != "untouched" {
		t.Fatalf("unrelated file changed: %q", c)
	}
}

func TestMoveManyTool_RollsBackOnFailure(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "A")
	_ = s.Write("ns", "b.md", "B")
	_ = s.Write("ns", "occupied.md", "X")
	_, isErr := runTool(s, "move_many", map[string]any{
		"moves": []any{
			map[string]any{"namespace": "ns", "filename": "a.md", "new_filename": "archive/a.md"},
			map[string]any{"namespace": "ns", "filename": "b.md", "new_filename": "occupied.md"},
		},
	})
	if !isErr {
		t.Fatal("expected error from move_many")
	}
	if c, _, _ := s.Read("ns", "a.md"); c != "A" {
		t.Fatalf("first move not rolled back, a.md content: %q", c)
	}
	if _, _, err := s.Read("ns", "archive/a.md"); err != ErrNotFound {
		t.Fatal("expected archive/a.md to not exist after rollback")
	}
	if c, _, _ := s.Read("ns", "occupied.md"); c != "X" {
		t.Fatalf("destination clobbered: %q", c)
	}
}

func TestMoveManyTool_EmptyArray(t *testing.T) {
	s := setupStore(t)
	res, isErr := runTool(s, "move_many", map[string]any{"moves": []any{}})
	if isErr {
		t.Fatalf("empty moves should be a no-op, got: %v", res)
	}
}
