// Package hello is the fixture plugin: a complete, self-contained AInsel plugin
// that the hub-level end-to-end test and the console's plugin-host test run
// against, and that plugin authors can copy.
//
// It is a real plugin rather than a mock — the manifest, the static UI module,
// the skill document, the visibility lookup and the MCP endpoint are each the
// thing a third-party plugin has to implement — but it keeps no state: every
// answer is derived from the request. That keeps the fixture's assertions exact:
// a caller that gets "remembered for inv-7" saw the invocation id travel through
// the hub's MCP proxy, not a value the fixture happened to have stored.
//
// New(hubURL) returns the handler; the hub reaches it over its in-cluster
// Service, and the tests reach it behind httptest.
package hello

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// staticFS holds every file the plugin serves under /static/, plus the manifest.
// The manifest paths in static/ainsel-plugin.json are resolved against this tree,
// so a path in the manifest and a served file cannot drift apart silently.
//
//go:embed static
var staticFS embed.FS

// manifestBytes is the same file as static/ainsel-plugin.json, embedded on its
// own so Manifest() does not have to read through the FS.
//
//go:embed static/ainsel-plugin.json
var manifestBytes []byte

const (
	// pluginID and pluginVersion match the manifest's id and version; they are
	// also the MCP server identity the hub sees on initialize.
	pluginID      = "hello"
	pluginVersion = "1.0.0"

	// credentialHeader carries the scoped credential the hub minted for the
	// caller. The plugin has one of its own and must not use it for a lookup
	// that answers "what may this caller see".
	credentialHeader = "X-Ainsel-Credential" // #nosec G101 -- the header name, not a credential value

	// invocationHeader names the agent invocation an MCP call belongs to.
	invocationHeader = "X-Ainsel-Invocation-Id"

	// hubVisiblePath is the hub endpoint that judges what the caller may read.
	hubVisiblePath = "/api/v1/plugin-context/visible"

	// hubTimeout bounds a callback into the hub: a plugin request is inside the
	// caller's request, so a hung hub must not pile up goroutines.
	hubTimeout = 5 * time.Second

	// maxHubBody caps how much of the hub's answer is read.
	maxHubBody = 1 << 20
)

// hubClient is the client used for callbacks into the hub.
var hubClient = &http.Client{Timeout: hubTimeout}

// invocationKey is the context key carrying the invocation id from the HTTP
// request into an MCP tool handler, where only a context is available.
type invocationKey struct{}

// Manifest returns the plugin manifest, byte for byte as embedded. The hub
// fetches it from GET /ainsel-plugin.json; tests and the console's fixture test
// read it through here. The returned slice is a copy, so a caller cannot
// corrupt the embedded document.
func Manifest() []byte {
	return bytes.Clone(manifestBytes)
}

// New returns the plugin's HTTP handler: the manifest at its well-known path,
// the static tree, the visibility lookup under the manifest's api.basePath, and
// the MCP endpoint at mcp.path. hubURL is the address of the AInsel hub the
// plugin calls back into (e.g. "http://hub.namespace.svc:8080").
func New(hubURL string) http.Handler {
	hub := strings.TrimSuffix(hubURL, "/")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ainsel-plugin.json", handleManifest)
	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("GET /api/visible-agents", handleVisibleAgents(hub))
	// The MCP endpoint takes POST (requests), GET (an SSE stream) and DELETE
	// (session teardown), so it is mounted without a method pattern.
	mux.Handle("/mcp", mcpHandler())
	return mux
}

// handleManifest serves the manifest with the exact bytes that were embedded:
// what the reconciler installs is what the plugin was built with.
func handleManifest(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(Manifest())
}

// contentTypes pins the Content-Type of the static files whose meaning depends
// on it. The console imports index.js as an ES module — a text/plain response
// makes the browser refuse it — and the hub stores the skill body as markdown.
// Anything not listed is left to net/http's own detection.
var contentTypes = map[string]string{
	".js":   "text/javascript; charset=utf-8",
	".mjs":  "text/javascript; charset=utf-8",
	".json": "application/json",
	".css":  "text/css; charset=utf-8",
	".md":   "text/markdown; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".svg":  "image/svg+xml",
	".map":  "application/json",
	".wasm": "application/wasm",
}

// staticHandler serves /static/* from the embedded tree, refusing the bare
// directory: there is no index document at that path and a listing of an
// embedded filesystem would only leak the plugin's layout.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Unreachable: the path is embedded, so fs.Sub cannot fail here.
		panic("hello: embedded static tree is missing: " + err.Error())
	}
	files := http.StripPrefix("/static/", http.FileServerFS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := contentTypes[strings.ToLower(path.Ext(r.URL.Path))]; ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		if strings.TrimPrefix(r.URL.Path, "/static") == "/" {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// visibleRequest is the body of the hub's visibility check. Field order is the
// wire format the hub's tests see, so it is part of the fixture's contract.
type visibleRequest struct {
	ResourceType string   `json:"resourceType"`
	Names        []string `json:"names"`
}

// visibleAnswer is the hub's reply, relayed to the caller unchanged.
type visibleAnswer struct {
	Visible []string `json:"visible"`
}

// handleVisibleAgents answers "which of these agents may the caller read?" by
// asking the hub — as the caller, with the credential the hub attached to this
// request. The fixture has no group model of its own and must not invent one:
// answering from its own identity would show every agent to every caller.
func handleVisibleAgents(hubURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		credential := r.Header.Get(credentialHeader)
		if credential == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing credential")
			return
		}

		names := splitNames(r.URL.Query().Get("names"))
		if len(names) == 0 {
			writeJSONError(w, http.StatusBadRequest, "names is required")
			return
		}

		body, err := json.Marshal(visibleRequest{ResourceType: "agent", Names: names})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "cannot encode request")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), hubTimeout)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, hubURL+hubVisiblePath, bytes.NewReader(body))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "cannot reach the hub")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+credential)

		res, err := hubClient.Do(req)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "hub visibility check failed")
			return
		}
		defer func() { _ = res.Body.Close() }()

		if res.StatusCode != http.StatusOK {
			writeJSONError(w, http.StatusBadGateway, "hub visibility check failed")
			return
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, maxHubBody))
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "hub visibility check failed")
			return
		}
		var answer visibleAnswer
		if err := json.Unmarshal(raw, &answer); err != nil {
			writeJSONError(w, http.StatusBadGateway, "hub visibility check failed")
			return
		}
		if answer.Visible == nil {
			answer.Visible = []string{}
		}
		writeJSON(w, http.StatusOK, answer)
	}
}

// splitNames parses the comma-separated names query parameter: empty entries are
// dropped and duplicates collapsed, so "a,,a,b" asks about two agents.
func splitNames(raw string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// mcpHandler builds the plugin's MCP endpoint: a streamable HTTP server with the
// three tools the manifest declares. It is stateless on purpose — every request
// is its own session, so nothing has to be shared between replicas of a plugin.
func mcpHandler() http.Handler {
	srv := server.NewMCPServer(pluginID, pluginVersion, server.WithToolCapabilities(false))

	srv.AddTool(
		mcp.NewTool("greet",
			mcp.WithDescription("Greet someone by name"),
			mcp.WithString("name", mcp.Required(), mcp.Description("Name to greet")),
		),
		handleGreet,
	)
	srv.AddTool(
		mcp.NewTool("remember",
			mcp.WithDescription("Remember something for the current invocation"),
			// Optional: a caller that just wants to confirm the invocation id
			// reached the plugin may call it with no arguments.
			mcp.WithString("text", mcp.Description("What to remember")),
		),
		handleRemember,
	)
	srv.AddTool(
		mcp.NewTool("reset",
			mcp.WithDescription("Forget everything this plugin remembered"),
		),
		handleReset,
	)

	return server.NewStreamableHTTPServer(srv,
		server.WithStateLess(true),
		// The invocation id arrives as a header, but a tool handler only gets a
		// context: copy it across here so remember can echo it back.
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			id := r.Header.Get(invocationHeader)
			if id == "" {
				return ctx
			}
			return context.WithValue(ctx, invocationKey{}, id)
		}),
	)
}

// handleGreet answers with the greeting the end-to-end test asserts verbatim.
func handleGreet(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	if name == "" {
		return mcp.NewToolResultError("greet: name is required"), nil
	}
	return mcp.NewToolResultText("hello, " + name), nil
}

// handleRemember proves the invocation id travelled from the agent runtime
// through the hub's MCP proxy into the plugin. The fixture stores nothing; a real
// plugin would keep text against this invocation.
func handleRemember(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, ok := invocationFrom(ctx)
	if !ok {
		return mcp.NewToolResultError("remember: missing " + invocationHeader), nil
	}
	return mcp.NewToolResultText("remembered for " + id), nil
}

// handleReset is the operator-audience tool: it exists so the hub's audience
// filter has something to hide from an agent.
func handleReset(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText("reset"), nil
}

// invocationFrom reads the invocation id copied into the context by the streamable
// server's context function.
func invocationFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(invocationKey{}).(string)
	return id, ok && id != ""
}

// writeJSON writes v with no trailing newline, so the body is exactly what a test
// asserts.
func writeJSON(w http.ResponseWriter, status int, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// writeJSONError answers with {"error": msg}, the same shape the hub uses.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
