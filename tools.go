package main

import (
	"errors"
	"fmt"
)

var mcpTools = []map[string]any{
	{
		"name":        "read",
		"description": "Get the contents of a file in a namespace",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string", "description": "Namespace for the file"},
				"filename":  map[string]any{"type": "string", "description": "Name of the file"},
			},
			"required": []string{"namespace", "filename"},
		},
	},
	{
		"name":        "write",
		"description": "Overwrite a file in a namespace (creates if not exists)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"filename":  map[string]any{"type": "string"},
				"content":   map[string]any{"type": "string"},
			},
			"required": []string{"namespace", "filename", "content"},
		},
	},
	{
		"name":        "append",
		"description": "Append text to a file in a namespace (creates if not exists; adds newline before new content if file is non-empty)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"filename":  map[string]any{"type": "string"},
				"content":   map[string]any{"type": "string"},
			},
			"required": []string{"namespace", "filename", "content"},
		},
	},
	{
		"name":        "list",
		"description": "List files in a namespace",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
			},
			"required": []string{"namespace"},
		},
	},
}

func runTool(store Store, name string, args map[string]any) (result any, isError bool) {
	str := func(key string) string {
		v, _ := args[key].(string)
		return v
	}

	switch name {
	case "read":
		content, updatedAt, err := store.Read(str("namespace"), str("filename"))
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "not found"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]string{"content": content, "updated_at": updatedAt}, false

	case "write":
		if err := store.Write(str("namespace"), str("filename"), str("content")); err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "append":
		if err := store.Append(str("namespace"), str("filename"), str("content")); err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "list":
		files, err := store.List(str("namespace"))
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]any{"files": files}, false

	default:
		return map[string]string{"error": fmt.Sprintf("unknown tool: %s", name)}, true
	}
}
