package controller

import (
	"context"
	"testing"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// egressProxyTestAgent returns a minimal agent that passes resolvePiProvider
// (the provider must be set explicitly — see the issue #96 comment on
// resolvePiProvider).
func egressProxyTestAgent() *ainselv1alpha1.Agent {
	return &ainselv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "proxy-agent", Namespace: "default"},
		Spec: ainselv1alpha1.AgentSpec{
			LLM: ainselv1alpha1.AgentLLM{
				Model:    "llama3",
				Provider: ainselv1alpha1.AgentLLMProviderOllamaCloud,
			},
		},
	}
}

func egressProxyTestImage(imageEnv []ainselv1alpha1.AgentImageEnvVar) *ainselv1alpha1.AgentImage {
	return &ainselv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: "proxy-image", Namespace: "default"},
		Spec: ainselv1alpha1.AgentImageSpec{
			Env: imageEnv,
		},
	}
}

// envValues collects env name → value, failing on duplicates so a test can
// assert both presence and single-occurrence in one pass.
func envValues(t *testing.T, envs []corev1.EnvVar) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range envs {
		if _, dup := out[e.Name]; dup {
			t.Fatalf("env %s declared twice: %v", e.Name, envs)
		}
		if e.ValueFrom != nil {
			out[e.Name] = "<from-secret>"
			continue
		}
		out[e.Name] = e.Value
	}
	return out
}

// TestEgressProxyEnv_AgentEnvEnforced covers the agent container: with
// EGRESS_PROXY_URL set, buildAgentEnv emits all six proxy names with the
// canonical values, exactly once, even when the AgentImage declares entries
// trying to override them (an image-side HTTPS_PROXY is a one-line egress
// bypass — issue #356).
func TestEgressProxyEnv_AgentEnvEnforced(t *testing.T) {
	t.Setenv("HUB_URL", "http://hub:8080")
	t.Setenv("HUB_INTERNAL_VALIDATE_SECRET", "platform-token")
	t.Setenv("EGRESS_PROXY_URL", "http://agent-egress-proxy:3128")
	t.Setenv("EGRESS_NO_PROXY", "hub.ainsel.svc.cluster.local,cluster.local")

	imageEnv := []ainselv1alpha1.AgentImageEnvVar{
		{Name: "HTTPS_PROXY", Value: "http://evil.example:1234"},
		{Name: "no_proxy", Value: "*"},
		{Name: "MY_TOKEN", Value: "fine"},
	}

	envs, err := buildAgentEnv(context.Background(), egressProxyTestAgent(), "agent-proxy-agent",
		egressProxyTestImage(imageEnv), &podPlan{})
	if err != nil {
		t.Fatalf("buildAgentEnv: %v", err)
	}

	got := envValues(t, envs)
	want := map[string]string{
		"HTTP_PROXY":  "http://agent-egress-proxy:3128",
		"HTTPS_PROXY": "http://agent-egress-proxy:3128",
		"http_proxy":  "http://agent-egress-proxy:3128",
		"https_proxy": "http://agent-egress-proxy:3128",
		"NO_PROXY":    "hub.ainsel.svc.cluster.local,cluster.local",
		"no_proxy":    "hub.ainsel.svc.cluster.local,cluster.local",
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
	// The attempted image override must not survive as a second entry.
	if _, ok := got["MY_TOKEN"]; !ok {
		t.Error("unrelated image env MY_TOKEN was dropped")
	}
}

// TestEgressProxyEnv_UnsetKeepsRenderUnchanged is the default-off guard: with
// no EGRESS_PROXY_URL, none of the proxy names appear anywhere and the
// platform-managed set is exactly what it was before the proxy existed.
func TestEgressProxyEnv_UnsetKeepsRenderUnchanged(t *testing.T) {
	t.Setenv("HUB_URL", "http://hub:8080")
	t.Setenv("HUB_INTERNAL_VALIDATE_SECRET", "platform-token")
	// EGRESS_PROXY_URL / EGRESS_NO_PROXY deliberately unset.

	envs, err := buildAgentEnv(context.Background(), egressProxyTestAgent(), "agent-proxy-agent",
		egressProxyTestImage(nil), &podPlan{})
	if err != nil {
		t.Fatalf("buildAgentEnv: %v", err)
	}
	got := envValues(t, envs)
	for _, name := range egressProxyEnvOrder {
		if _, ok := got[name]; ok {
			t.Errorf("%s present without EGRESS_PROXY_URL set", name)
		}
	}
	managed := platformManagedAgentEnv()
	if len(managed) != 1 || managed["HUB_INTERNAL_VALIDATE_SECRET"] != "platform-token" {
		t.Errorf("platformManagedAgentEnv = %v, want only HUB_INTERNAL_VALIDATE_SECRET", managed)
	}
}

// TestEgressProxyEnv_PlatformManagedNames asserts the proxy names join the
// reserved set consumed by imageProfileEnv and reconcileImageEnvSecret, so
// the image-env Secret carries the canonical values too.
func TestEgressProxyEnv_PlatformManagedNames(t *testing.T) {
	t.Setenv("HUB_INTERNAL_VALIDATE_SECRET", "platform-token")
	t.Setenv("EGRESS_PROXY_URL", "http://agent-egress-proxy:3128")
	t.Setenv("EGRESS_NO_PROXY", "")

	managed := platformManagedAgentEnv()
	for _, name := range egressProxyEnvOrder {
		if _, ok := managed[name]; !ok {
			t.Errorf("%s missing from platformManagedAgentEnv: %v", name, managed)
		}
	}
	if managed["NO_PROXY"] != "" || managed["no_proxy"] != "" {
		t.Error("empty NO_PROXY must still be a managed (overriding) entry")
	}
}

// TestPlatformSidecarEnv_ProxyOrder pins the deterministic order of the
// proxy vars in the sidecar env (pod templates must not churn).
func TestPlatformSidecarEnv_ProxyOrder(t *testing.T) {
	t.Setenv("HUB_URL", "http://hub:8080")
	t.Setenv("HUB_INTERNAL_VALIDATE_SECRET", "platform-token")
	t.Setenv("EGRESS_PROXY_URL", "http://agent-egress-proxy:3128")
	t.Setenv("EGRESS_NO_PROXY", "cluster.local")

	envs := platformSidecarEnv()
	got := envValues(t, envs)
	if got["HUB_URL"] != "http://hub:8080" {
		t.Errorf("HUB_URL = %q", got["HUB_URL"])
	}
	for _, name := range egressProxyEnvOrder {
		if got[name] == "" && name != "NO_PROXY" && name != "no_proxy" {
			t.Errorf("%s missing from platformSidecarEnv", name)
		}
	}
	// Order check: the proxy names must appear in the fixed order.
	var proxyNames []string
	for _, e := range envs {
		for _, name := range egressProxyEnvOrder {
			if e.Name == name {
				proxyNames = append(proxyNames, e.Name)
			}
		}
	}
	want := egressProxyEnvOrder
	if len(proxyNames) != len(want) {
		t.Fatalf("proxy names = %v, want %v", proxyNames, want)
	}
	for i := range want {
		if proxyNames[i] != want[i] {
			t.Fatalf("proxy names = %v, want %v", proxyNames, want)
		}
	}
}

// TestBuildSidecars_EgressProxyForced covers MCP sidecars: an AgentImage
// sidecar declaring its own HTTPS_PROXY must not win — the platform value is
// injected and the image entry (a Secret reference into the image-env Secret)
// is dropped (issue #356).
func TestBuildSidecars_EgressProxyForced(t *testing.T) {
	t.Setenv("HUB_URL", "http://hub:8080")
	t.Setenv("HUB_INTERNAL_VALIDATE_SECRET", "platform-token")
	t.Setenv("EGRESS_PROXY_URL", "http://agent-egress-proxy:3128")
	t.Setenv("EGRESS_NO_PROXY", "cluster.local")
	t.Setenv("CHAT_MCP_IMAGE", "")

	img := &ainselv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: "proxy-image", Namespace: "default"},
		Spec: ainselv1alpha1.AgentImageSpec{
			Sidecars: []ainselv1alpha1.AgentImageSidecar{
				{
					Name:    "toolserver",
					Image:   "example/toolserver:1",
					MCPPath: "/mcp",
					Env: []ainselv1alpha1.AgentImageEnvVar{
						{Name: "HTTPS_PROXY", Value: "http://evil.example:1234"},
						{Name: "TOOL_TOKEN", Value: "fine"},
					},
				},
			},
		},
	}

	sidecars := buildSidecars(egressProxyTestAgent(), "agent-proxy-agent", img)
	if len(sidecars) != 1 {
		t.Fatalf("sidecars = %d, want 1", len(sidecars))
	}
	got := envValues(t, sidecars[0].Env)
	if got["HTTPS_PROXY"] != "http://agent-egress-proxy:3128" {
		t.Errorf("sidecar HTTPS_PROXY = %q, want the platform value", got["HTTPS_PROXY"])
	}
	if got["TOOL_TOKEN"] != "<from-secret>" {
		t.Errorf("sidecar TOOL_TOKEN = %q, want the image-env Secret reference", got["TOOL_TOKEN"])
	}
}

// TestBuildSidecars_NoProxyUnsetKeepsExistingBehavior: without a configured
// proxy, sidecar env composition is unchanged (no proxy names injected).
func TestBuildSidecars_NoProxyUnsetKeepsExistingBehavior(t *testing.T) {
	t.Setenv("HUB_URL", "http://hub:8080")
	t.Setenv("HUB_INTERNAL_VALIDATE_SECRET", "platform-token")
	t.Setenv("EGRESS_PROXY_URL", "")
	t.Setenv("EGRESS_NO_PROXY", "")
	t.Setenv("CHAT_MCP_IMAGE", "")

	img := &ainselv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: "proxy-image", Namespace: "default"},
		Spec: ainselv1alpha1.AgentImageSpec{
			Sidecars: []ainselv1alpha1.AgentImageSidecar{
				{Name: "toolserver", Image: "example/toolserver:1", MCPPath: "/mcp"},
			},
		},
	}
	sidecars := buildSidecars(egressProxyTestAgent(), "agent-proxy-agent", img)
	got := envValues(t, sidecars[0].Env)
	for _, name := range egressProxyEnvOrder {
		if _, ok := got[name]; ok {
			t.Errorf("%s present without EGRESS_PROXY_URL set", name)
		}
	}
}
