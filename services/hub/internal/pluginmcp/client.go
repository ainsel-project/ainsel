// Package pluginmcp is the hub-side MCP client for plugin MCP endpoints. It
// talks to a plugin's streamable HTTP MCP server over its in-cluster endpoint,
// stamping every request with the AInsel plugin headers, and caches successful
// tools/list results per plugin id + manifest version.
package pluginmcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// toolsCacheTTL is how long a successful tools/list result stays fresh.
const toolsCacheTTL = 30 * time.Second

// protocolVersion is the MCP revision the hub speaks to plugins. It is pinned
// to the latest legacy revision so each operation is a plain
// initialize → request exchange without the stateless-discovery probe.
const protocolVersion = mcp.ProtocolVersion20251125

// Target identifies a plugin MCP endpoint. Endpoint is "ip:port" as verified
// by the hub; Path is the manifest's mcp.path (e.g. "/mcp").
type Target struct {
	PluginID        string
	Endpoint        string
	Path            string
	ManifestVersion string
}

// CallOptions carries the per-call credentials for a plugin tool call.
type CallOptions struct {
	Credential   string
	InvocationID string
}

// cacheEntry is one cached tools/list result.
type cacheEntry struct {
	tools     []mcp.Tool
	fetchedAt time.Time
}

// Client calls plugin MCP endpoints. A fresh MCP session is opened and closed
// per operation. Successful tool lists are cached for toolsCacheTTL, keyed by
// plugin id + manifest version; errors are never cached.
type Client struct {
	httpClient *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry

	// now is the clock used for cache expiry; tests override it.
	now func() time.Time
}

// New returns a Client. A nil httpClient uses the default transport.
func New(httpClient *http.Client) *Client {
	return &Client{
		httpClient: httpClient,
		cache:      make(map[string]cacheEntry),
		now:        time.Now,
	}
}

// ListTools returns the plugin's tools, from cache when fresh. The result is
// cached for toolsCacheTTL keyed by plugin id + manifest version; a failed
// list is not cached.
func (c *Client) ListTools(ctx context.Context, t Target, credential string) ([]mcp.Tool, error) {
	key := t.PluginID + "|" + t.ManifestVersion

	c.mu.Lock()
	entry, ok := c.cache[key]
	c.mu.Unlock()
	if ok && c.now().Sub(entry.fetchedAt) < toolsCacheTTL {
		return entry.tools, nil
	}

	cl, err := c.connect(ctx, t, credential, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = cl.Close() }()

	res, err := cl.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("plugin %s tools/list: %w", t.PluginID, err)
	}

	c.mu.Lock()
	c.cache[key] = cacheEntry{tools: res.Tools, fetchedAt: c.now()}
	c.mu.Unlock()
	return res.Tools, nil
}

// CallTool invokes a tool on the plugin. A tool-level error comes back inside
// the result with IsError set, not as an error.
func (c *Client) CallTool(ctx context.Context, t Target, opts CallOptions, name string, args map[string]any) (*mcp.CallToolResult, error) {
	cl, err := c.connect(ctx, t, opts.Credential, opts.InvocationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cl.Close() }()

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: args}}
	res, err := cl.CallTool(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("plugin %s tools/call %s: %w", t.PluginID, name, err)
	}
	return res, nil
}

// Invalidate drops all cached tool lists for a plugin, regardless of
// manifest version.
func (c *Client) Invalidate(pluginID string) {
	prefix := pluginID + "|"
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.cache {
		if strings.HasPrefix(key, prefix) {
			delete(c.cache, key)
		}
	}
}

// connect opens a fresh MCP session against the plugin endpoint: start the
// transport and perform the initialize handshake. On success the caller owns
// the returned client and must Close it.
func (c *Client) connect(ctx context.Context, t Target, credential, invocationID string) (*mcpclient.Client, error) {
	url := "http://" + t.Endpoint + t.Path

	headers := func(context.Context) map[string]string {
		h := map[string]string{
			"X-Ainsel-Plugin-Id":  t.PluginID,
			"X-Ainsel-Credential": credential,
		}
		if invocationID != "" {
			h["X-Ainsel-Invocation-Id"] = invocationID
		}
		return h
	}

	opts := []transport.StreamableHTTPCOption{transport.WithHTTPHeaderFunc(headers)}
	if c.httpClient != nil {
		opts = append(opts, transport.WithHTTPBasicClient(c.httpClient))
	}

	cl, err := mcpclient.NewStreamableHttpClient(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("plugin %s mcp client: %w", t.PluginID, err)
	}
	if err := cl.Start(ctx); err != nil {
		_ = cl.Close()
		return nil, fmt.Errorf("plugin %s mcp start: %w", t.PluginID, err)
	}
	initReq := mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: protocolVersion,
		ClientInfo:      mcp.Implementation{Name: "ainsel-hub", Version: "1"},
	}}
	if _, err := cl.Initialize(ctx, initReq); err != nil {
		_ = cl.Close()
		return nil, fmt.Errorf("plugin %s mcp initialize: %w", t.PluginID, err)
	}
	return cl, nil
}
