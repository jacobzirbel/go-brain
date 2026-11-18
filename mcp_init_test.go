package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// initialize must carry server instructions naming every tool, so clients that
// defer tool schemas still tell the model gobrain's tools exist.
func TestInitializeInstructionsNameEveryTool(t *testing.T) {
	srv := newServer(config{}, nil)
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	rr := httptest.NewRecorder()
	srv.handleMCP(rr, req)

	var resp struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json %q: %v", rr.Body.String(), err)
	}
	got := resp.Result.Instructions
	if got == "" {
		t.Fatal("initialize returned no instructions")
	}
	for _, tool := range mcpTools {
		if !strings.Contains(got, tool.Name) {
			t.Errorf("instructions do not mention tool %q", tool.Name)
		}
	}
}
