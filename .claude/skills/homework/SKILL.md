---
name: homework
description: everything that needs JZ — decisions, checks, risks, things to know
disable-model-invocation: true
---

# homework

You invoked this to hand JZ the complete list of what needs him. Session-wide, not diff-scoped — if a diff needs a deep read, that's `/review-manual`, and this list links to it rather than duplicating it.

Instructions below are to you, the running model.

## The filter

Only four things reach him. Everything else is your job — do it instead of listing it.

| Category | Because |
|---|---|
| **Intent** | Only he knows if this is what he meant. |
| **Ground truth you can't reach** | His machine, prod, real data, the room, an external system, a credential. |
| **Taste** | Naming, shape, good-enough, which tradeoff he wants. |
| **Risk acceptance** | Irreversible, outward-facing, or costs money. |

If you can't write a non-embarrassing "why you and not me," the item is yours. Delete it and go do it.

## Sweep

Cover all of it before writing — the point of this skill is *nothing gets left in your head*:

1. **This session.** Every fork you resolved by picking a default. Every assumption you ran with. Every "I'll mention this later" you didn't.
2. **The working tree.** Uncommitted changes, unpushed commits, anything half-built.
3. **The namespace.** `state.md` open threads — which ones this session touched, which have gone stale.
4. **What you didn't do.** Scope you dropped, blocked, or deliberately skipped.

## Output

Print in chat. Sections in this order, ordered within each by cost-if-ignored. Omit empty sections — never pad to look thorough.

### DECIDE
Forks where two answers are both defensible. Each one: the fork, what you did anyway, what changes if he flips it. Mark `[blocking]` only if you genuinely cannot proceed — otherwise state the default and keep moving. Silence must be a valid answer.

```
[ ] Dedupe on content-hash or path?
    Did: path. Flip → intentional duplicates collapse.        [30s read]
```

### VERIFY
Things only he can check. Exact command or action, expected result, what it means if it differs.

```
[ ] Run the suite on your machine — CGO/fts5 wiring is env-specific.
    Expect: green. Red → tell me, don't debug it.             [1 min]
```

### RISK
Already done, he owns it now. Irreversible, outward-facing, or costs money. One line each, no hedging.

### KNOW
No action. Behavior that changed under him, assumptions you ran with, something that'll surprise him later. Bullets, not checkboxes — nothing to tick.

### NOT DONE
Scope you dropped, and why. Blocked, out of scope, or needs something from him first. This is load-bearing: it's how he knows the short list is chosen rather than incomplete.

### STALE
Open threads that have been open long enough to rot — from `state.md`, not from this session. Each: the thread, how long it's sat, and the one move that would close it. Cap at 3. This is the only section that looks past the current session.

## Discipline

- **No inflation.** Hedging your uncertainty into "please verify" is laundering your work onto him. Verify it yourself.
- **No fake blocking.** Anything you could answer by reading code is not a question. Blocking items stall the whole task — reserve them for cases where guessing wrong makes the work useless.
- **Defaults always.** Every DECIDE ships with what you already did. He should be able to ignore the entire list and still have working software.
- **Empty is a real answer.** "No homework" is the correct output for most sessions. A list that's always full is a list he stops reading.
- **Cold-readable.** He may read this with no session open. No "as we discussed," no pronouns pointing at chat history.
- **Time estimate on every actionable item.** It's what gets the 30-second ones done instead of deferred with the rest.

## Optional: queue tagging

If he's running the dead-drop lists, tag each actionable item with its tier so it can be filed without re-reading:

- `#daily` — goes stale fast (diffs, anything blocking tomorrow)
- `#sprint` — bounded, clears this sprint (design forks, drafts)
- `#graze` — no deadline, drain whenever (reading, spine-traces)

Untagged is fine when he isn't running the queue. Don't ask which mode he's in — infer from whether the lists have been mentioned.
