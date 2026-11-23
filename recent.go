package main

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// recent: what changed since a time, across namespaces. Lets upkeep passes
// (sweeps, reviews) find what to read without one tree call per namespace.

var recentToolDef = toolDef{
	Name:        "recent",
	Description: "Namespace maintenance: files changed since a time, newest first, with per-namespace counts.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: map[string]property{
			"since":           {Type: "string", Description: "UTC lower bound, inclusive: RFC3339 (\"2026-11-20T00:00:00Z\") or a date (\"2026-11-20\"). Defaults to 24 hours ago."},
			"namespace":       {Type: "string", Description: "Limit to one namespace. Omit or pass \"*\" for all."},
			"path":            {Type: "string", Description: "Optional doublestar glob over filename, e.g. \"journal/**\"."},
			"limit":           {Type: "integer", Description: "Max files returned. Default 50, hard cap 200. Per-namespace counts are not limited."},
			"include_archive": {Type: "boolean", Description: "Include the archived/ prefix. Defaults to false."},
		},
	},
}

func init() {
	mcpTools = append(mcpTools, recentToolDef)
	toolRunners["recent"] = toolEntry{run: toolRecent}
}

type recentResult struct {
	Since      string         `json:"since"`
	Files      []RecentChange `json:"files"`
	Namespaces map[string]int `json:"namespaces"`
	Total      int            `json:"total"`
	Truncated  bool           `json:"truncated,omitempty"`
}

func toolRecent(s *SQLiteStore, a toolArgs) (any, bool) {
	ns := a.ns("namespace")
	if ns == "*" {
		ns = ""
	}
	since, err := parseSince(a.str("since"), time.Now().UTC())
	if err != nil {
		return errResult{Error: err.Error()}, true
	}
	res, err := s.Recent(RecentOptions{
		Namespace:      ns,
		Since:          since,
		Path:           a.str("path"),
		Limit:          a.integer("limit", 50),
		IncludeArchive: a.boolean("include_archive"),
		IncludeClosed:  a.boolean("include_closed"), // unadvertised; JZ-only
	})
	if err != nil {
		return errResult{Error: err.Error()}, true
	}
	return recentResult{
		Since:      since.Format(time.RFC3339),
		Files:      res.Files,
		Namespaces: res.Counts,
		Total:      res.Total,
		Truncated:  res.Total > len(res.Files),
	}, false
}

// parseSince accepts RFC3339 or a bare YYYY-MM-DD (midnight UTC); empty means
// 24 hours before now.
func parseSince(v string, now time.Time) (time.Time, error) {
	if v == "" {
		return now.Add(-24 * time.Hour), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("since must be RFC3339 (2026-11-20T00:00:00Z) or a date (2026-11-20), got %q", v)
}

type RecentOptions struct {
	Namespace      string // "" = all namespaces
	Since          time.Time
	Path           string // optional doublestar glob over filename
	Limit          int    // default 50, hard cap 200
	IncludeArchive bool
	IncludeClosed  bool
}

type RecentChange struct {
	Namespace string `json:"namespace"`
	Filename  string `json:"filename"`
	UpdatedAt string `json:"updated_at"`
	Size      int    `json:"size"`
}

type RecentResult struct {
	Files  []RecentChange
	Counts map[string]int // every match, not just the returned page
	Total  int
}

// Recent lists files whose updated_at is at or after opts.Since, newest first.
// Counts and Total cover every match; Files is capped at Limit.
func (s *SQLiteStore) Recent(opts RecentOptions) (RecentResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	includeArchive, includeClosed := 0, 0
	if opts.IncludeArchive {
		includeArchive = 1
	}
	if opts.IncludeClosed {
		includeClosed = 1
	}
	q := `
		SELECT e.namespace, e.filename, e.updated_at, length(COALESCE(e.new, e.content))
		FROM entries e
		WHERE datetime(e.updated_at) >= datetime(?)
		  AND (? = '' OR e.namespace = ?)
		  AND (? = 1 OR (
			e.filename NOT GLOB 'archive/*'
			AND e.filename NOT GLOB 'archived/*'
		  ))
		  AND e.filename NOT GLOB 'deleted/*'
		  AND (? = 1 OR e.namespace NOT IN (
			SELECT name FROM namespaces WHERE closed = 1
		  ))
		ORDER BY datetime(e.updated_at) DESC, e.namespace, e.filename`
	since := opts.Since.UTC().Format("2006-01-02 15:04:05")
	rows, err := s.db.Query(q, since, opts.Namespace, opts.Namespace, includeArchive, includeClosed)
	if err != nil {
		return RecentResult{}, err
	}
	defer rows.Close()

	res := RecentResult{Files: []RecentChange{}, Counts: map[string]int{}}
	for rows.Next() {
		var c RecentChange
		var size sql.NullInt64
		if err := rows.Scan(&c.Namespace, &c.Filename, &c.UpdatedAt, &size); err != nil {
			return RecentResult{}, err
		}
		c.Size = int(size.Int64)
		if opts.Path != "" {
			match, perr := doublestar.Match(opts.Path, c.Filename)
			if perr != nil {
				return RecentResult{}, perr
			}
			if !match {
				continue
			}
		}
		res.Counts[c.Namespace]++
		res.Total++
		if len(res.Files) < limit {
			res.Files = append(res.Files, c)
		}
	}
	return res, rows.Err()
}
