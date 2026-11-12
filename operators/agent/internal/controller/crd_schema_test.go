package controller

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ainsel-project/ainsel/shared/api/llmprovider"
)

// TestAgentImageCRDHasTokenFromEnv ensures the checked-in CRD base matches the
// Go types. This catches the class of bug where a developer updates the Go
// struct (e.g. renaming a JSON tag) but forgets to regenerate the CRD YAML.
//
// The specific failure mode: the live cluster CRD had `token` instead of
// `tokenFromEnv` in the mcpServers schema, causing the API server to silently
// drop the tokenFromEnv field from AgentImage specs. This meant
// MCP_SERVER_TOKENS was never built for forgejo-mcp-server, and all Forgejo
// API calls failed with "token is required".
func TestAgentImageCRDHasTokenFromEnv(t *testing.T) {
	b, err := os.ReadFile("../../config/crd/bases/ainsel.dev_agentimages.yaml")
	if err != nil {
		t.Fatalf("read CRD: %v", err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal CRD: %v", err)
	}

	// Navigate: spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.mcpServers.items.properties
	spec := mustMap(t, doc, "spec")
	versions := mustSlice(t, spec, "versions")
	if len(versions) == 0 {
		t.Fatal("no versions in CRD")
	}
	v0 := mustMap(t, versions[0], "")
	schema := mustMap(t, v0, "schema")
	oapi := mustMap(t, schema, "openAPIV3Schema")
	props := mustMap(t, oapi, "properties")
	specProps := mustMap(t, props, "spec")
	specPropsProps := mustMap(t, specProps, "properties")
	mcpServers := mustMap(t, specPropsProps, "mcpServers")
	items := mustMap(t, mcpServers, "items")
	itemProps := mustMap(t, items, "properties")

	if _, ok := itemProps["tokenFromEnv"]; !ok {
		t.Errorf("AgentImage CRD mcpServers schema missing 'tokenFromEnv' field; found keys: %v", keys(itemProps))
	}
	if _, ok := itemProps["token"]; ok {
		t.Errorf("AgentImage CRD mcpServers schema still has old 'token' field; should be 'tokenFromEnv'")
	}

	// Also verify tokenFromEnv is NOT required (it's optional)
	required := mustSlice(t, items, "required")
	for _, r := range required {
		if s, ok := r.(string); ok && s == "token" {
			t.Errorf("AgentImage CRD mcpServers schema incorrectly requires 'token' field")
		}
	}
}

// TestAgentCRDHasPauseFields guards the pause feature (issue #328): the
// checked-in Agent CRD must carry spec.pause.pausedUntil and status.paused.
// Without them the API server silently prunes the fields, so pausing an
// agent through the hub or kubectl would appear to succeed and do nothing.
func TestAgentCRDHasPauseFields(t *testing.T) {
	b, err := os.ReadFile("../../config/crd/bases/ainsel.dev_agents.yaml")
	if err != nil {
		t.Fatalf("read CRD: %v", err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal CRD: %v", err)
	}

	spec := mustMap(t, doc, "spec")
	versions := mustSlice(t, spec, "versions")
	if len(versions) == 0 {
		t.Fatal("no versions in CRD")
	}
	v0 := mustMap(t, versions[0], "")
	oapi := mustMap(t, mustMap(t, mustMap(t, v0, "schema"), "openAPIV3Schema"), "properties")

	specProps := mustMap(t, mustMap(t, oapi, "spec"), "properties")
	pause := mustMap(t, specProps, "pause")
	pauseProps := mustMap(t, pause, "properties")
	if _, ok := pauseProps["pausedUntil"]; !ok {
		t.Errorf("Agent CRD spec.pause schema missing 'pausedUntil'; found keys: %v", keys(pauseProps))
	}

	statusProps := mustMap(t, mustMap(t, oapi, "status"), "properties")
	if _, ok := statusProps["paused"]; !ok {
		t.Errorf("Agent CRD status schema missing 'paused'; found keys: %v", keys(statusProps))
	}
}

// TestAgentCRDHasModelLicense guards the model-licence record (spec.llm.modelLicense):
// without the schema field the API server silently prunes it, so the
// ModelLicenseRecorded condition would never move to True and an administrator's
// licence review would survive only in git history.
func TestAgentCRDHasModelLicense(t *testing.T) {
	b, err := os.ReadFile("../../config/crd/bases/ainsel.dev_agents.yaml")
	if err != nil {
		t.Fatalf("read CRD: %v", err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal CRD: %v", err)
	}

	spec := mustMap(t, doc, "spec")
	versions := mustSlice(t, spec, "versions")
	if len(versions) == 0 {
		t.Fatal("no versions in CRD")
	}
	v0 := mustMap(t, versions[0], "")
	oapi := mustMap(t, mustMap(t, mustMap(t, v0, "schema"), "openAPIV3Schema"), "properties")

	specProps := mustMap(t, mustMap(t, oapi, "spec"), "properties")
	llm := mustMap(t, specProps, "llm")
	llmProps := mustMap(t, llm, "properties")
	license, ok := llmProps["modelLicense"]
	if !ok {
		t.Errorf("Agent CRD spec.llm schema missing 'modelLicense'; found keys: %v", keys(llmProps))
		return
	}
	licMap, ok := license.(map[string]any)
	if !ok {
		t.Fatalf("modelLicense schema is not a map")
	}
	if typ, ok := licMap["type"].(string); !ok || typ != "string" {
		t.Errorf("modelLicense schema must be type string, got %v", licMap["type"])
	}
	if req, ok := licMap["required"]; ok {
		t.Errorf("modelLicense must remain optional, but schema carries: %v", req)
	}
}

// TestAgentCRDProviderEnumAndBaseURLs guards issue #96: the checked-in Agent
// CRD must constrain spec.llm.provider to exactly the registry's providers
// (shared/api/llmprovider), in registry order, with no
// default (a default would just move the silent-Ollama-Cloud fallback into
// the schema), and must carry the per-provider baseUrl override fields.
// Without the enum the API server accepts any provider string and only the
// operator's InvalidLLMProvider condition reports the mistake; without
// baseUrl the fields are silently pruned from stored objects.
func TestAgentCRDProviderEnumAndBaseURLs(t *testing.T) {
	b, err := os.ReadFile("../../config/crd/bases/ainsel.dev_agents.yaml")
	if err != nil {
		t.Fatalf("read CRD: %v", err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal CRD: %v", err)
	}

	spec := mustMap(t, doc, "spec")
	versions := mustSlice(t, spec, "versions")
	if len(versions) == 0 {
		t.Fatal("no versions in CRD")
	}
	v0 := mustMap(t, versions[0], "")
	schema := mustMap(t, v0, "schema")
	oapi := mustMap(t, schema, "openAPIV3Schema")
	specProps := mustMap(t, mustMap(t, oapi, "properties"), "spec")
	llmProps := mustMap(t, mustMap(t, specProps, "properties"), "llm")
	provider := mustMap(t, llmProps, "properties")
	provider = mustMap(t, provider, "provider")

	enum := mustSlice(t, provider, "enum")
	want := llmprovider.IDs()
	if len(enum) != len(want) {
		t.Fatalf("spec.llm.provider enum = %v, want %v", enum, want)
	}
	for i, w := range want {
		if enum[i] != w {
			t.Fatalf("spec.llm.provider enum = %v, want %v", enum, want)
		}
	}
	if _, ok := provider["default"]; ok {
		t.Error("spec.llm.provider must have no schema default (issue #96 removes the silent fallback)")
	}

	// baseUrl overrides on every built-in provider block.
	for _, block := range []string{"ollamaCloud", "openCode", "alibabaCloud", "openAI", "anthropic", "google"} {
		props := mustMap(t, mustMap(t, specProps, "properties"), block)
		itemProps := mustMap(t, props, "properties")
		if _, ok := itemProps["baseUrl"]; !ok {
			t.Errorf("CRD %s schema missing 'baseUrl'; found keys: %v", block, keys(itemProps))
		}
	}
}

func mustMap(t *testing.T, parent any, key string) map[string]any {
	t.Helper()
	var m map[string]any
	if key == "" {
		// parent is already the map
		m, _ = parent.(map[string]any)
	} else {
		p, _ := parent.(map[string]any)
		if p == nil {
			t.Fatalf("expected map for parent, got %T", parent)
		}
		v, ok := p[key]
		if !ok {
			t.Fatalf("key %q not found in map", key)
		}
		m, _ = v.(map[string]any)
	}
	if m == nil {
		t.Fatalf("expected map[string]any for key %q, got %T", key, parent)
	}
	return m
}

func mustSlice(t *testing.T, parent any, key string) []any {
	t.Helper()
	p, _ := parent.(map[string]any)
	if p == nil {
		t.Fatalf("expected map for parent, got %T", parent)
	}
	v, ok := p[key]
	if !ok {
		t.Fatalf("key %q not found in map", key)
	}
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("expected []any for key %q, got %T", key, v)
	}
	return s
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
