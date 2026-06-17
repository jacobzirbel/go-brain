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

func TestMoveTool_AppliesImmediatelyAndIsRevertible(t *testing.T) {
	s := setupStore(t)
	if err := s.Write("ns", "old/path.md", "hi"); err != nil {
		t.Fatal(err)
	}
	_ = s.Review("ns", "old/path.md")
	res, isErr := runTool(s, "move", map[string]any{
		"namespace":    "ns",
		"filename":     "old/path.md",
		"new_filename": "new/path.md",
	})
	if isErr {
		t.Fatalf("move returned error: %v", res)
	}
	// Move took effect immediately: file lives at the destination, source gone.
	content, _, err := s.Read("ns", "new/path.md")
	if err != nil {
		t.Fatalf("read new path failed: %v", err)
	}
	if content != "hi" {
		t.Fatalf("content lost: %q", content)
	}
	if _, _, err := s.Read("ns", "old/path.md"); err != ErrNotFound {
		t.Fatalf("source should be gone, got err=%v", err)
	}
	// The revert target points back at the origin until reviewed.
	fromNS, fromName, ok, err := s.MovedFrom("ns", "new/path.md")
	if err != nil || !ok {
		t.Fatalf("expected revert target, got ok=%v err=%v", ok, err)
	}
	if fromNS != "ns" || fromName != "old/path.md" {
		t.Fatalf("revert target wrong: %s/%s", fromNS, fromName)
	}
	// Reject moves it back.
	if err := s.Reject("ns", "new/path.md"); err != nil {
		t.Fatalf("reject failed: %v", err)
	}
	if c, _, _ := s.Read("ns", "old/path.md"); c != "hi" {
		t.Fatalf("file not moved back: %q", c)
	}
	if _, _, err := s.Read("ns", "new/path.md"); err != ErrNotFound {
		t.Fatal("destination should be gone after reject")
	}
	if _, _, ok, _ := s.MovedFrom("ns", "old/path.md"); ok {
		t.Fatal("revert target should be cleared after reject")
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
		t.Fatalf("cross-namespace move should apply immediately: %v", err)
	}
	if _, _, err := s.Read("a", "f.md"); err != ErrNotFound {
		t.Fatalf("source should be gone, got %v", err)
	}
}

func TestMoveTool_ApproveBlessesMove(t *testing.T) {
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
	if err := s.Review("ns", "elsewhere.md"); err != nil {
		t.Fatalf("review failed: %v", err)
	}
	// Approving clears the revert target; the file stays at the destination.
	if _, _, ok, _ := s.MovedFrom("ns", "elsewhere.md"); ok {
		t.Fatal("revert target should be cleared after approval")
	}
	if c, _, _ := s.Read("ns", "elsewhere.md"); c != "x" {
		t.Fatalf("file should remain at destination: %q", c)
	}
	if _, _, err := s.Read("ns", "f.md"); err != ErrNotFound {
		t.Fatal("source should stay gone after approval")
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
	// Moves applied immediately; each carries a revert target until approved.
	for _, name := range []string{"archive/journal/may-22.md", "archive/journal/may-23.md"} {
		if _, _, err := s.Read("brain", name); err != nil {
			t.Fatalf("missing destination %s: %v", name, err)
		}
		if _, _, ok, _ := s.MovedFrom("brain", name); !ok {
			t.Fatalf("expected revert target on %s", name)
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
	// Whole batch rolled back: first move undone, a.md still at its origin.
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

func TestMoveForReview_RevertTargetSurvivesRepeatedMoves(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "A")
	_ = s.Review("ns", "a.md")
	// A → B → C without review in between.
	if err := s.MoveForReview([]MoveOp{{"ns", "a.md", "ns", "b.md"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveForReview([]MoveOp{{"ns", "b.md", "ns", "c.md"}}); err != nil {
		t.Fatal(err)
	}
	// Revert target stays the last reviewed location (a.md), not the intermediate.
	fromNS, fromName, ok, _ := s.MovedFrom("ns", "c.md")
	if !ok || fromNS != "ns" || fromName != "a.md" {
		t.Fatalf("expected revert target ns/a.md, got ok=%v %s/%s", ok, fromNS, fromName)
	}
	// Reject lands back at the original reviewed location.
	if err := s.Reject("ns", "c.md"); err != nil {
		t.Fatalf("reject failed: %v", err)
	}
	if c, _, _ := s.Read("ns", "a.md"); c != "A" {
		t.Fatalf("expected file back at a.md, got %q", c)
	}
}

func TestMoveForReview_MoveBackToOriginClearsPointer(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "A")
	_ = s.Review("ns", "a.md")
	if err := s.MoveForReview([]MoveOp{{"ns", "a.md", "ns", "b.md"}}); err != nil {
		t.Fatal(err)
	}
	// Moving back onto the reviewed location makes it clean again — no pending.
	if err := s.MoveForReview([]MoveOp{{"ns", "b.md", "ns", "a.md"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := s.MovedFrom("ns", "a.md"); ok {
		t.Fatal("expected revert target cleared after moving back to origin")
	}
	if n, _ := s.GlobalPendingCount(); n != 0 {
		t.Fatalf("expected 0 pending after round-trip, got %d", n)
	}
}

func TestReject_RevertTargetTaken(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "a.md", "A")
	_ = s.Review("ns", "a.md")
	if err := s.MoveForReview([]MoveOp{{"ns", "a.md", "ns", "b.md"}}); err != nil {
		t.Fatal(err)
	}
	// Someone occupies the original location before the move is rejected.
	_ = s.Write("ns", "a.md", "squatter")
	if err := s.Reject("ns", "b.md"); err != ErrDestinationExists {
		t.Fatalf("expected ErrDestinationExists rejecting onto occupied origin, got %v", err)
	}
	// Nothing undone: file still at b.md with its revert target intact.
	if c, _, _ := s.Read("ns", "b.md"); c != "A" {
		t.Fatalf("file should remain at b.md: %q", c)
	}
	if _, _, ok, _ := s.MovedFrom("ns", "b.md"); !ok {
		t.Fatal("revert target should survive a failed reject")
	}
}

func TestReview_BlessesContentAndMoveTogether(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "v1")
	_ = s.Review("ns", "f.md")
	_ = s.Write("ns", "f.md", "v2") // pending content
	_ = s.InsertComment("ns", "f.md", "edit + rename")
	if err := s.MoveForReview([]MoveOp{{"ns", "f.md", "ns", "renamed.md"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Review("ns", "renamed.md"); err != nil {
		t.Fatalf("review failed: %v", err)
	}
	content, newVal, _, err := s.ReadEntry("ns", "renamed.md")
	if err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if content != "v2" || newVal.Valid {
		t.Fatalf("expected content=v2 new=NULL at destination, got %q / %v", content, newVal)
	}
	if _, _, ok, _ := s.MovedFrom("ns", "renamed.md"); ok {
		t.Fatal("revert target should be cleared after approval")
	}
	comments, _ := s.ListComments("ns", "renamed.md", true, 20, 0)
	if len(comments) != 1 || !comments[0].Reviewed {
		t.Fatalf("expected 1 reviewed comment at destination, got %+v", comments)
	}
}

func TestReject_UndoesContentAndMoveTogether(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "v1")
	_ = s.Review("ns", "f.md")
	_ = s.Write("ns", "f.md", "v2")
	if err := s.MoveForReview([]MoveOp{{"ns", "f.md", "ns", "renamed.md"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Reject("ns", "renamed.md"); err != nil {
		t.Fatalf("reject failed: %v", err)
	}
	// File back at origin with content reverted to the last reviewed value.
	content, newVal, _, err := s.ReadEntry("ns", "f.md")
	if err != nil {
		t.Fatalf("file not moved back: %v", err)
	}
	if content != "v1" || newVal.Valid {
		t.Fatalf("expected content=v1 new=NULL after reject, got %q / %v", content, newVal)
	}
	if _, _, err := s.Read("ns", "renamed.md"); err != ErrNotFound {
		t.Fatal("destination should be gone after reject")
	}
}

func TestPendingCounts_IncludeUnreviewedMoves(t *testing.T) {
	s := setupStore(t)
	_ = s.Write("ns", "f.md", "x")
	_ = s.Review("ns", "f.md")
	if n, _ := s.GlobalPendingCount(); n != 0 {
		t.Fatalf("expected 0 pending, got %d", n)
	}
	if err := s.MoveForReview([]MoveOp{{"ns", "f.md", "ns", "g.md"}}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.GlobalPendingCount(); n != 1 {
		t.Fatalf("expected 1 pending after move, got %d", n)
	}
	if n, _ := s.NamespacePendingCount("ns"); n != 1 {
		t.Fatalf("expected namespace pending 1, got %d", n)
	}
	files, _ := s.List("ns")
	if len(files) != 1 || !files[0].HasPending {
		t.Fatalf("List should flag unreviewed move as pending: %+v", files)
	}
	pending, _ := s.PendingFiles()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending file, got %d", len(pending))
	}
	if pending[0].Filename != "g.md" {
		t.Fatalf("pending file should be at its new location, got %q", pending[0].Filename)
	}
	if pending[0].MovedFromNamespace != "ns" || pending[0].MovedFromFilename != "f.md" {
		t.Fatalf("PendingFiles should carry revert target, got %+v", pending[0])
	}
}

func TestMoveManyTool_EmptyArray(t *testing.T) {
	s := setupStore(t)
	res, isErr := runTool(s, "move_many", map[string]any{"moves": []any{}})
	if isErr {
		t.Fatalf("empty moves should be a no-op, got: %v", res)
	}
}
