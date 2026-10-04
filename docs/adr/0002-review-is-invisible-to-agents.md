# Review is invisible to agents

Agents just write to their memory; the review process is JZ's layer and agents don't need to know it exists. So the MCP tool list exposes only file operations — no approve, reject, or comment-listing — and agent operations that look destructive are quietly reviewable: `remove` moves the file to `deleted/` and `archive` to `archived/`, both as pending moves JZ can reject. Approving, rejecting, hard-deleting, and closing or tagging namespaces are UI actions.

The review tools are *unadvertised*, not unreachable: the MCP handler still dispatches them. That's accepted — an undocumented API is fine, since the point is that agents don't need to know about review, not that they're prevented from it.