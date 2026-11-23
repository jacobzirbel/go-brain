package main

import (
	"strings"
	"testing"
	"time"
)

func TestLint_Checks(t *testing.T) {
	s := setupStore(t)
	for _, w := range [][3]string{
		{"a", "index.md", "x"},
		{"a", "state.md", "- ship it (check 2026-11-01)\n- later (check 2026-12-31)"},
		{"a", "tasks/t.md", "x"},
		{"a", "journal/2026-11-23T1645_good.md", "x"},
		{"a", "journal/2026-07-08.md", "x"},
		{"a", "journal/index.md", "x"},
		{"b", "notes.md", "x"},
		{"c", "index.md", "x"},
		{"c", "state.md", strings.Repeat("y", 4001)},
		{"d", "index.md", "   "},
		{"d", "state.md", "x"},
	} {
		if err := s.Write(w[0], w[1], w[2]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE entries SET updated_at = '2026-11-01 00:00:00' WHERE namespace='a' AND filename='state.md'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE entries SET updated_at = '2026-11-20 00:00:00' WHERE namespace='a' AND filename='tasks/t.md'`); err != nil {
		t.Fatal(err)
	}
	lintNow = func() time.Time { return time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC) }
	defer func() { lintNow = func() time.Time { return time.Now().UTC() } }()

	res, isErr := runTool(s, "lint", map[string]any{})
	if isErr {
		t.Fatalf("error: %+v", res)
	}
	r := res.(lintResult)
	want := map[string]int{"boot_size": 1, "missing_core": 3, "stale_state": 1, "expired_check": 1, "journal_name": 1}
	for k, v := range want {
		if r.Counts[k] != v {
			t.Errorf("%s: want %d, got %d (%+v)", k, v, r.Counts[k], r.Findings)
		}
	}

	res, _ = runTool(s, "lint", map[string]any{"namespace": "a", "checks": "journal_name"})
	if r := res.(lintResult); r.Total != 1 || r.Findings[0].Filename != "journal/2026-07-08.md" {
		t.Fatalf("filtered: %+v", r)
	}
}
