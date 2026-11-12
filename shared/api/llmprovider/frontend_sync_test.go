package llmprovider

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// TestFrontendRegistryMatchesGo keeps the console's provider list in step
// with this registry: same IDs, same order. The console cannot import Go, so
// this reads its registry source directly.
func TestFrontendRegistryMatchesGo(t *testing.T) {
	const path = "../../../frontend/src/pages/agents/llmProviders.ts"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var got []string
	for _, m := range regexp.MustCompile(`\bid: '([^']+)'`).FindAllStringSubmatch(string(src), -1) {
		got = append(got, m[1])
	}
	if want := IDs(); !slices.Equal(got, want) {
		t.Fatalf("console provider ids %v, Go registry ids %v", got, want)
	}
}

// TestFrontendPiBuiltInMatchesGo keeps the console's piBuiltIn flag — which
// hides the temperature and points the context window at Pi's catalog — in
// step with the Go registry, and with the operator's rule that only
// OpenAI-compatible providers receive a temperature.
func TestFrontendPiBuiltInMatchesGo(t *testing.T) {
	const path = "../../../frontend/src/pages/agents/llmProviders.ts"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	entries := regexp.MustCompile(`(?s)\bid: '([^']+)'.*?piBuiltIn: (true|false)`).FindAllStringSubmatch(string(src), -1)
	if len(entries) != len(IDs()) {
		t.Fatalf("found %d console entries with piBuiltIn, want %d", len(entries), len(IDs()))
	}
	for _, m := range entries {
		p, ok := Lookup(m[1])
		if !ok {
			t.Errorf("console provider %q is not in the Go registry", m[1])
			continue
		}
		console := m[2] == "true"
		if console != p.PiBuiltIn {
			t.Errorf("%s: console piBuiltIn=%v, Go PiBuiltIn=%v", p.ID, console, p.PiBuiltIn)
		}
		if noTemperature := p.PiAPI != PiAPIOpenAICompletions; console != noTemperature {
			t.Errorf("%s: console hides temperature=%v, operator applies it=%v", p.ID, console, !noTemperature)
		}
	}
}
