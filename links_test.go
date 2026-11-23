package main

import "testing"

func TestLint_DanglingLinks(t *testing.T) {
	s := setupStore(t)
	for _, w := range [][3]string{
		{"a", "index.md", "Map: `decisions/live.md`, `decisions/old.md`, `tasks/gone.md`, `b/notes.md`, `global/inbox/x.md`, [[live]], [[nowhere]]\n" +
			"Bare: see decisions/live.md and roadmap/missing.md.\nTemplate `journal/<slug>.md`, repo `server/x.md`, `CLAUDE.md`.\n```\n`fenced/gone.md`\n```\n[m](tasks/gone.md)"},
		{"a", "state.md", "x"},
		{"a", "decisions/live.md", "x"},
		{"a", "decisions/sub.md", "sibling `live.md` and `../index.md`"},
		{"a", "archived/decisions/old.md", "x"},
		{"a", "journal/2026-11-01T0000_x.md", "`tasks/gone.md`"},
		{"b", "index.md", "x"},
		{"b", "state.md", "x"},
		{"b", "notes.md", "x"},
		{"global", "inbox/x.md", "x"},
	} {
		if err := s.Write(w[0], w[1], w[2]); err != nil {
			t.Fatal(err)
		}
	}
	res, isErr := runTool(s, "lint", map[string]any{"namespace": "a", "checks": "dangling_link"})
	if isErr {
		t.Fatalf("error: %+v", res)
	}
	r := res.(lintResult)
	want := map[string]bool{
		"line 1: decisions/old.md → archived (archived/decisions/old.md)": true,
		"line 1: tasks/gone.md → missing":                                 true,
		"line 1: [[nowhere]] → missing":                                   true,
		"line 2: roadmap/missing.md → missing":                            true,
	}
	got := map[string]bool{}
	for _, f := range r.Findings {
		got[f.Detail] = true
		if f.Filename != "index.md" {
			t.Errorf("unexpected source %s: %s", f.Filename, f.Detail)
		}
	}
	for d := range want {
		if !got[d] {
			t.Errorf("missing finding %q; got %+v", d, r.Findings)
		}
	}
	if len(r.Findings) != len(want) {
		t.Errorf("want %d findings, got %d: %+v", len(want), len(r.Findings), r.Findings)
	}
}
