# Phase 8 requires FTS5; mattn/go-sqlite3 gates that behind a build tag.
# Use `make` / `make test` to ensure it's always set.
BUILD_TAGS = sqlite_fts5

.PHONY: build test vet run

build:
	go build -tags "$(BUILD_TAGS)" -o go-brain .

test:
	go test -tags "$(BUILD_TAGS)" -count=1 ./...

vet:
	go vet -tags "$(BUILD_TAGS)" ./...

run: build
	./go-brain
