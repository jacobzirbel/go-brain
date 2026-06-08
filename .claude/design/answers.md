# gobrain design — reasoning per question

## 1. Always-read vs. read-on-demand

**Verdict:** Cut the "always read tree" rule. The right always-read set is `state.md` only, with boot instructions embedded as a header comment. Trees and decisions go fully on-demand.

**Reasoning:** Trees look cheap (titles only) but compound. 100 journal entries with descriptive titles is ~1.5K tokens of tree alone, paid every boot, often for nothing. Most sessions only need *now* context. The boot instructions should teach Claude to fetch the tree only when the task references historical work.

**Boot sequence:**
1. Read `state.md` (contains current state + 4-line boot header).
2. If task mentions past work, decisions, or "remember when…" → fetch relevant tree.
3. From tree, fetch specific files by descriptive filename.
4. Before exiting: update `state.md` if anything material changed.

**Tradeoff:** Removing the always-read tree means Claude sometimes misses relevant past context it would have noticed in the title list. Tipping factor: if you find yourself frequently saying "you should have known about X" → put X in `state.md` or add a "see also" line. Don't reintroduce the always-tree.

---

## 2. How small is small enough for state.md?

**Verdict:** 1K token hard cap. Above that, force a split.

**Reasoning:**
- 500 tokens is too restrictive for >1 concurrent project.
- 2K is where state.md becomes a kitchen sink — anything goes in, nothing leaves.
- 1K ≈ 750 words ≈ one screen of bullets. Reviewable at a glance, fits cheaply in every boot.

**What belongs in state.md:** what is true *right now*, what's in progress, what's blocked, what to NOT do (pitfalls discovered recently). Pure present tense.

**What does NOT belong:** rationale, history, completed work, deep technical detail, anything that hasn't been touched in 2 weeks. Those go to `journal/`, `decisions/`, or `reference/`.

**Split signal:** When state.md crosses 1K, the action is *promotion*, not subdivision. Take the oldest bullets and promote each to a journal entry with a descriptive filename. The state file becomes a compact pointer ("see journal/2026-03-bigquery-migration-plan").

---

## 3. Token math: tree + fetches vs one flat file

**Numbers** (per the example: 20 decisions, fetch ~3 relevant):
- Tree: ~20 lines × ~50 chars × 0.25 tok/char = **~250 tok**
- 3 files @ ~300 tok = **~900 tok**
- 4 tool-call overheads @ ~50 tok = **~200 tok**
- **Total: ~1350 tok** vs. one flat file at **~6000 tok**. Multi-file wins ~4.4×.

**Crossover:** Multi-file wins until Claude needs >70% of files. Beyond that, the per-fetch overhead and the tree cost stop being worth it.

**Required conditions for the math to hold:**
1. Filenames must be predictive enough that Claude picks correctly on first try. If picks are wrong and Claude refetches, savings shrink fast.
2. The folder must be at "decisions about distinct topics" granularity, not "every micro-decision."

**Practical rule:** Multi-file when (typical-fetch < 30% of folder) AND (titles predict content). Otherwise consolidate.

---

## 4. index.md as boot instructions — max useful per token

**Verdict:** index.md per-namespace is the wrong unit. Two better options, ranked:

**Best:** Put the universal boot protocol in user's global `CLAUDE.md` once. Per-namespace `state.md` carries only the small bit that's namespace-specific (e.g. "for this namespace, also read `clients.md`"). Saves a per-namespace tool call forever.

**Acceptable fallback:** If you keep per-namespace `index.md`, write it as a script, not a description. Target ≤300 tokens:

```
BOOT:
1. You already read state.md.
2. If task references past work, call tree, then fetch matching titles.
3. Naming: journal/YYYY-MM-DD-slug.md, decisions/slug.md
4. Before stop: if state changed, write state.md. If a decision was made, add to decisions/.
```

No prose. No "this is your memory layer." Claude knows.

---

## 5. Cross-session coherence (last-write-wins clobbering)

**Verdict:** Optimistic concurrency on `state.md`. Cheapest fix that prevents clobbering and forces a real merge.

**How:** `read` already returns `updated_at` ([tools.go:165](tools.go#L165)). Add optional `expected_updated_at` to `write`. If provided and stale, return a `conflict` error. Claude must re-read, merge, and retry. ~20 LOC in [store.go](store.go) + 1 schema field in [tools.go](tools.go).

**Why not append-only / CRDT / section-aware merge:**
- Append-only turns state.md into a journal — defeats the "small hot file" goal.
- CRDT is premature.
- Section-aware merge requires a structured schema and a parser — too much rigidity for a markdown file the user also edits.

**Bonus:** Make `write` refuse if the new content is >2× the previous file size AND no `expected_updated_at` was passed. Catches "session wiped state with a tiny note" mistakes. Belt-and-suspenders, but cheap.

**What this doesn't solve:** Two sessions writing thoughtful, different state in parallel. The second still has to merge by hand. That's correct — there's no auto-merge that's safe for opinions.

---

## 6. Minimum viable boot — useful in 30 seconds

**Verdict:** One tool call. Read `state.md` only. State.md must be self-sufficient for "what's going on right now."

**Implication:** Don't separate boot instructions from state if it means two reads. Either fold the protocol into a global `CLAUDE.md` (Q4 best option), or put a 4-line `<!-- BOOT: -->` header at the top of every state.md.

**30-second boot test:** If a fresh Claude reading only state.md can answer "what should I help you with right now?" in one turn — boot is sufficient. If it has to ask "what are you working on?" — state.md failed.

**Anti-pattern to avoid:** Multi-file boot sequences that load "for completeness." Completeness is the enemy of latency.

---

## 7. Patterns from human external memory

**Highest-value mappings:**

- **PARA (Tiago Forte)** — Projects / Areas / Resources / Archive maps almost 1:1 onto namespace folder layout. Use it. `projects/` = active outcomes, `areas/` = standing responsibilities, `resources/` = reference, `archived/` = done. state.md sits above PARA as a meta-layer.

- **Bullet Journal "migration"** — the act of forwarding unfinished items to a new page. gobrain's equivalent: a recurring compaction where bullets that have aged out of state.md get promoted to journal entries. This is a *required ritual*, not a nice-to-have. Without it, state.md rots.

- **Common law / precedent** — `decisions/` is exactly this. Encourage each decision file to cite prior decisions by filename. Cheap cross-references, builds a graph over time.

**Lower-value or premature:**

- **Zettelkasten** — the compounding value is in human re-reading, not single-pass Claude reads. Skip the dense linking until you have a reader UI worth navigating.

- **Spaced repetition / heat scoring** — interesting for v2 (auto-cool unread files into archive). Premature now.

- **GTD inbox** — useful concept (a single `inbox.md` for "throw it here, sort later"), but only worth adding if you notice yourself wanting to capture something without deciding where it goes. Don't pre-build.
