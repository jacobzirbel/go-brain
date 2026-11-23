package main

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// dangling_link: pointers in a file that resolve to nothing live in gobrain.
// Deliberately narrow (see go-brain experiment 04-link-integrity): it checks
// only forms that are unambiguously gobrain pointers — backticked or bare
// paths that contain a slash and end in .md, markdown links to .md files, and
// [[wiki]] links — and reports two classes:
//   archived: the target exists only under archived/ or archive/
//   missing:  the target exists nowhere
// A path resolves if it exists from the namespace root, from the source file's
// folder, in another namespace named by its first segment (global/ included),
// or by unique basename in the namespace. Journal entries are history and are
// skipped as sources.

var (
	linkFenceRe    = regexp.MustCompile("(?s)```.*?```")
	linkNonNLRe    = regexp.MustCompile(`[^\n]`)
	linkTickRe     = regexp.MustCompile("`([^`\n]+)`")
	linkBareRe     = regexp.MustCompile(`(?:^|[\s(→])((?:\.\./)*(?:[a-z0-9][a-z0-9_\-.]*/)+[A-Za-z0-9_\-.]*\.md)\b`)
	linkMarkdownRe = regexp.MustCompile(`\]\(([^)\s#]+\.md)(?:#[^)]*)?\)`)
	linkWikiRe     = regexp.MustCompile(`\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|[^\]]*)?\]\]`)
	linkPathRe     = regexp.MustCompile(`^(?:\.\./)*(?:[A-Za-z0-9_\-.]+/)+[A-Za-z0-9_\-.]+\.md$`)
	linkWikiNameRe = regexp.MustCompile(`^[A-Za-z0-9_\-. ]{1,80}$`)
)

// Path first segments that are repo or filesystem roots, not gobrain folders.
var linkRepoRoots = map[string]bool{
	"server": true, "client": true, "mcp": true, "lib": true, "shared": true, "scripts": true,
	"docs": true, "src": true, "cmd": true, "internal": true, "pkg": true, "app": true,
	"web": true, "test": true, "tests": true, ".claude": true, ".github": true, "node_modules": true,
}

func linkSkippable(p string) bool {
	if strings.ContainsAny(p, "<>{}*~$") || strings.Contains(p, "YYYY") || strings.Contains(p, "NN-") ||
		strings.Contains(p, "slug") || strings.HasPrefix(p, "/") || strings.Contains(p, "://") {
		return true
	}
	first := strings.SplitN(strings.TrimLeft(p, "./"), "/", 2)[0]
	if linkRepoRoots[first] {
		return true
	}
	base := strings.TrimSuffix(path.Base(p), ".md")
	return base != "" && base == strings.ToUpper(base) && strings.ToLower(base) != base // CLAUDE.md, README.md
}

type linkIndex struct {
	files map[string]map[string]bool     // ns -> filename -> true (all files, archived included)
	base  map[string]map[string][]string // ns -> basename -> live filenames
}

func isArchivedName(f string) bool {
	return strings.HasPrefix(f, "archived/") || strings.HasPrefix(f, "archive/") || strings.HasPrefix(f, "deleted/")
}

func (s *SQLiteStore) linkIndex() (*linkIndex, error) {
	rows, err := s.db.Query(`SELECT namespace, filename FROM entries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ix := &linkIndex{files: map[string]map[string]bool{}, base: map[string]map[string][]string{}}
	for rows.Next() {
		var n, f string
		if err := rows.Scan(&n, &f); err != nil {
			return nil, err
		}
		if ix.files[n] == nil {
			ix.files[n] = map[string]bool{}
			ix.base[n] = map[string][]string{}
		}
		ix.files[n][f] = true
		if !isArchivedName(f) {
			b := path.Base(f)
			ix.base[n][b] = append(ix.base[n][b], f)
		}
	}
	return ix, rows.Err()
}

func (ix *linkIndex) live(ns, f string) bool {
	return ix.files[ns][f] && !isArchivedName(f)
}

func (ix *linkIndex) archived(ns, f string) string {
	for _, pre := range []string{"archived/", "archive/"} {
		if ix.files[ns][pre+f] {
			return pre + f
		}
	}
	b := path.Base(f)
	for name := range ix.files[ns] {
		if isArchivedName(name) && path.Base(name) == b {
			return name
		}
	}
	return ""
}

// resolvePath returns "" when p resolves live, otherwise "archived" or
// "missing" and, for archived, where the copy sits.
func (ix *linkIndex) resolvePath(ns, src, p string) (string, string) {
	type cand struct{ ns, f string }
	var cands []cand
	if !strings.HasPrefix(p, "../") {
		cands = append(cands, cand{ns, p})
	}
	cands = append(cands, cand{ns, path.Join(path.Dir(src), p)})
	if parts := strings.SplitN(p, "/", 2); len(parts) == 2 {
		if _, ok := ix.files[parts[0]]; ok {
			cands = append(cands, cand{parts[0], parts[1]})
		}
		if parts[0] == "global" {
			cands = append(cands, cand{"global", parts[1]})
		}
	}
	for _, c := range cands {
		if ix.live(c.ns, c.f) {
			return "", ""
		}
	}
	if len(ix.base[ns][path.Base(p)]) == 1 { // under-qualified but findable
		return "", ""
	}
	for _, c := range cands {
		if a := ix.archived(c.ns, c.f); a != "" {
			if c.ns != ns {
				a = c.ns + "/" + a
			}
			return "archived", a
		}
	}
	return "missing", ""
}

func (ix *linkIndex) resolveWiki(ns, name string) (string, string) {
	b := strings.TrimSpace(name)
	if !strings.HasSuffix(b, ".md") {
		b += ".md"
	}
	if len(ix.base[ns][b]) > 0 {
		return "", ""
	}
	if a := ix.archived(ns, b); a != "" {
		return "archived", a
	}
	return "missing", ""
}

func lineOf(body string, off int) int { return strings.Count(body[:off], "\n") + 1 }

// lintLinks checks every live, non-journal file in byNS.
func (s *SQLiteStore) lintLinks(byNS map[string][]lintFile) ([]LintFinding, error) {
	ix, err := s.linkIndex()
	if err != nil {
		return nil, err
	}
	var out []LintFinding
	for n, files := range byNS {
		for _, f := range files {
			if strings.HasPrefix(f.name, "journal/") {
				continue
			}
			// Blank out fenced code, keeping offsets so line numbers stay right.
			body := linkFenceRe.ReplaceAllStringFunc(f.body, func(m string) string {
				return linkNonNLRe.ReplaceAllString(m, " ")
			})
			seen := map[string]bool{}
			report := func(off int, ptr, class, where string) {
				key := ptr + "|" + class
				if seen[key] {
					return
				}
				seen[key] = true
				d := fmt.Sprintf("line %d: %s → %s", lineOf(body, off), ptr, class)
				if where != "" {
					d += " (" + where + ")"
				}
				out = append(out, LintFinding{Namespace: n, Filename: f.name, Check: "dangling_link", Detail: d})
			}
			checkPath := func(off int, p string) {
				p = strings.TrimSuffix(strings.TrimPrefix(p, "./"), ".")
				if !linkPathRe.MatchString(p) || linkSkippable(p) {
					return
				}
				if class, where := ix.resolvePath(n, f.name, p); class != "" {
					report(off, p, class, where)
				}
			}
			for _, m := range linkTickRe.FindAllStringSubmatchIndex(body, -1) {
				checkPath(m[2], body[m[2]:m[3]])
			}
			noTicks := linkTickRe.ReplaceAllStringFunc(body, func(m string) string { return strings.Repeat(" ", len(m)) })
			for _, m := range linkBareRe.FindAllStringSubmatchIndex(noTicks, -1) {
				checkPath(m[2], noTicks[m[2]:m[3]])
			}
			for _, m := range linkMarkdownRe.FindAllStringSubmatchIndex(body, -1) {
				if p := body[m[2]:m[3]]; !strings.HasPrefix(p, "http") {
					checkPath(m[2], p)
				}
			}
			for _, m := range linkWikiRe.FindAllStringSubmatchIndex(body, -1) {
				name := body[m[2]:m[3]]
				if !linkWikiNameRe.MatchString(name) || strings.Count(name, " ") > 0 {
					continue
				}
				if class, where := ix.resolveWiki(n, name); class != "" {
					report(m[2], "[["+name+"]]", class, where)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Detail < out[j].Detail })
	return out, nil
}
