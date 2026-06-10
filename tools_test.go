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

func TestMoveTool_StagesPendingMove(t *testing.T) {
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
	// Staged, not applied: file is still at the source path.
	if _, _, err := s.Read("ns", "old/path.md"); err != nil {
		t.Fatalf("source should still exist before approval: %v", err)
	}
	if _, _, err := s.Read("ns", "new/path.md"); err != ErrNotFound {
		t.Fatalf("destination should not exist before approval, got err=%v", err)
	}
	dstNS, dstName, ok, err := s.PendingMove("ns", "old/path.md")
	if err != nil || !ok {
		t.Fatalf("expected staged move, got ok=%v err=%v", ok, err)
	}
	if dstNS != "ns" || dstName != "new/path.md" {
		t.Fatalf("staged destination wrong: %s/%s", dstNS, dstName)
	}
	// Approval applies it.
	if err := s.Review("ns", "old/path.md"); err != nil {
		t.Fatalf("review failed: %v", err)
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
	if _, _, ok, _ := s.PendingMove("ns", "new/path.md"); ok {
		t.Fatal("staged move should be cleared after approval")
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
	if err := s.Review("a", "f.md"); err != nil {
		t.Fatalf("review failed: %v", err)
	}
	if _, _, err := s.Read("b", "folder/f.md"); err != nil {
		t.Fatalf("cross-namespace move failed: %v", err)
	}
}

func TestMoveTool_RejectKeepsFileInPlace(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "x")
	_ = s.Review("ns", "f.md")
	if _, isErr := runTool(s, "move", map[string]any{
		"namespace":    "ns",
		"filename":     "f.md",
		"new_filename": "elsewhere.md",
	}); isErr {
		t.Fatal("move tool errored")
	}
	if err := s.Reject("ns", "f.md"); err != nil {
		t.Fatalf("reject failed: %v", err)
	}
	if _, _, ok, _ := s.PendingMove("ns", "f.md"); ok {
		t.Fatal("staged move should be cleared after reject")
	}
	if c, _, _ := s.Read("ns", "f.md"); c != "x" {
		t.Fatalf("file should be untouched: %q", c)
	}
	if _, _, err := s.Read("ns", "elsewhere.md"); err != ErrNotFound {
		t.Fatal("destination should not exist after reject")
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
	// Staged only — sources still in place until each is approved.
	for _, name := range []string{"journal/may-22.md", "journal/may-23.md"} {
		if _, _, ok, _ := s.PendingMove("brain", name); !ok {
			t.Fatalf("expected staged move on %s", name)
		}
		if err := s.Review("brain", name); err != nil {
			t.Fatalf("review %s failed: %v", name, err)
		}
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
	if _, _, ok, _ := s.PendingMove("ns", "a.md"); ok {
		t.Fatal("first staging not rolled back: a.md still has a staged move")
	}
	if c, _, _ := s.Read("ns", "a.md"); c != "A" {
		t.Fatalf("a.md content changed: %q", c)
	}
	if _, _, err := s.Read("ns", "archive/a.md"); err != ErrNotFound {
		t.Fatal("expected archive/a.md to not exist after rollback")
	}
	if c, _, _ := s.Read("ns", "occupied.md"); c != "X" {
		t.Fatalf("destination clobbered: %q", c)
	}
}

func TestRequestMove_DuplicateStagedDestination(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "A")
	_ = s.Write("ns", "b.md", "B")
	if err := s.RequestMove([]MoveOp{{"ns", "a.md", "ns", "dst.md"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestMove([]MoveOp{{"ns", "b.md", "ns", "dst.md"}}); err != ErrDestinationExists {
		t.Fatalf("expected ErrDestinationExists for already-staged destination, got %v", err)
	}
	// Restaging the same file to the same destination is fine (idempotent).
	if err := s.RequestMove([]MoveOp{{"ns", "a.md", "ns", "dst.md"}}); err != nil {
		t.Fatalf("restaging same move should succeed: %v", err)
	}
}

func TestReview_StagedMoveDestinationTaken(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "A")
	_ = s.Review("ns", "a.md")
	if err := s.RequestMove([]MoveOp{{"ns", "a.md", "ns", "dst.md"}}); err != nil {
		t.Fatal(err)
	}
	// Destination appears after staging.
	_ = s.Write("ns", "dst.md", "taken")
	if err := s.Review("ns", "a.md"); err != ErrDestinationExists {
		t.Fatalf("expected ErrDestinationExists at approval, got %v", err)
	}
	// Nothing applied: source intact, staged move still recorded.
	if c, _, _ := s.Read("ns", "a.md"); c != "A" {
		t.Fatalf("source corrupted: %q", c)
	}
	if _, _, ok, _ := s.PendingMove("ns", "a.md"); !ok {
		t.Fatal("staged move should survive a failed approval")
	}
}

func TestReview_AppliesContentAndMoveTogether(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "v1")
	_ = s.Review("ns", "f.md")
	_ = s.Write("ns", "f.md", "v2")
	_ = s.InsertComment("ns", "f.md", "edit + rename")
	if err := s.RequestMove([]MoveOp{{"ns", "f.md", "ns", "renamed.md"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Review("ns", "f.md"); err != nil {
		t.Fatalf("review failed: %v", err)
	}
	content, newVal, _, err := s.ReadEntry("ns", "renamed.md")
	if err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if content != "v2" || newVal.Valid {
		t.Fatalf("expected content=v2 new=NULL at destination, got %q / %v", content, newVal)
	}
	comments, _ := s.ListComments("ns", "renamed.md", true, 20, 0)
	if len(comments) != 1 || !comments[0].Reviewed {
		t.Fatalf("expected 1 reviewed comment at destination, got %+v", comments)
	}
}

func TestPendingCounts_IncludeStagedMoves(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "x")
	_ = s.Review("ns", "f.md")
	if n, _ := s.GlobalPendingCount(); n != 0 {
		t.Fatalf("expected 0 pending, got %d", n)
	}
	if err := s.RequestMove([]MoveOp{{"ns", "f.md", "ns", "g.md"}}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.GlobalPendingCount(); n != 1 {
		t.Fatalf("expected 1 pending after staging move, got %d", n)
	}
	if n, _ := s.NamespacePendingCount("ns"); n != 1 {
		t.Fatalf("expected namespace pending 1, got %d", n)
	}
	files, _ := s.List("ns")
	if len(files) != 1 || !files[0].HasPending {
		t.Fatalf("List should flag staged move as pending: %+v", files)
	}
	pending, _ := s.PendingFiles()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending file, got %d", len(pending))
	}
	if pending[0].MoveToNamespace != "ns" || pending[0].MoveToFilename != "g.md" {
		t.Fatalf("PendingFiles should carry move destination, got %+v", pending[0])
	}
}

func TestMoveManyTool_EmptyArray(t *testing.T) {
	s := setupStore(t)
	res, isErr := runTool(s, "move_many", map[string]any{"moves": []any{}})
	if isErr {
		t.Fatalf("empty moves should be a no-op, got: %v", res)
	}
}
