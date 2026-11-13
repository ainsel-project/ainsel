package authz

import "testing"

// These values are wire-visible — stored in resource_groups.resource_type,
// used as the `type` field of the group-resources API, and DefaultGroupID is
// a row id created by migration 0016 — so a typo must fail a test, not
// production.

func TestDefaultGroupID(t *testing.T) {
	if DefaultGroupID != "legacy" {
		t.Errorf("DefaultGroupID = %q, want %q (the group id created by migration 0016)", DefaultGroupID, "legacy")
	}
}

func TestResourceTypeValues(t *testing.T) {
	// Explicit comparisons: the constant on the left, the wire value on the
	// right, so changing a constant breaks this test instead of renaming the
	// expectation with it.
	checks := []struct {
		got  ResourceType
		want string
	}{
		{ResourceAgent, "agent"},
		{ResourceAgentImage, "agent-image"},
		{ResourceConnector, "connector"},
		{ResourcePersona, "persona"},
		{ResourceSkill, "skill"},
		{ResourceChannel, "channel"},
		{ResourceMCPServer, "mcp-server"},
		{ResourceTrigger, "trigger"},
		{ResourceCronTrigger, "cron-trigger"},
	}
	for _, c := range checks {
		if string(c.got) != c.want {
			t.Errorf("ResourceType = %q, want %q", string(c.got), c.want)
		}
	}
}

func TestAllResourceTypesComplete(t *testing.T) {
	seen := map[ResourceType]bool{}
	for _, rt := range AllResourceTypes {
		if seen[rt] {
			t.Errorf("AllResourceTypes contains %q twice", rt)
		}
		seen[rt] = true
	}
	for _, rt := range []ResourceType{
		ResourceAgent, ResourceAgentImage, ResourceConnector, ResourcePersona,
		ResourceSkill, ResourceChannel, ResourceMCPServer, ResourceTrigger,
		ResourceCronTrigger,
	} {
		if !seen[rt] {
			t.Errorf("AllResourceTypes is missing %q", rt)
		}
	}
	if len(AllResourceTypes) != 9 {
		t.Errorf("AllResourceTypes has %d entries, want 9", len(AllResourceTypes))
	}
}
