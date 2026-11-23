package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// lint: metadata-and-pattern checks that find upkeep problems without anyone
// reading every file. Each finding names the file and why, so a sweep reads
// only what lint points at.

var lintToolDef = toolDef{
	Name: "lint",
	Description: "Upkeep checks over one namespace or all of them, without reading content yourself. Findings: " +
		"missing_core (no index.md or state.md); stale_state (another non-journal file changed more than a day after state.md — state likely lags); " +
		"expired_check (a line carrying \"(check YYYY-MM-DD)\" whose date has passed — re-verify the line, then bump the date); " +
		"journal_name (journal file not named YYYY-MM-DDTHHMM_slug.md — use the wrap tool, which names entries itself). " +
		"Excludes archived/, deleted/ and closed namespaces.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: map[string]property{
			"namespace": {Type: "string", Description: "One namespace. Omit or pass \"*\" for all."},
			"checks":    {Type: "string", Description: "Optional comma-separated subset: missing_core, stale_state, expired_check, journal_name."},
		},
	},
}

func init() {
	mcpTools = append(mcpTools, lintToolDef)
	toolRunners["lint"] = toolEntry{run: toolLint}
}

// lintNow is the clock lint compares check dates against; tests replace it.
var lintNow = func() time.Time { return time.Now().UTC() }

type LintFinding struct {
	Namespace string `json:"namespace"`
	Filename  string `json:"filename,omitempty"`
	Check     string `json:"check"`
	Detail    string `json:"detail"`
}

type lintResult struct {
	Findings []LintFinding  `json:"findings"`
	Counts   map[string]int `json:"counts"`
	Total    int            `json:"total"`
}

var (
	journalNameRe = regexp.MustCompile(`^journal/(index\.md|\d{4}-\d{2}-\d{2}T\d{4}_[a-z0-9][a-z0-9-]*\.md)$`)
	checkTokenRe  = regexp.MustCompile(`\(check (\d{4}-\d{2}-\d{2})\)`)
)

const staleStateGap = 24 * time.Hour

func toolLint(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	if ns == "*" {
		ns = ""
	}
	want := map[string]bool{}
	for _, c := range strings.Split(a.str("checks"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			want[c] = true
		}
	}
	findings, err := s.Lint(ns, want, lintNow())
	if err != nil {
		return errResult{Error: err.Error()}, true
	}
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Check]++
	}
	return lintResult{Findings: findings, Counts: counts, Total: len(findings)}, false
}

type lintFile struct {
	name    string
	updated time.Time
	body    string
}

// Lint runs the checks in want (all when empty) over namespace ns ("" = all
// open namespaces) and returns findings sorted by namespace then filename.
func (s *SQLiteStore) Lint(ns string, want map[string]bool, now time.Time) ([]LintFinding, error) {
	on := func(c string) bool { return len(want) == 0 || want[c] }
	rows, err := s.db.Query(`
		SELECT e.namespace, e.filename, e.updated_at, COALESCE(e.new, e.content)
		FROM entries e
		WHERE (? = '' OR e.namespace = ?)
		  AND e.filename NOT GLOB 'archive/*'
		  AND e.filename NOT GLOB 'archived/*'
		  AND e.filename NOT GLOB 'deleted/*'
		  AND e.namespace NOT IN (SELECT name FROM namespaces WHERE closed = 1)`, ns, ns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byNS := map[string][]lintFile{}
	for rows.Next() {
		var n, f, u, body string
		if err := rows.Scan(&n, &f, &u, &body); err != nil {
			return nil, err
		}
		t, perr := parseStoreTime(u)
		if perr != nil {
			return nil, perr
		}
		byNS[n] = append(byNS[n], lintFile{name: f, updated: t, body: body})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	today := now.UTC().Format("2006-01-02")
	var out []LintFinding
	for n, files := range byNS {
		var state *lintFile
		hasIndex := false
		for i := range files {
			switch files[i].name {
			case "state.md":
				state = &files[i]
			case "index.md":
				hasIndex = true
			}
		}
		if on("missing_core") {
			if !hasIndex {
				out = append(out, LintFinding{Namespace: n, Check: "missing_core", Detail: "no index.md"})
			}
			if state == nil {
				out = append(out, LintFinding{Namespace: n, Check: "missing_core", Detail: "no state.md"})
			}
		}
		if on("stale_state") && state != nil {
			var newest *lintFile
			for i := range files {
				f := &files[i]
				if f.name == "state.md" || f.name == "index.md" || strings.HasPrefix(f.name, "journal/") {
					continue
				}
				if newest == nil || f.updated.After(newest.updated) {
					newest = f
				}
			}
			if newest != nil && newest.updated.Sub(state.updated) > staleStateGap {
				out = append(out, LintFinding{Namespace: n, Filename: "state.md", Check: "stale_state",
					Detail: fmt.Sprintf("%s changed %s, %d days after state.md (%s)", newest.name,
						newest.updated.Format("2006-01-02"), int(newest.updated.Sub(state.updated).Hours()/24), state.updated.Format("2006-01-02"))})
			}
		}
		for _, f := range files {
			if on("journal_name") && strings.HasPrefix(f.name, "journal/") && !strings.Contains(strings.TrimPrefix(f.name, "journal/"), "/") && !journalNameRe.MatchString(f.name) {
				out = append(out, LintFinding{Namespace: n, Filename: f.name, Check: "journal_name", Detail: "not journal/YYYY-MM-DDTHHMM_slug.md"})
			}
			if on("expired_check") {
				for i, line := range strings.Split(f.body, "\n") {
					for _, m := range checkTokenRe.FindAllStringSubmatch(line, -1) {
						if m[1] < today {
							out = append(out, LintFinding{Namespace: n, Filename: f.name, Check: "expired_check",
								Detail: fmt.Sprintf("line %d: check date %s passed: %s", i+1, m[1], truncateRunes(strings.TrimSpace(line), 120))})
						}
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		if out[i].Filename != out[j].Filename {
			return out[i].Filename < out[j].Filename
		}
		return out[i].Detail < out[j].Detail
	})
	if out == nil {
		out = []LintFinding{}
	}
	return out, nil
}

func parseStoreTime(v string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable updated_at %q", v)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
