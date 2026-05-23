package main

import (
	"errors"
	"fmt"
)

const filenameDescription = "Name of the file. Slashes create folders for display purposes, e.g. \"journal/2026-05-23.md\" or \"projects/website/notes.md\"."

var mcpTools = []map[string]any{
	{
		"name":        "read",
		"description": "Get the contents of a file in a namespace",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string", "description": "Namespace for the file"},
				"filename":  map[string]any{"type": "string", "description": filenameDescription},
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
				"filename":  map[string]any{"type": "string", "description": filenameDescription},
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
				"filename":  map[string]any{"type": "string", "description": filenameDescription},
				"content":   map[string]any{"type": "string"},
			},
			"required": []string{"namespace", "filename", "content"},
		},
	},
	{
		"name":        "list",
		"description": "List files in a namespace. Filenames may contain slashes to indicate folders.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
			},
			"required": []string{"namespace"},
		},
	},
	{
		"name":        "delete",
		"description": "Delete a file from a namespace",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"filename":  map[string]any{"type": "string", "description": filenameDescription},
			},
			"required": []string{"namespace", "filename"},
		},
	},
	{
		"name":        "move",
		"description": "Rename or move a file. The destination can be in a different namespace and/or a different folder (use slashes in the filename to change folders). Fails if the destination already exists.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace":     map[string]any{"type": "string", "description": "Source namespace"},
				"filename":      map[string]any{"type": "string", "description": "Source filename"},
				"new_namespace": map[string]any{"type": "string", "description": "Destination namespace (defaults to source namespace if omitted)"},
				"new_filename":  map[string]any{"type": "string", "description": "Destination filename, may contain slashes"},
			},
			"required": []string{"namespace", "filename", "new_filename"},
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

	case "delete":
		err := store.Delete(str("namespace"), str("filename"))
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "not found"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	case "move":
		dstNS := str("new_namespace")
		if dstNS == "" {
			dstNS = str("namespace")
		}
		err := store.Move(str("namespace"), str("filename"), dstNS, str("new_filename"))
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"error": "source not found"}, true
		}
		if errors.Is(err, ErrDestinationExists) {
			return map[string]string{"error": "destination already exists"}, true
		}
		if err != nil {
			return map[string]string{"error": err.Error()}, true
		}
		return map[string]bool{"ok": true}, false

	default:
		return map[string]string{"error": fmt.Sprintf("unknown tool: %s", name)}, true
	}
}
