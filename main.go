package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// ── Config ────────────────────────────────────────────────────────────────────

var (
	secretKey string
	baseURL   string
	port      string
)

func init() {
	secretKey = os.Getenv("SECRET_KEY")
	baseURL = os.Getenv("BASE_URL")
	port = os.Getenv("PORT")
	if port == "" {
		port = "3049"
	}
}

// ── Token store ───────────────────────────────────────────────────────────────

var (
	tokensMu    sync.RWMutex
	validTokens = map[string]struct{}{}
)

func addToken(t string) {
	tokensMu.Lock()
	validTokens[t] = struct{}{}
	tokensMu.Unlock()
}

func hasToken(t string) bool {
	tokensMu.RLock()
	_, ok := validTokens[t]
	tokensMu.RUnlock()
	return ok
}

func deleteToken(t string) {
	tokensMu.Lock()
	delete(validTokens, t)
	tokensMu.Unlock()
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ── Middleware ────────────────────────────────────────────────────────────────

func auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token := strings.TrimPrefix(header, "Bearer ")
		token = strings.TrimSpace(token)
		if token == "" || !hasToken(token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			return
		}
		next(w, r)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ── OAuth endpoints ───────────────────────────────────────────────────────────

func handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                            baseURL,
		"registration_endpoint":             baseURL + "/register",
		"authorization_endpoint":            baseURL + "/oauth/authorize",
		"token_endpoint":                    baseURL + "/oauth/token",
		"response_types_supported":          []string{"code"},
		"grant_types_supported":             []string{"authorization_code"},
		"code_challenge_methods_supported":  []string{"S256"},
	})
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.ClientName == "" {
		body.ClientName = "claude"
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":     randHex(16),
		"client_secret": "not-used",
		"redirect_uris": body.RedirectURIs,
		"client_name":   body.ClientName,
		"grant_types":   []string{"authorization_code"},
		"response_types": []string{"code"},
	})
}

func handleAuthorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
  <title>go-brain — Authorize</title>
  <style>
    body { font-family: sans-serif; max-width: 400px; margin: 80px auto; padding: 20px; }
    input { width: 100%%; padding: 8px; margin: 8px 0; box-sizing: border-box; font-size: 16px; }
    button { width: 100%%; padding: 10px; background: #2563eb; color: white; border: none; border-radius: 4px; font-size: 16px; cursor: pointer; }
    button:hover { background: #1d4ed8; }
  </style>
</head>
<body>
  <h2>go-brain</h2>
  <p>jacobzirbel.com</p>
  <form method="POST" action="/oauth/authorize">
    <input type="hidden" name="redirect_uri" value=%q />
    <input type="hidden" name="state" value=%q />
    <input type="hidden" name="code_challenge" value=%q />
    <input type="hidden" name="code_challenge_method" value=%q />
    <input type="password" name="secret" placeholder="Enter your secret key" autofocus />
    <button type="submit">Authorize</button>
  </form>
</body>
</html>`, redirectURI, state, codeChallenge, codeChallengeMethod)
}

func handleAuthorizePost(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	secret := r.FormValue("secret")
	redirectURI := r.FormValue("redirect_uri")
	state := r.FormValue("state")
	codeChallenge := r.FormValue("code_challenge")
	codeChallengeMethod := r.FormValue("code_challenge_method")

	if secret == "" || secret != secretKey {
		params := url.Values{
			"redirect_uri":          {redirectURI},
			"state":                 {state},
			"code_challenge":        {codeChallenge},
			"code_challenge_method": {codeChallengeMethod},
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `<!DOCTYPE html><html><body style="font-family:sans-serif;max-width:400px;margin:80px auto;padding:20px">
<h2>go-brain</h2>
<p style="color:red">Invalid secret key. Try again.</p>
<a href="/oauth/authorize?%s">← Back</a>
</body></html>`, params.Encode())
		return
	}

	code := randHex(32)
	addToken("code:" + code)

	redirectURL, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := redirectURL.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	redirectURL.RawQuery = q.Encode()
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

func handleToken(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	grantType := r.FormValue("grant_type")
	code := r.FormValue("code")

	// Also accept JSON body
	if grantType == "" {
		var body struct {
			GrantType string `json:"grant_type"`
			Code      string `json:"code"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		grantType = body.GrantType
		code = body.Code
	}

	if grantType != "authorization_code" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	codeKey := "code:" + code
	if !hasToken(codeKey) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	deleteToken(codeKey)
	accessToken := randHex(32)
	addToken(accessToken)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   86400 * 365,
	})
}

// ── MCP endpoint ──────────────────────────────────────────────────────────────

var mcpTools = []map[string]any{
	{
		"name":        "ping",
		"description": "Check connectivity to go-brain",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
}

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

func handleMCP(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"jsonrpc": "2.0",
			"id":      nil,
			"error":   map[string]any{"code": -32700, "message": "parse error"},
		})
		return
	}

	respond := func(result any) {
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	rpcError := func(code int, message string) {
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": code, "message": message}})
	}

	switch req.Method {
	case "initialize":
		respond(map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": "go-brain", "version": "0.1.0"},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})
	case "tools/list":
		respond(map[string]any{"tools": mcpTools})
	case "tools/call":
		name, _ := req.Params["name"].(string)
		if name != "ping" {
			respond(map[string]any{
				"content": []map[string]any{{"type": "text", "text": fmt.Sprintf(`{"error":"unknown tool: %s"}`, name)}},
				"isError": true,
			})
			return
		}
		result, _ := json.Marshal(map[string]bool{"pong": true})
		respond(map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(result)}},
			"isError": false,
		})
	case "ping":
		respond(map[string]any{})
	default:
		rpcError(-32601, "method not found: "+req.Method)
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	if secretKey != "" {
		addToken(secretKey)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", handleMeta)
	mux.HandleFunc("POST /register", handleRegister)
	mux.HandleFunc("GET /oauth/authorize", handleAuthorizeGet)
	mux.HandleFunc("POST /oauth/authorize", handleAuthorizePost)
	mux.HandleFunc("POST /oauth/token", handleToken)
	mux.HandleFunc("POST /mcp", auth(handleMCP))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "server": "go-brain"})
	})

	addr := ":" + port
	log.Printf("go-brain listening on %s", addr)
	log.Printf("OAuth endpoint: %s/oauth/authorize", baseURL)
	log.Fatal(http.ListenAndServe(addr, mux))
}
