package llmprovider

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

func TestIDsOrder(t *testing.T) {
	want := []string{"ollama-cloud", "opencode", "alibaba-cloud", "openai", "anthropic", "google", "custom"}
	if got := IDs(); !slices.Equal(got, want) {
		t.Fatalf("IDs() = %v, want %v", got, want)
	}
}

func TestLookupUnknown(t *testing.T) {
	for _, id := range []string{"", "nope"} {
		if _, ok := Lookup(id); ok {
			t.Errorf("Lookup(%q) found a provider, want none", id)
		}
	}
}

func TestCredentialRoundTrip(t *testing.T) {
	ref := &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "x"}, Key: "api-key"}
	for _, p := range All() {
		t.Run(p.ID, func(t *testing.T) {
			var spec v1alpha1.AgentSpec
			if _, ok := p.Credential(&spec); ok {
				t.Fatal("Credential on an empty spec reported a block")
			}
			p.SetCredential(&spec, Credential{APIKeySecretRef: ref, BaseURL: "https://b"})
			got, ok := p.Credential(&spec)
			if !ok {
				t.Fatal("Credential after SetCredential reported no block")
			}
			if got.APIKeySecretRef == nil || got.APIKeySecretRef.Name != "x" || got.APIKeySecretRef.Key != "api-key" {
				t.Errorf("APIKeySecretRef = %+v, want x/api-key", got.APIKeySecretRef)
			}
			if got.BaseURL != "https://b" {
				t.Errorf("BaseURL = %q, want https://b", got.BaseURL)
			}
		})
	}
	var spec v1alpha1.AgentSpec
	custom, _ := Lookup("custom")
	custom.SetCredential(&spec, Credential{BaseURL: "https://b"})
	if spec.CustomProvider == nil || spec.CustomProvider.URL != "https://b" {
		t.Errorf("custom BaseURL did not land in spec.customProvider.url: %+v", spec.CustomProvider)
	}
}

func TestBaseURLOverride(t *testing.T) {
	p, _ := Lookup("anthropic")
	var spec v1alpha1.AgentSpec
	if got := p.BaseURL(&spec); got != "https://api.anthropic.com" {
		t.Errorf("BaseURL without block = %q, want the default", got)
	}
	spec.Anthropic = &v1alpha1.AgentAnthropic{BaseURL: "https://b"}
	if got := p.BaseURL(&spec); got != "https://b" {
		t.Errorf("BaseURL with override = %q, want https://b", got)
	}
}

func TestPiNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range All() {
		if seen[p.PiName] {
			t.Errorf("duplicate PiName %q", p.PiName)
		}
		seen[p.PiName] = true
		// Pi's built-in providers must stay untouched (ChatGPT sign-in needs "openai").
		if slices.Contains([]string{"openai", "anthropic", "google"}, p.PiName) {
			t.Errorf("provider %q uses Pi built-in provider key %q", p.ID, p.PiName)
		}
	}
}

func TestAllReturnsCopy(t *testing.T) {
	All()[0].ID = "mutated"
	if All()[0].ID == "mutated" {
		t.Fatal("All() exposes the registry's backing slice")
	}
}
