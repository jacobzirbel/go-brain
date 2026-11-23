package main

import "strings"

// serverInstructions is sent in the initialize result. Clients that defer tool
// schemas (tool search) still place server instructions in the model's
// context, so this is what keeps the model aware that gobrain's tools exist
// when only their names — or nothing — are loaded. The tool list is built from
// mcpTools, so a tool added in its own file is named here without touching
// main.go.
func serverInstructions() string {
	names := make([]string, 0, len(mcpTools))
	for _, t := range mcpTools {
		names = append(names, t.Name)
	}
	return `gobrain is the user's persistent memory: namespaced markdown files that survive across sessions. ` +
		`Tools (always available from this server): ` + strings.Join(names, ", ") + `. ` +
		`If your client defers tool schemas, load these tools before concluding gobrain is unavailable — ` +
		`never tell the user you cannot read or write gobrain without first trying. ` +
		`Start with boot(namespace) to load a namespace's index.md and state.md. ` +
		`End a session with wrap(namespace, slug, journal, state): the server names the journal entry from its own clock.`
}
