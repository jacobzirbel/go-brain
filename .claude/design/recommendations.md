# Concrete next moves

In rough priority order. Each item is small and independently shippable.

## Ship next

1. **Optimistic concurrency on write.** Add optional `expected_updated_at` to the `write` tool ([tools.go:25](tools.go#L25)) and `store.Write` ([store.go](store.go)). If stale, return `conflict`. Solves Q5 cheaply.

2. **Drop "always read tree" from boot.** Update boot instructions so trees are pulled only when the task touches history. Reclaims hundreds of tokens per boot.

3. **1K token cap on state.md.** Enforce in the boot protocol as a Claude-side rule ("if your edit pushes past 1K, promote oldest bullets to journal/ first"). Server-side enforcement can come later.

4. **Adopt PARA folder layout in every namespace.** `projects/`, `areas/`, `resources/`, `archived/`. Codify in the boot protocol so all sessions agree.

## Ship soon

5. **Decide: per-namespace `index.md` or global boot in `CLAUDE.md`.** Global is cheaper per session. Per-namespace is more flexible. Pick before adding the third namespace — switching later means editing every namespace.

6. **Define "namespace."** One sentence in the protocol. Without this, namespaces drift.

7. **Failure-mode policy for gobrain unreachable.** Bake into the boot protocol so behavior is predictable.

8. **Mobile read/edit path for state.md.** Even a simple sync to a file Jacob can open on his phone. Without this, every correction costs a Claude session.

## Defer until you feel the pain

9. `boot(namespace)` tool that bundles state + size budget in one round trip.

10. Per-file changelog or audit log for drift detection.

11. Heat scoring / auto-cooling of unread files.

12. `update`/patch tool for section-level writes.

## Don't build

- CRDT-style merge for state.md.
- Zettelkasten-style dense linking before there's a UI that benefits from it.
- SQL/FTS until flat-file performance actually hurts (the brief already calls this out — keep deferring).
