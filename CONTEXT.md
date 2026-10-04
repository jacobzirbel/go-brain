# gobrain

gobrain is a persistent, human-reviewed memory store for Claude sessions: markdown files in namespaces, readable and writable by agents over MCP, with every agent change approved or rejected by JZ. It is the storage layer under the **memory loop** — the session practice (boot → work → wrap, tiers, sweep) whose vocabulary lives in the brain's own namespaces, not here.

## Language

Synonyms are listed so readers can map existing code and docs onto a term; they aren't banned.

### Memory

**Namespace**:
A named, isolated set of files, and the unit a session boots into. Names are case-insensitive.

**Tag**:
A free-form label JZ attaches to a namespace in the UI to group and filter namespaces. Not exposed over MCP.

**Boot**:
A session committing to its primary namespace — "this is where I'm starting" — and loading that namespace's index and state.

**Primary namespace**:
The one namespace a session has booted into.

**Index**:
A namespace's standing instructions for how to work in it (`index.md`).

**State**:
A namespace's snapshot of where things stand now (`state.md`).

**Global**:
Shared memory every session can read and write alongside its own namespace. Never booted, and not a namespace — though today it's addressed as `namespace: "global"`.
_Synonyms_: global namespace, global layer

### Files

**File**:
A named markdown document in a namespace.
_Synonyms_: entry (`entries` table)

**Folder**:
A slash-prefix shared by filenames. It has no existence of its own: an empty folder can't exist.

**Section**:
A heading and its body within a file, addressable by heading text or slug. A file's H1 is skipped because the filename serves as the title.

### Lifecycle

**Archived**:
A file moved under `archived/` — kept and rereadable, but out of the way of default listing and search.

**Deleted**:
A file moved under `deleted/` by an agent's remove — hidden even from archive views, and rejectable like any pending move. Distinct from a hard delete, which only JZ can do and which is permanent.

**Closed**:
A namespace hidden from every agent tool until JZ reopens it. Applies to namespaces only, never files.
_Synonyms_: archive/reopen (a `store.go` comment — not to be confused with an archived file)

### Review

**Comment**:
The writer's one-line explanation of why a change was made, attached to that change — like a commit message.
_Synonyms_: change note

**Current version**:
A file's content as every read returns it, pending or not. It is the file's real content.

**Reviewed version**:
The most recent version of a file JZ has reviewed — what a pending file is diffed against and what Reject restores. Records that JZ saw it, not that it's more correct than the current version.
_Synonyms_: last reviewed, `content` (the column)

**Pending**:
A file with a change JZ hasn't yet approved or rejected — new content, a move, or both.
_Synonyms_: unreviewed, `new` (the column)

**Approve**:
JZ accepting a pending file's changes, making its current version the reviewed version.
_Synonyms_: review (`Review()` in code)

**Reject**:
JZ discarding a pending file's changes, restoring its reviewed version and location.

**Review Queue**:
Every pending file across all namespaces, worked through in the UI.
_Synonyms_: inbox (the UI's label — not the `inbox/` space in `global`)
