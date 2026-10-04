# SQLite + FTS5, not files on disk, no embeddings

Memory lives in one SQLite database rather than as files on the VM's filesystem. Exposing the VM filesystem through MCP felt wrong, and a database avoids filesystem permission issues. A file in gobrain also isn't quite a file on disk: it has a current and a reviewed version, moves can be pending, and folders are just name prefixes. SQLite gives that flexibility directly.

Search is FTS5 full-text with no embeddings. There's no reason for embeddings yet: the goal is for an agent to need at most ~40k tokens from its namespace at a time, read whole, with search as a fallback.
