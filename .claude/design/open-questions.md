# Questions not yet on the list

Ordered roughly by "answer this sooner."

## A. What is a namespace, conceptually?
Project? Identity? Domain? Right now it's a string. The boundary decides everything downstream — when do you split, when do you cross-link. Without a stated definition, namespaces will drift into a single junk drawer or fragment into 50 micro-namespaces. **Pick one model and document it before adding the second namespace.**

## B. When does a session "end"?
The decision says Claude writes at session end. Claude doesn't actually know when the user closes the window. Options: (a) write incrementally after each material change, (b) write on every tool result that mutates intent, (c) rely on a `/save` slash command. (a) is safest but noisy; (c) puts the burden on Jacob. Probably (a) with a guard: don't rewrite state.md if the diff is trivial.

## C. What happens when gobrain is down?
Mobile-first means flaky networks. If the boot read fails, does Claude (a) abort, (b) degrade and tell the user, (c) silently proceed without memory? (c) is the worst — silent context loss. Decide and bake it into the boot instructions.

## D. How does the user audit drift?
If a past session wrote something wrong or stale to state.md, how does Jacob notice? Options: a simple per-file changelog (`state.md.log` with append-only timestamps + diffs), a TUI that shows "last 5 changes," or a periodic "here's what changed" digest. Without this, gobrain accumulates errors silently.

## E. Cross-namespace context bleed
Sometimes work-A needs to reference personal-B (or vice versa). Three approaches: (1) tags inside files, (2) symbolic refs (`@personal/health.md`), (3) duplicate the info. Doing nothing means Claude can't connect dots that should be connected. But doing too much means namespaces stop being isolation boundaries.

## F. Privacy on shared/public devices
Mobile-first usage on public networks. What's in state.md if Jacob's phone is over someone's shoulder? Decide what categories of content (medical, financial, relational) belong in gobrain at all vs. a separate locked namespace vs. nowhere.

## G. Garbage collection policy
Auto-archive after N days unread? Manual only? If manual, who runs the compaction? If Claude runs it, what stops Claude from archiving something Jacob still cares about? Pick a default and a kill switch.

## H. Tooling for the human
gobrain is a memory system for Claude. Does Jacob also need a way to read/edit state.md directly, on mobile, fast? If not, every correction routes through a Claude session, which is slow and lossy. A minimal mobile-friendly viewer/editor (even just a synced markdown file) is probably load-bearing for trust.

## I. Versioning the boot protocol
If the boot protocol lives in global CLAUDE.md and changes, every existing state.md still references the old protocol implicitly. How do you upgrade old namespaces? Probably: boot protocols should be additive and backward-compatible, never breaking. Worth committing to this rule explicitly.

## J. What's the failure mode of a too-helpful past Claude?
A past session decides something is "important" and writes a 400-token paragraph to state.md. Next session inherits it. Compounds. State.md grows by erosion. Need a per-write size budget enforced by the boot instructions: "if your edit pushes state.md past 1K tokens, promote the oldest items first."

## K. Should `write` be split from `update`?
Right now `write` overwrites. An `update`/patch tool that takes a section heading + new content would (a) reduce token cost of partial edits and (b) make merges less destructive. But it adds surface area and assumes the file has structured sections.

## L. The boot read should probably be one tool, not a convention
If "read state.md" is the universal first move, `boot(namespace)` as a dedicated tool would: (1) be self-documenting in the tool list, (2) let the server bundle state + recent activity in one round trip, (3) let the server enforce the size budget. Adds one tool, removes one convention. Probably worth it.
