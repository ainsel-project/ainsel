package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/api"
	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
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
// wireAuthPlanForTest drops the returned plan so the guard tests read as error
// checks. wireAuthPlan is the successor of the old wireAuthMiddleware (issue
// #264): the chain is now composed before the server exists.
// wireAuthPlanForMode is wireAuthPlan with the mode resolution newContainer does
// before calling it, for the tests that assert on the plan rather than on the
// error.
func wireAuthPlanForMode(ctx context.Context) (authPlan, error) {
	mode, err := resolveAuthMode()
	if err != nil {
		return authPlan{}, err
	}
	// Same reading of the insecure-no-auth override main.go does, so a mode-"none"
	// plan is only reachable here the way it is reachable in production.
	cfg := containerConfig{hubAllowInsecureNoAuth: os.Getenv("HUB_ALLOW_INSECURE_NO_AUTH") == "true"}
	return wireAuthPlan(ctx, &container{authMode: mode}, cfg)
}

func wireAuthPlanForTest(ctx context.Context, c *container, cfg containerConfig) error {
	// newContainer resolves the mode before wiring the chain; mirror that here.
	mode, err := resolveAuthMode()
	if err != nil {
		return err
	}
	c.authMode = mode
	_, err = wireAuthPlan(ctx, c, cfg)
	return err
}

func TestWireAuthMiddlewareOIDCPlusLocalGuards(t *testing.T) {
	setEnv := func(t *testing.T, key, val string) {
		t.Setenv(key, val)
	}

	t.Run("invalid mode error lists oidc+local", func(t *testing.T) {
		setEnv(t, "HUB_AUTH_MODE", "bogus")
		setEnv(t, "OIDC_ISSUER", "")
		setEnv(t, "OIDC_AUDIENCE", "")
		err := wireAuthPlanForTest(context.Background(), &container{}, containerConfig{})
		if err == nil || !strings.Contains(err.Error(), "oidc+local") {
			t.Fatalf("expected invalid-mode error to mention oidc+local, got: %v", err)
		}
	})

	t.Run("oidc+local requires issuer and audience", func(t *testing.T) {
		setEnv(t, "HUB_AUTH_MODE", "oidc+local")
		setEnv(t, "OIDC_ISSUER", "")
		setEnv(t, "OIDC_AUDIENCE", "")
		setEnv(t, "HUB_LOCAL_JWT_SECRET", "0123456789abcdef0123456789abcdef")
		err := wireAuthPlanForTest(context.Background(), &container{}, containerConfig{})
		if err == nil || !strings.Contains(err.Error(), "OIDC_ISSUER") {
			t.Fatalf("expected issuer/audience requirement error, got: %v", err)
		}
	})

	t.Run("oidc+local requires the local JWT secret", func(t *testing.T) {
		setEnv(t, "HUB_AUTH_MODE", "oidc+local")
		setEnv(t, "OIDC_ISSUER", "https://idp.test/")
		setEnv(t, "OIDC_AUDIENCE", "ainsel")
		setEnv(t, "HUB_LOCAL_JWT_SECRET", "")
		err := wireAuthPlanForTest(context.Background(), &container{}, containerConfig{})
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

// TestWireAuthPlanDefaultGroupID pins the wiring api.Deps.DefaultGroupID's
// doc-comment promises: cmd/hub sets it from the resolved auth mode.
//
// Nothing else catches a plan that never sets it. The api tests that exercise the
// group-less create path pass "legacy" to the handlers themselves, so they stay
// green while a real mode-"none" install files every group-less create as "" and
// rejects it with "groupId is required" — the one thing the mode has no way to
// satisfy, because its group endpoints answer authz.ErrNoRegistry.
func TestWireAuthPlanDefaultGroupID(t *testing.T) {
	t.Run("mode none carries the single group", func(t *testing.T) {
		t.Setenv("HUB_AUTH_MODE", "none")
		t.Setenv("HUB_ALLOW_INSECURE_NO_AUTH", "true")
		plan, err := wireAuthPlanForMode(context.Background())
		if err != nil {
			t.Fatalf("wireAuthPlan(none): %v", err)
		}
		if plan.defaultGroupID != authz.DefaultGroupID {
			t.Errorf("mode none: defaultGroupID = %q, want %q", plan.defaultGroupID, authz.DefaultGroupID)
		}
	})

	// An authenticating mode has a real group registry, so a create that names no
	// group is the caller's mistake and stays a 400. The plan must not quietly
	// default it into the legacy group.
	t.Run("mode oidc leaves groupId mandatory", func(t *testing.T) {
		issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/openid-configuration":
				_ = json.NewEncoder(w).Encode(map[string]string{
					"issuer":                 "http://" + r.Host,
					"jwks_uri":               "http://" + r.Host + "/keys",
					"userinfo_endpoint":      "http://" + r.Host + "/userinfo",
					"authorization_endpoint": "http://" + r.Host + "/authorize",
				})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer issuer.Close()

		t.Setenv("HUB_AUTH_MODE", "oidc")
		t.Setenv("OIDC_ISSUER", issuer.URL)
		t.Setenv("OIDC_AUDIENCE", "ainsel")
		plan, err := wireAuthPlanForMode(context.Background())
		if err != nil {
			t.Fatalf("wireAuthPlan(oidc): %v", err)
		}
		if plan.defaultGroupID != "" {
			t.Errorf("mode oidc: defaultGroupID = %q, want empty", plan.defaultGroupID)
		}
	})
}
