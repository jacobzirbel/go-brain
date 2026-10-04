# Agent writes go live before review

An agent's write takes effect immediately — every later read, by any session, sees it — and JZ reviews afterwards. We trust the agent to write down what's currently in the conversation accurately; review exists to catch occasional drift, not to gate correctness. So review is a single diff against the reviewed version, and intermediate versions between reviews are discarded. A gate that hid pending changes from agents was rejected: the next session would boot on stale memory until JZ cleared the Inbox, making JZ's attention the bottleneck of the loop.

## Consequences

- Rejecting reverts to the reviewed version, discarding every change since — not just the latest.
- To agents, a rejection is just another outside edit, so agents don't need any review-specific handling.
- Full version history (every version kept, reviewed versions marked like named versions) is a planned nice-to-have; it doesn't change what review compares against.
