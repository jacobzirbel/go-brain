package main

import (
	"strings"
	"testing"
	"time"
)

func TestWrap_NamesFromClockAndReplacesState(t *testing.T) {
	s := setupStore(t)
	if err := s.Write("p", "state.md", "old"); err != nil {
		t.Fatal(err)
	}
	wrapNow = func() time.Time { return time.Date(2026, 11, 23, 16, 45, 9, 0, time.UTC) }
	defer func() { wrapNow = func() time.Time { return time.Now().UTC() } }()

	res, isErr := runTool(s, "wrap", map[string]any{
		"namespace": "P", "slug": "Wrap Tool: shipped!", "journal": "did it", "state": "new state",
	})
	if isErr {
		t.Fatalf("error: %+v", res)
	}
	r := res.(wrapResult)
	if r.Journal != "journal/2026-11-23T1645_wrap-tool-shipped.md" || !r.StateUpdated {
		t.Fatalf("got %+v", r)
	}
	if body, _, _ := s.Read("p", r.Journal); body != "did it" {
		t.Fatalf("journal body %q", body)
	}
	if body, _, _ := s.Read("p", "state.md"); body != "new state" {
		t.Fatalf("state body %q", body)
	}

	// Same minute: suffixed, and state untouched when omitted.
	res, _ = runTool(s, "wrap", map[string]any{"namespace": "p", "slug": "wrap tool shipped", "journal": "again"})
	r = res.(wrapResult)
	if r.Journal != "journal/2026-11-23T1645_wrap-tool-shipped-2.md" || r.StateUpdated {
		t.Fatalf("collision: got %+v", r)
	}
	if body, _, _ := s.Read("p", "state.md"); body != "new state" {
		t.Fatalf("state changed without state arg: %q", body)
	}
}

func TestWrap_CreatesStateAndValidates(t *testing.T) {
	s := setupStore(t)
	res, isErr := runTool(s, "wrap", map[string]any{"namespace": "q", "slug": "first", "journal": "j", "state": "s"})
	if isErr {
		t.Fatalf("error: %+v", res)
	}
	if body, _, _ := s.Read("q", "state.md"); body != "s" {
		t.Fatalf("state not created: %q", body)
	}
	for _, args := range []map[string]any{
		{"namespace": "q", "slug": "!!!", "journal": "j"},
		{"namespace": "q", "slug": "x", "journal": ""},
		{"namespace": "q", "slug": "x", "journal": "j", "state": ""},
	} {
		if res, isErr := runTool(s, "wrap", args); !isErr {
			t.Fatalf("want error for %v, got %+v", args, res)
		}
	}
	if !strings.HasPrefix(wrapSlug(strings.Repeat("a", 80)), strings.Repeat("a", 60)) || len(wrapSlug(strings.Repeat("a", 80))) != 60 {
		t.Fatal("slug cap")
	}
}
