package main

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// wrap: end-of-session flush in one call. The server names the journal entry
// from its own clock, so sessions stop inventing timestamps (T0000, T1200,
// bare dates, missing times), and the state.md snapshot lands in the same
// transaction as the journal entry, so a wrap is never half-applied.

var wrapToolDef = toolDef{
	Name: "wrap",
	Description: "End-of-session flush. Writes a journal entry at journal/<UTC now as YYYY-MM-DDTHHMM>_<slug>.md — the server picks the timestamp, so never put a date or time in the slug — and, when `state` is given, replaces state.md with it, both in one transaction. " +
		"state.md is a snapshot of what is true now, not a log: pass the whole new file, history belongs in the journal entry. Both writes are pending review like any other write. Returns the journal filename used.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: map[string]property{
			"namespace": {Type: "string", Description: "Namespace being wrapped."},
			"slug":      {Type: "string", Description: "A few words naming what the session did, e.g. \"wrap-tool-shipped\". Lowercased; anything but a-z, 0-9 and - becomes -. No date."},
			"journal":   {Type: "string", Description: "Journal entry body: what happened this session and why."},
			"state":     {Type: "string", Description: "Optional. The complete new state.md. Omit to leave state.md untouched."},
			"comment":   {Type: "string", Description: commentDescription},
		},
		Required: []string{"namespace", "slug", "journal"},
	},
}

func init() {
	mcpTools = append(mcpTools, wrapToolDef)
	toolRunners["wrap"] = toolEntry{needsNS: true, run: toolWrap}
}

// wrapNow is the clock wrap names entries from; tests replace it.
var wrapNow = func() time.Time { return time.Now().UTC() }

type wrapResult struct {
	OK           bool   `json:"ok"`
	Journal      string `json:"journal"`
	StateUpdated bool   `json:"state_updated"`
}

var slugJunk = regexp.MustCompile(`[^a-z0-9]+`)

func wrapSlug(raw string) string {
	s := slugJunk.ReplaceAllString(strings.ToLower(raw), "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-")
	}
	return s
}

func toolWrap(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	slug := wrapSlug(a.str("slug"))
	if slug == "" {
		return errResult{Error: "missing required argument: slug"}, true
	}
	if a.str("journal") == "" {
		return errResult{Error: "missing required argument: journal"}, true
	}
	_, hasState := a["state"]
	state := a.str("state")
	if hasState && state == "" {
		return errResult{Error: "state is empty; omit it to leave state.md untouched"}, true
	}
	name, err := s.Wrap(ns, slug, a.str("journal"), state, wrapNow())
	if err != nil {
		return storeError(err), true
	}
	if c := a.str("comment"); c != "" {
		if err := s.InsertComment(ns, name, c); err != nil {
			return errResult{Error: err.Error()}, true
		}
		if state != "" {
			if err := s.InsertComment(ns, "state.md", c); err != nil {
				return errResult{Error: err.Error()}, true
			}
		}
	}
	return wrapResult{OK: true, Journal: name, StateUpdated: state != ""}, false
}

// Wrap creates the journal entry and, when state is non-empty, sets state.md
// (creating it if absent) in one transaction. A name collision within the same
// minute gets a -2, -3 … suffix. Returns the journal filename.
func (s *SQLiteStore) Wrap(namespace, slug, journal, state string, now time.Time) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	base := fmt.Sprintf("journal/%s_%s", now.UTC().Format("2006-01-02T1504"), slug)
	name := ""
	for i := 1; i <= 50; i++ {
		cand := base + ".md"
		if i > 1 {
			cand = fmt.Sprintf("%s-%d.md", base, i)
		}
		var one int
		err := tx.QueryRow(`SELECT 1 FROM entries WHERE namespace=? AND filename=?`, namespace, cand).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			name = cand
			break
		}
		if err != nil {
			return "", err
		}
	}
	if name == "" {
		return "", fmt.Errorf("no free journal name for %s", base)
	}
	if err := wrapPut(tx, namespace, name, normalizeLineEndings(journal)); err != nil {
		return "", err
	}
	if state != "" {
		if err := wrapPut(tx, namespace, "state.md", normalizeLineEndings(state)); err != nil {
			return "", err
		}
	}
	if err := upsertNamespace(tx, namespace); err != nil {
		return "", err
	}
	return name, tx.Commit()
}

// wrapPut sets entries.new for a file, inserting the row if it is absent.
func wrapPut(tx *sql.Tx, namespace, filename, content string) error {
	var rowid int64
	var body string
	err := tx.QueryRow(`
		INSERT INTO entries (namespace, filename, new, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(namespace, filename) DO UPDATE SET new = excluded.new, updated_at = excluded.updated_at
		RETURNING rowid, COALESCE(new, content)
	`, namespace, filename, content).Scan(&rowid, &body)
	if err != nil {
		return err
	}
	return upsertFTS(tx, rowid, namespace, filename, body)
}
