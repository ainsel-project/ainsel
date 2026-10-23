package hello

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestVisibility_OnlyAgentsTheCallerCanRead is written to be copied into your
// own plugin's test suite: it needs nothing from this package except New, so
// paste it next to your handler and change the names.
//
// The rule it pins is the one AInsel's group model depends on. A plugin gets one
// credential, its own — minted for the plugin, not for the human or agent that
// happened to ask. So the plugin, not the hub, is the last thing standing between
// a caller and the list of agents it can see. The plugin cannot decide that list
// itself; it must ask the hub, as the caller, through
// POST /api/v1/plugin-context/visible, and then return exactly what the hub said.
//
// The fake hub below answers "visible = the names this caller may read", which
// is what the real hub does with its group grants. If your plugin ever returned
// an agent the hub did not list, this test would fail — and in production the
// caller would see an agent they were never granted.
func TestVisibility_OnlyAgentsTheCallerCanRead(t *testing.T) {
	// readable is the hub's group model, expressed as "which agents may this
	// caller read". a3 is readable by nobody, so a plugin that ignores the
	// lookup is caught by it.
	readable := map[string][]string{
		"ainselp_reader": {"a1"},
		"ainselp_admin":  {"a1", "a2"},
	}

	// hub records the bearer of every request the plugin made, so the test can
	// check the plugin asked as the caller rather than as itself.
	var hubBearers []string

	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/plugin-context/visible" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		hubBearers = append(hubBearers, bearer)

		var req struct {
			ResourceType string   `json:"resourceType"`
			Names        []string `json:"names"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.ResourceType != "agent" {
			http.Error(w, "unknown resourceType", http.StatusBadRequest)
			return
		}

		// Answer in the requested order, keeping only what the caller may read.
		visible := []string{}
		for _, name := range req.Names {
			if slices.Contains(readable[bearer], name) {
				visible = append(visible, name)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string][]string{"visible": visible})
	}))
	defer hub.Close()

	plugin := httptest.NewServer(New(hub.URL))
	defer plugin.Close()

	cases := []struct {
		name       string
		credential string
		want       string
	}{
		{"reader sees only a1", "ainselp_reader", `{"visible":["a1"]}`},
		{"admin sees a1 and a2", "ainselp_admin", `{"visible":["a1","a2"]}`},
		{"unknown caller sees nothing", "ainselp_stranger", `{"visible":[]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hubBearers = nil

			req, err := http.NewRequest(http.MethodGet, plugin.URL+"/api/visible-agents?names=a1,a2,a3", nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("X-Ainsel-Credential", tc.credential)

			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer func() { _ = res.Body.Close() }()
			raw, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			if res.StatusCode != http.StatusOK {
				t.Fatalf("status: got %d, want %d", res.StatusCode, http.StatusOK)
			}
			if string(raw) != tc.want {
				t.Fatalf("body: got %s, want %s", raw, tc.want)
			}

			// The plugin must have asked the hub as this caller, not with its own
			// background credential: asking as itself would see every agent.
			if len(hubBearers) != 1 || hubBearers[0] != tc.credential {
				t.Fatalf("hub bearers: got %v, want exactly [%s]", hubBearers, tc.credential)
			}

			// The copy-paste guard: nothing the hub did not list may come back.
			var answer struct {
				Visible []string `json:"visible"`
			}
			if err := json.Unmarshal(raw, &answer); err != nil {
				t.Fatalf("decode answer: %v", err)
			}
			for _, name := range answer.Visible {
				if !slices.Contains(readable[tc.credential], name) {
					t.Errorf("visible: got %q, which %s cannot read", name, tc.credential)
				}
			}
		})
	}
}
