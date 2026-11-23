package main

import (
	"testing"
	"time"
)

func TestRecent_SinceFilterAndCounts(t *testing.T) {
	s := setupStore(t)
	for _, w := range [][2]string{{"a", "state.md"}, {"a", "journal/x.md"}, {"b", "index.md"}, {"a", "archived/old.md"}} {
		if err := s.Write(w[0], w[1], "body"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE entries SET updated_at = '2020-01-01 00:00:00' WHERE namespace='b'`); err != nil {
		t.Fatal(err)
	}

	res, isErr := runTool(s, "recent", map[string]any{"since": "2021-01-01"})
	if isErr {
		t.Fatalf("error: %+v", res)
	}
	r := res.(recentResult)
	if r.Total != 2 || r.Namespaces["a"] != 2 || r.Namespaces["b"] != 0 {
		t.Fatalf("want 2 changes in a only (archived excluded, b too old), got %+v", r)
	}

	res, _ = runTool(s, "recent", map[string]any{"since": "2019-01-01", "namespace": "b"})
	if r := res.(recentResult); r.Total != 1 || r.Files[0].Filename != "index.md" {
		t.Fatalf("namespace filter: got %+v", r)
	}

	res, _ = runTool(s, "recent", map[string]any{"since": "2019-01-01", "path": "journal/**"})
	if r := res.(recentResult); r.Total != 1 || r.Files[0].Filename != "journal/x.md" {
		t.Fatalf("path filter: got %+v", r)
	}

	res, _ = runTool(s, "recent", map[string]any{"since": "2019-01-01", "limit": float64(1)})
	if r := res.(recentResult); len(r.Files) != 1 || !r.Truncated || r.Total != 3 {
		t.Fatalf("limit: got %+v", r)
	}

	if _, isErr := runTool(s, "recent", map[string]any{"since": "yesterday"}); !isErr {
		t.Fatal("bad since should error")
	}
}

func TestRecent_DefaultSinceIs24h(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	got, err := parseSince("", now)
	if err != nil || !got.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("got %v %v", got, err)
	}
}
