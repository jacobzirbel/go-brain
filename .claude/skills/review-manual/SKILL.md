---
name: review-manual
description: writes the read-this-first map for a diff — targeted review homework, not a summary
disable-model-invocation: true
---

# review-manual

You invoked this to hand JZ a diff for review. Output is a **map**, not a summary: where to look, what to look for, in what order. He reads the code himself — targeted. Instructions below are to you, the running model.

## What this is not
- Not a description of the diff. He can read a diff.
- Not a quality claim. Never "this is clean" / "safe to merge."
- Not a persistent artifact. Ephemeral by design — written for this review, discarded after. Never commit it, never write it to gobrain.

## Scope
Default target is the uncommitted working diff plus any commits on the branch not on `main`. `$ARGUMENTS` overrides (a ref range, a path, a PR number).

## Before writing

1. Get the diff. `git diff main...HEAD` and `git diff` — read the actual changed lines.
2. Run the project's tests. If they fail, stop and fix; a manual over a red build wastes his read.
3. Find the review targets. A target is a line where **you made a judgment call he can't see from the diff alone** — a chosen default, an assumed invariant, an error path you picked, a boundary you drew. Not "complex code." Code is not a target because it's long.
4. Rank targets by *cost if wrong*, not by size of change.

Optional escalation, only if he asked or the diff crosses >5 files: fan out parallel readers on disjoint axes (correctness / integration / future-brittleness), then use their findings to pick targets. The manual is the coalesce sink, not the raw reports.

## Output

Write to `.claude/review-manuals/<branch>-<YYYY-MM-DD>.md` and print the READ and DECIDE sections in chat. The file exists so he can review cold — offline, no session open. Write it self-contained: no "ask me if unclear," no references to this conversation.

Sections, in this order. Omit any that are empty. Never pad.

### 1. What changed
Three sentences max. What it does now that it didn't before, and the one thing most likely to be wrong.

### 2. Read this
The spine of the manual. 3–7 items, ordered by cost-if-wrong. Cap at 7 — a longer list gets skipped whole. Each item:

```
[ ] store.go:412-448 — the dedupe branch                    [4 min]
    Look for: two notes with identical content but different paths.
    I keyed on path, so both survive. If you wanted content-dedupe,
    this is the line that decides it.
    Why you: it's a product call, not a correctness bug. → Appendix B
```

Rules:
- **Point, don't paste.** `file.go:120-155` — he has the repo open. Quote a line only when the point *is* the exact wording.
- Every item names **a specific question he can answer**, not a topic. "Check the locking" is not an item. "Confirm nothing else writes `notes` between line 88 and the commit at 141" is.
- Time estimate on every item. It's what makes the 2-minute ones actually happen.
- Reading order is the *risk* order, not file order. Say so if it jumps around.
- Push anything longer than four lines into an appendix and link it. The list stays scannable.

### 3. Decide
Genuine forks only — where two answers are both defensible and I picked one. State the fork, what I did, and what changes if he flips it. If there's no fork, cut the section; don't manufacture one.

### 4. Skipped
What you deliberately did *not* send him to, one line each, with why. `ui.go` — 300 lines, all mechanical renames. This is load-bearing: it tells him the omissions were chosen, so the short list is trustworthy rather than lazy.

### 5. Appendices
Lettered. One per deep-context item referenced above. This is where the reasoning goes — the alternative you rejected, the data-flow trace, the invariant argument, the upstream constraint. He reads an appendix only when an item sends him there.

Appendices are the pressure valve: they let the READ list stay short without losing the substance. If nothing references an appendix, delete it.

### 6. Marks
Close with the mark protocol, verbatim:

> Mark items as you go. Output is questions, not fixes — bring the list back,
> don't patch it. `?` = don't understand, `!` = think it's wrong,
> `~` = works but I'll hate it in 6 months.

## Discipline
- Never send him somewhere you haven't read yourself.
- An item you can verify is your job, not his. If you could have run it, run it and drop the item.
- Zero targets is a legitimate outcome for a small diff. Say "nothing worth your time here, tests green" and stop. A manual that always has six items trains him to ignore it.
- No type-check or lint gating in the manual. Those are hooks' job; he's rejected them as the review gate.
- Never propose a refactor here. If you spotted one, it goes in an appendix as an observation, unranked.
