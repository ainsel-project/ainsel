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
