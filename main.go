package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ── Config ────────────────────────────────────────────────────────────────────

type config struct {
	secretKey        string
	baseURL          string
	port             string
	frontendPassword string
	dbPath           string
}

func loadConfig() config {
	cfg := config{
		secretKey:        os.Getenv("SECRET_KEY"),
		baseURL:          os.Getenv("BASE_URL"),
		frontendPassword: os.Getenv("FRONTEND_PASSWORD"),
		port:             os.Getenv("PORT"),
		dbPath:           os.Getenv("DB_PATH"),
	}
	if cfg.port == "" {
		cfg.port = "3049"
	}
	if cfg.dbPath == "" {
		cfg.dbPath = "/opt/go-brain/brain.db"
	}
	return cfg
}

// ── Server ────────────────────────────────────────────────────────────────────

type server struct {
	cfg    config
	store  *SQLiteStore
	tokens tokenStore
}

func newServer(cfg config, store *SQLiteStore) *server {
	s := &server{
		cfg:    cfg,
		store:  store,
		tokens: tokenStore{tokens: map[string]struct{}{}},
	}
	if cfg.secretKey != "" {
		s.tokens.add(cfg.secretKey)
	}
	return s
}

// ── Token store ───────────────────────────────────────────────────────────────

type tokenStore struct {
	mu     sync.RWMutex
	tokens map[string]struct{}
}

func (t *tokenStore) add(tok string) {
	t.mu.Lock()
	t.tokens[tok] = struct{}{}
	t.mu.Unlock()
}

func (t *tokenStore) has(tok string) bool {
	t.mu.RLock()
	_, ok := t.tokens[tok]
	t.mu.RUnlock()
	return ok
}

func (t *tokenStore) delete(tok string) {
	t.mu.Lock()
	delete(t.tokens, tok)
	t.mu.Unlock()
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure means the process can't mint tokens at all
	}
	return hex.EncodeToString(b)
}

// ── Middleware ────────────────────────────────────────────────────────────────

func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token := strings.TrimPrefix(header, "Bearer ")
		token = strings.TrimSpace(token)
		if token == "" || !s.tokens.has(token) {
			writeJSON(w, http.StatusUnauthorized, errResult{Error: "Unauthorized"})
			return
		}
		next(w, r)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d", r.Method, r.URL.Path, rec.status)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}

// ── OAuth endpoints ───────────────────────────────────────────────────────────

type oauthMetadata struct {
	Issuer                        string   `json:"issuer"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	ResponseTypesSupported        []string `json:"response_types_supported"`
	GrantTypesSupported           []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

func (s *server) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, oauthMetadata{
		Issuer:                        s.cfg.baseURL,
		RegistrationEndpoint:          s.cfg.baseURL + "/register",
		AuthorizationEndpoint:         s.cfg.baseURL + "/oauth/authorize",
		TokenEndpoint:                 s.cfg.baseURL + "/oauth/token",
		ResponseTypesSupported:        []string{"code"},
		GrantTypesSupported:           []string{"authorization_code"},
		CodeChallengeMethodsSupported: []string{"S256"},
	})
}

type registrationResponse struct {
	ClientID      string   `json:"client_id"`
	ClientSecret  string   `json:"client_secret"`
	RedirectURIs  []string `json:"redirect_uris"`
	ClientName    string   `json:"client_name"`
	GrantTypes    []string `json:"grant_types"`
	ResponseTypes []string `json:"response_types"`
}

func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	// An empty body is a valid registration; anything else malformed is not.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errResult{Error: "invalid request body"})
		return
	}
	if body.ClientName == "" {
		body.ClientName = "claude"
	}
	writeJSON(w, http.StatusCreated, registrationResponse{
		ClientID:      randHex(16),
		ClientSecret:  "not-used",
		RedirectURIs:  body.RedirectURIs,
		ClientName:    body.ClientName,
		GrantTypes:    []string{"authorization_code"},
		ResponseTypes: []string{"code"},
	})
}

func (s *server) handleAuthorizeGet(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleAuthorizePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	secret := r.FormValue("secret")
	redirectURI := r.FormValue("redirect_uri")
	state := r.FormValue("state")
	codeChallenge := r.FormValue("code_challenge")
	codeChallengeMethod := r.FormValue("code_challenge_method")

	if secret == "" || secret != s.cfg.secretKey {
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
	s.tokens.add("code:" + code)

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

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

func (s *server) handleToken(w http.ResponseWriter, r *http.Request) {
	// ParseForm only consumes the body for form content-types, so the JSON
	// fallback below still sees an unread body.
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, errResult{Error: "invalid_request"})
		return
	}
	grantType := r.FormValue("grant_type")
	code := r.FormValue("code")

	// Also accept JSON body
	if grantType == "" {
		var body struct {
			GrantType string `json:"grant_type"`
			Code      string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errResult{Error: "invalid_request"})
			return
		}
		grantType = body.GrantType
		code = body.Code
	}

	if grantType != "authorization_code" {
		writeJSON(w, http.StatusBadRequest, errResult{Error: "unsupported_grant_type"})
		return
	}
	codeKey := "code:" + code
	if !s.tokens.has(codeKey) {
		writeJSON(w, http.StatusBadRequest, errResult{Error: "invalid_grant"})
		return
	}
	s.tokens.delete(codeKey)
	accessToken := randHex(32)
	s.tokens.add(accessToken)
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   86400 * 365,
	})
}

// ── MCP endpoint ──────────────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type mcpTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content []mcpTextContent `json:"content"`
	IsError bool             `json:"isError"`
}

type mcpInitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	ServerInfo      mcpServerInfo   `json:"serverInfo"`
	Capabilities    mcpCapabilities `json:"capabilities"`
}

type mcpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type mcpCapabilities struct {
	Tools struct{} `json:"tools"`
}

func (s *server) handleMCP(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "parse error"},
		})
		return
	}

	respond := func(result any) {
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
	}
	respondErr := func(code int, message string) {
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: message}})
	}

	switch req.Method {
	case "initialize":
		respond(mcpInitializeResult{
			ProtocolVersion: "2024-11-05",
			ServerInfo:      mcpServerInfo{Name: "go-brain", Version: "0.1.0"},
		})
	case "tools/list":
		respond(struct {
			Tools []toolDef `json:"tools"`
		}{Tools: mcpTools})
	case "tools/call":
		name, _ := req.Params["name"].(string)
		args, _ := req.Params["arguments"].(map[string]any)
		result, isError := runTool(s.store, name, args)
		raw, err := json.Marshal(result)
		if err != nil {
			respondErr(-32603, "internal error: "+err.Error())
			return
		}
		respond(mcpToolResult{
			Content: []mcpTextContent{{Type: "text", Text: string(raw)}},
			IsError: isError,
		})
	case "ping":
		respond(struct{}{})
	default:
		respondErr(-32601, "method not found: "+req.Method)
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	cfg := loadConfig()
	store, err := NewSQLiteStore(cfg.dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	log.Printf("database: %s", cfg.dbPath)
	srv := newServer(cfg, store)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", srv.handleMeta)
	mux.HandleFunc("POST /register", srv.handleRegister)
	mux.HandleFunc("GET /oauth/authorize", srv.handleAuthorizeGet)
	mux.HandleFunc("POST /oauth/authorize", srv.handleAuthorizePost)
	mux.HandleFunc("POST /oauth/token", srv.handleToken)
	mux.HandleFunc("POST /mcp", srv.auth(srv.handleMCP))
	mux.HandleFunc("POST /", srv.auth(srv.handleMCP))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "server": "go-brain"})
	})

	srv.registerUIRoutes(mux)

	httpServer := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           logMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	log.Printf("go-brain listening on %s", httpServer.Addr)
	log.Printf("OAuth endpoint: %s/oauth/authorize", cfg.baseURL)
	log.Fatal(httpServer.ListenAndServe())
}
