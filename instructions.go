package main

import (
	"fmt"
	"strings"
)

// maintenanceTools are for upkeep sessions, not everyday use. The server
// instructions name them only by count so regular sessions don't spend
// context on them; their own short descriptions say what they are.
var maintenanceTools = map[string]bool{"lint": true, "recent": true}

// serverInstructions is sent in the initialize result. Clients that defer tool
// schemas (tool search) still place server instructions in the model's
// context, so this is what keeps the model aware that gobrain's tools exist
// when only their names — or nothing — are loaded. Built from mcpTools, so a
// tool added in its own file needs no edit here unless it is maintenance-only.
func serverInstructions() string {
	var names []string
	extra := 0
	for _, t := range mcpTools {
		if maintenanceTools[t.Name] {
			extra++
			continue
		}
		names = append(names, t.Name)
	}
	more := ""
	if extra > 0 {
		more = fmt.Sprintf(", plus %d more for namespace maintenance", extra)
	}
	return `gobrain is the user's persistent memory: namespaced markdown files that survive across sessions. ` +
		`Tools (always available from this server): ` + strings.Join(names, ", ") + more + `. ` +
		`If your client defers tool schemas, load these tools before concluding gobrain is unavailable — ` +
		`never tell the user you cannot read or write gobrain without first trying. ` +
		`Start with boot(namespace) to load a namespace's index.md and state.md. ` +
		`End a session with wrap(namespace, slug, journal, state): the server names the journal entry from its own clock.`
}
