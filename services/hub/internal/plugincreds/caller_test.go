package plugincreds

import (
	"context"
	"testing"
)

// ManagerStamp and IsManagerOf are two halves of one rule: the value a plugin
// may stamp at create is exactly the value that lets it through the managed
// guard later. They are defined next to each other so they cannot drift.
func TestManagerStampRoundTrip(t *testing.T) {
	c := &Caller{PluginID: "forgejo", Kind: KindPlugin}

	stamp := ManagerStamp(c.PluginID)
	if stamp != "plugin-forgejo" {
		t.Fatalf("ManagerStamp = %q", stamp)
	}
	if !c.IsManagerOf(stamp) {
		t.Error("a caller must be the manager of its own stamp")
	}
	for _, other := range []string{"", "user", "plugin", "plugin-github", "gitops", "plugin-forgejo-extra"} {
		if c.IsManagerOf(other) {
			t.Errorf("IsManagerOf(%q) = true, want false", other)
		}
	}
}

// Agent- and user-kind credentials carry a PluginID too (the plugin they were
// minted for), so identity-bearing is not the same as being a plugin.
func TestIsManagerOfRejectsNonPluginKinds(t *testing.T) {
	for _, kind := range []string{KindAgent, KindUser, ""} {
		c := &Caller{PluginID: "forgejo", Kind: kind}
		if c.IsPlugin() {
			t.Errorf("kind %q reported itself as a plugin", kind)
		}
		if c.IsManagerOf("plugin-forgejo") {
			t.Errorf("kind %q passed the manager check", kind)
		}
	}
	var nilCaller *Caller
	if nilCaller.IsManagerOf("plugin-forgejo") {
		t.Error("nil caller passed the manager check")
	}
}

func TestCallerFromEmpty(t *testing.T) {
	if _, ok := CallerFrom(context.Background()); ok {
		t.Error("CallerFrom on a bare context reported a caller")
	}
	c := &Caller{PluginID: "forgejo", Kind: KindPlugin}
	if got, ok := CallerFrom(WithCaller(context.Background(), c)); !ok || got != c {
		t.Error("WithCaller/CallerFrom round-trip lost the caller")
	}
}
