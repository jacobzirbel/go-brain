# go-brain

## Commands

**Test:**
```
CGO_ENABLED=1 go test -tags fts5 ./...
```
`-tags fts5` is required — `mattn/go-sqlite3` must be built with fts5 support or every test fails at store setup with "no such module: fts5".
