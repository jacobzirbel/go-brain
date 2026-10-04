# go-brain

## Commands

**Test:**
```
CGO_ENABLED=1 go test -tags fts5 ./...
```
`-tags fts5` is required — `mattn/go-sqlite3` must be built with fts5 support or every test fails at store setup with "no such module: fts5".

## Agent skills

### Issue tracker

Issues live as local markdown under `.scratch/<feature-slug>/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`), recorded as a `Status:` line in each issue file. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
