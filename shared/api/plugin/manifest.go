// Package plugin parses and validates the AInsel plugin manifest — the
// well-known document (`GET /ainsel-plugin.json`) a plugin image serves to
// describe itself: identity, the plugin API version it targets, its UI bundle,
// its backend base path, its MCP endpoint, and the AInsel permissions it asks
// for.
//
// The reconciler that installs a Plugin resource and the hub that serves the
// console both need the exact same reading of this document, so the schema,
// the permission/slot vocabulary and the compatibility rule live here instead
// of being duplicated in each consumer.
//
// Only the standard library is used: this package is imported by the hub, by
// operators, and by anything else that must judge a manifest without pulling
// in Kubernetes dependencies.
package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// HostAPIVersion is the plugin API version this build of AInsel implements.
// It is the host side of Compatible: a manifest targeting a newer minor than
// this is incompatible.
const HostAPIVersion = "1.0"

// ManifestPath is the path a plugin serves its manifest at, relative to the
// root of its in-cluster Service.
const ManifestPath = "/ainsel-plugin.json"

// MaxManifestBytes caps how much of a manifest body a fetcher will accept.
// Plugins are untrusted code, so the cap is enforced before parsing.
const MaxManifestBytes = 256 << 10

// Permissions is the closed v1 vocabulary of AInsel permissions a manifest may
// declare. Anything else is rejected, so a plugin cannot silently widen what it
// asks for by inventing a name. Entries are grouped by resource, read before
// write within each resource.
var Permissions = []string{
	"agents:read",
	"agents:write",
	"agent-images:read",
	"agent-images:write",
	"connectors:read",
	"connectors:write",
	"invocations:read",
	"mcp-servers:read",
	"mcp-servers:write",
	"observability:read",
	"personas:read",
	"personas:write",
	"skills:read",
	"skills:write",
	"triggers:read",
	"triggers:write",
}

// Slots is the closed v1 vocabulary of console extension points a manifest may
// fill. Slot keys outside this set are rejected.
var Slots = []string{
	"observability.section",
	"invocation.panel",
	"agent.panel",
	"persona.panel",
}

// idPattern is the plugin id shape: a lowercase slug that is safe to use as a
// URL segment and as a tool-name prefix. It must end alphanumeric — the id is
// also the Plugin object's name, and a Kubernetes name may not end in '-'
// (the hub's install validation uses the same pattern).
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}[a-z0-9]$`)

// Manifest is a parsed plugin manifest. Only identity fields and pluginApi are
// required; the ui, api and mcp specs are each optional and nil when absent.
type Manifest struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	PluginAPI   string   `json:"pluginApi"`
	UI          *UISpec  `json:"ui,omitempty"`
	API         *APISpec `json:"api,omitempty"`
	MCP         *MCPSpec `json:"mcp,omitempty"`

	// Permissions are the AInsel permissions the plugin needs. Every entry
	// must be in Permissions; the admin grants them on install.
	Permissions []string `json:"permissions,omitempty"`
}

// UISpec describes the console extension a plugin contributes: the ES module
// to load, the pages it owns, and the slots it fills.
type UISpec struct {
	// Entry is the plugin-relative path of its UI bundle, e.g.
	// "/plugins/mem0/index.js".
	Entry string `json:"entry"`

	// Pages are navigation entries rendered under the plugin's namespace.
	Pages []Page `json:"pages,omitempty"`

	// Slots maps a slot id (see Slots) to the exported component name that
	// fills it.
	Slots map[string]string `json:"slots,omitempty"`
}

// Page is one navigation entry contributed by a plugin.
type Page struct {
	// Path is the route path, relative to the plugin's namespace.
	Path string `json:"path"`

	// Label is the navigation text shown in the console sidebar.
	Label string `json:"label"`

	// Section groups the page under a navigation heading.
	Section string `json:"section,omitempty"`

	// Icon names an icon from the console's icon set.
	Icon string `json:"icon,omitempty"`
}

// APISpec marks that a plugin exposes a backend API, reachable only through
// the hub proxy.
type APISpec struct {
	// BasePath is the plugin-relative prefix the API is served under.
	BasePath string `json:"basePath"`
}

// MCPSpec marks that a plugin exposes an MCP endpoint over streamable HTTP.
type MCPSpec struct {
	// Path is the plugin-relative path of the MCP endpoint.
	Path string `json:"path"`
}

// Parse decodes a manifest body and validates it. It rejects bodies larger
// than MaxManifestBytes, input that is not JSON, and unknown fields — a
// manifest must not carry keys this package does not understand, because they
// would be silently dropped. The decoded manifest is returned only when it is
// valid.
func Parse(data []byte) (*Manifest, error) {
	if len(data) > MaxManifestBytes {
		return nil, fmt.Errorf("plugin: manifest too large: %d bytes, maximum is %d bytes",
			len(data), MaxManifestBytes)
	}

	// Label malformed input as invalid JSON up front, with the decoder's
	// positional detail. Doing it here keeps the strict decode below free to
	// report schema problems ("unknown field") with their own wording.
	if !json.Valid(data) {
		var discard any
		if err := json.Unmarshal(data, &discard); err != nil {
			return nil, fmt.Errorf("plugin: invalid JSON manifest: %w", err)
		}
		return nil, errors.New("plugin: invalid JSON manifest")
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("plugin: cannot decode manifest: %w", err)
	}

	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks a manifest against the platform rules and returns every
// problem it found, joined into one error (see errors.Join) so a plugin author
// sees all of them at once.
func (m *Manifest) Validate() error {
	var errs []error

	if !idPattern.MatchString(m.ID) {
		errs = append(errs, fmt.Errorf("plugin: invalid id %q: must match %s", m.ID, idPattern))
	}
	if strings.TrimSpace(m.DisplayName) == "" {
		errs = append(errs, errors.New("plugin: displayName is required"))
	}
	if strings.TrimSpace(m.Version) == "" {
		errs = append(errs, errors.New("plugin: version is required"))
	}
	if _, _, verr := parseAPIVersion(m.PluginAPI); verr != nil {
		errs = append(errs, fmt.Errorf("plugin: invalid pluginApi %q: %w", m.PluginAPI, verr))
	}

	for _, p := range m.Permissions {
		if !slices.Contains(Permissions, p) {
			errs = append(errs, fmt.Errorf("plugin: unknown permission %q: not in the v1 permission vocabulary", p))
		}
	}

	if m.UI != nil {
		errs = append(errs, validatePath("ui.entry", m.UI.Entry))

		seen := make(map[string]bool, len(m.UI.Pages))
		for i, page := range m.UI.Pages {
			errs = append(errs, validatePath(fmt.Sprintf("ui.pages[%d].path", i), page.Path))
			if page.Path == "" {
				continue
			}
			if seen[page.Path] {
				errs = append(errs, fmt.Errorf("plugin: duplicate page path %q: page paths must be unique", page.Path))
			}
			seen[page.Path] = true
		}

		// Slots is a map, so iterate its keys in sorted order: Validate's
		// error is stored verbatim on the Plugin status by the reconciler,
		// and a message whose parts flap between reconciles because Go
		// randomised map order would look like the plugin kept changing.
		for _, slot := range sortedKeys(m.UI.Slots) {
			if !slices.Contains(Slots, slot) {
				errs = append(errs, fmt.Errorf("plugin: unknown slot %q: not in the v1 slot vocabulary", slot))
			}
		}
	}

	if m.API != nil {
		errs = append(errs, validatePath("api.basePath", m.API.BasePath))
	}
	if m.MCP != nil {
		errs = append(errs, validatePath("mcp.path", m.MCP.Path))
	}

	return errors.Join(errs...)
}

// Compatible reports whether a manifest targeting pluginAPI can run against a
// host implementing the host version: the majors must be equal and the
// manifest's minor must not exceed the host's minor. Both versions must be
// "MAJOR.MINOR".
func Compatible(pluginAPI, host string) (bool, error) {
	pluginMajor, pluginMinor, err := parseAPIVersion(pluginAPI)
	if err != nil {
		return false, fmt.Errorf("plugin: invalid pluginApi %q: %w", pluginAPI, err)
	}
	hostMajor, hostMinor, err := parseAPIVersion(host)
	if err != nil {
		return false, fmt.Errorf("plugin: invalid host plugin api version %q: %w", host, err)
	}
	return pluginMajor == hostMajor && pluginMinor <= hostMinor, nil
}

// MissingApprovals returns the entries of declared that are not in approved —
// that is, what still needs an admin's approval. Duplicates are collapsed and
// the result is sorted, so it is stable enough to store or compare.
func MissingApprovals(declared, approved []string) []string {
	granted := make(map[string]struct{}, len(approved))
	for _, a := range approved {
		granted[a] = struct{}{}
	}

	var missing []string
	seen := make(map[string]struct{}, len(declared))
	for _, d := range declared {
		if _, ok := granted[d]; ok {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		missing = append(missing, d)
	}
	sort.Strings(missing)
	return missing
}

// sortedKeys returns the keys of m in ascending order, for deterministic
// iteration over a map.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// parseAPIVersion splits a "MAJOR.MINOR" version into its parts. Both parts
// must be plain non-negative decimal numbers — no patch component, no
// prerelease suffix, no sign.
func parseAPIVersion(v string) (major, minor int, err error) {
	parts := strings.Split(v, ".")
	if len(parts) != 2 {
		return 0, 0, errors.New(`must be "MAJOR.MINOR"`)
	}
	if major, err = parseVersionPart(parts[0]); err != nil {
		return 0, 0, fmt.Errorf("major %q: %w", parts[0], err)
	}
	if minor, err = parseVersionPart(parts[1]); err != nil {
		return 0, 0, fmt.Errorf("minor %q: %w", parts[1], err)
	}
	return major, minor, nil
}

func parseVersionPart(s string) (int, error) {
	if s == "" {
		return 0, errors.New("is empty")
	}
	if strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0, errors.New("must be a non-negative integer")
	}
	// Guaranteed to fit an int by the digit check above unless the number is
	// absurdly long, which Atoi reports as a range error.
	return strconv.Atoi(s)
}

// validatePath checks one path-ish manifest field. Paths are plugin-relative
// and are later joined onto a mount prefix or used as a route, so they must be
// absolute-within-the-plugin and must not escape via "..". A nil spec is not
// passed here at all: an empty path is only rejected once its parent spec is
// present.
func validatePath(field, path string) error {
	if path == "" {
		return fmt.Errorf("plugin: %s is required", field)
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("plugin: invalid %s %q: must start with %q", field, path, "/")
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return fmt.Errorf("plugin: invalid %s %q: must not contain %q", field, path, "..")
		}
	}
	return nil
}
