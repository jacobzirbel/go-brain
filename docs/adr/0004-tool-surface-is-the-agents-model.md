# The tool surface is the agent's model of memory

Agents learn how to use gobrain from tool names, shapes, and what's left out, not from instructions. So tools are designed for what they *mean* to the agent. `boot` commits a session to its primary namespace. It doesn't need to return the tree; a separate call is fine. Overwrite is a separate, deliberate tool (ADR-0003). Review doesn't appear at all (ADR-0002). Global is meant to get its own tools rather than being passed as a namespace. Descriptions stay terse and say what the tool does; the tool's shape carries the intent.
