package main

import (
	"context"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/api"
	"github.com/ainsel-project/ainsel/services/hub/internal/selfsync"
)

func TestWireMetricsSource(t *testing.T) {
	// The chart value and the response field are meant to read the same, so the
	// accepted tokens are the api package's, not a second spelling invented here.
	// An unrecognised value must land on the default rather than fail startup:
	// this setting chooses between two backends that both work, so a typo costs a
	// log line, not a pod.
	cases := []struct {
		raw, want string
	}{
		{"", api.MetricsSourceRecords},
		{"postgres", api.MetricsSourceRecords},
		{"POSTGRES", api.MetricsSourceRecords},
		{"  postgres  ", api.MetricsSourceRecords},
		{"prometheus", api.MetricsSourcePrometheus},
		{"Prometheus", api.MetricsSourcePrometheus},
		{"prometheus ", api.MetricsSourcePrometheus},
		{"victoriametrics", api.MetricsSourceRecords},
		{"prom", api.MetricsSourceRecords},
		{"records", api.MetricsSourceRecords},
	}
	for _, tc := range cases {
		if got := wireMetricsSource(tc.raw); got != tc.want {
			t.Errorf("wireMetricsSource(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestWireAuthMiddlewareOIDCPlusLocalGuards covers the startup guards for
// the oidc+local coexistence mode (issue #114). Only the error paths are
// exercised: they all return before any network or container access, so no
// live OIDC issuer or database is needed.
func TestWireAuthMiddlewareOIDCPlusLocalGuards(t *testing.T) {
	setEnv := func(t *testing.T, key, val string) {
		t.Setenv(key, val)
	}

	t.Run("invalid mode error lists oidc+local", func(t *testing.T) {
		setEnv(t, "HUB_AUTH_MODE", "bogus")
		setEnv(t, "OIDC_ISSUER", "")
		setEnv(t, "OIDC_AUDIENCE", "")
		err := wireAuthMiddleware(context.Background(), &container{}, containerConfig{})
		if err == nil || !strings.Contains(err.Error(), "oidc+local") {
			t.Fatalf("expected invalid-mode error to mention oidc+local, got: %v", err)
		}
	})

	t.Run("oidc+local requires issuer and audience", func(t *testing.T) {
		setEnv(t, "HUB_AUTH_MODE", "oidc+local")
		setEnv(t, "OIDC_ISSUER", "")
		setEnv(t, "OIDC_AUDIENCE", "")
		setEnv(t, "HUB_LOCAL_JWT_SECRET", "0123456789abcdef0123456789abcdef")
		err := wireAuthMiddleware(context.Background(), &container{}, containerConfig{})
		if err == nil || !strings.Contains(err.Error(), "OIDC_ISSUER") {
			t.Fatalf("expected issuer/audience requirement error, got: %v", err)
		}
	})

	t.Run("oidc+local requires the local JWT secret", func(t *testing.T) {
		setEnv(t, "HUB_AUTH_MODE", "oidc+local")
		setEnv(t, "OIDC_ISSUER", "https://idp.test/")
		setEnv(t, "OIDC_AUDIENCE", "ainsel")
		setEnv(t, "HUB_LOCAL_JWT_SECRET", "")
		err := wireAuthMiddleware(context.Background(), &container{}, containerConfig{})
		if err == nil || !strings.Contains(err.Error(), "HUB_LOCAL_JWT_SECRET") {
			t.Fatalf("expected HUB_LOCAL_JWT_SECRET requirement error, got: %v", err)
		}
	})
}

func TestPasswordPolicyFromEnv(t *testing.T) {
	// Defaults must reproduce the historical hard-coded policy exactly:
	// min 8, max 128 (hard), no character classes.
	t.Setenv("HUB_LOCAL_PASSWORD_MIN_LENGTH", "")
	t.Setenv("HUB_LOCAL_PASSWORD_CHARACTER_CLASSES", "")
	p, err := passwordPolicyFromEnv()
	if err != nil {
		t.Fatalf("defaults: unexpected error %v", err)
	}
	if p.MinLength != 8 || len(p.RequireClasses) != 0 {
		t.Fatalf("defaults: got %+v", p)
	}

	t.Run("valid overrides", func(t *testing.T) {
		t.Setenv("HUB_LOCAL_PASSWORD_MIN_LENGTH", "12")
		t.Setenv("HUB_LOCAL_PASSWORD_CHARACTER_CLASSES", "upper, digit")
		p, err := passwordPolicyFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if p.MinLength != 12 || len(p.RequireClasses) != 2 || p.RequireClasses[0] != "upper" || p.RequireClasses[1] != "digit" {
			t.Fatalf("got %+v", p)
		}
	})

	// Every rejection must name the offending variable: a typo'd policy must
	// fail the deploy loudly, not silently weaken it.
	cases := []struct {
		name        string
		minLen      string
		classes     string
		wantErrName string
	}{
		{"min length zero", "0", "", "HUB_LOCAL_PASSWORD_MIN_LENGTH"},
		{"min length above hard max", "129", "", "HUB_LOCAL_PASSWORD_MIN_LENGTH"},
		{"min length not an integer", "twelve", "", "HUB_LOCAL_PASSWORD_MIN_LENGTH"},
		{"unknown class", "", "lower,bogus", "HUB_LOCAL_PASSWORD_CHARACTER_CLASSES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HUB_LOCAL_PASSWORD_MIN_LENGTH", tc.minLen)
			t.Setenv("HUB_LOCAL_PASSWORD_CHARACTER_CLASSES", tc.classes)
			_, err := passwordPolicyFromEnv()
			if err == nil {
				t.Fatal("want startup error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErrName) {
				t.Fatalf("error %q must name %s", err, tc.wantErrName)
			}
		})
	}
}

// The hub registers its own endpoint by joining a configured base URL to a path
// constant, and the operator only injects the platform credential into a server
// whose URL is exactly one of the hub's agent endpoints. Two packages, two
// spellings of one path — which is why this assertion exists: a drift here makes
// every agent's platform tools fail authentication, and nothing else fails.
func TestSelfRegistrationPathMatchesTheRegisteredRoute(t *testing.T) {
	if selfsync.AgentMCPEndpointPath != api.AgentMCPPath {
		t.Errorf("registered route = %q, self-registration writes %q", api.AgentMCPPath, selfsync.AgentMCPEndpointPath)
	}
}

// The plane is opt-in, and one flag has to gate both halves of it: an endpoint
// that answers 503 while a registry row advertises it is worse than no row,
// because an agent reads the refusal as a broken platform rather than as an
// operator choice.
func TestSelfRegistrationRequiresTheEnabledPlane(t *testing.T) {
	t.Setenv("HUB_SELF_URL", "http://hub-backend.test-ns.svc.cluster.local:8080")
	t.Setenv(agentMCPEnabledEnv, "")
	if s := wireSelfSync(&container{}); s != nil {
		t.Error("a disabled plane must not register the hub's endpoint")
	}

	t.Setenv(agentMCPEnabledEnv, "true")
	if s := wireSelfSync(&container{}); s == nil {
		t.Error("an enabled plane with HUB_SELF_URL set must register the hub's endpoint")
	}

	t.Setenv("HUB_SELF_URL", "")
	if s := wireSelfSync(&container{}); s != nil {
		t.Error("without HUB_SELF_URL there is no address to register")
	}
}
