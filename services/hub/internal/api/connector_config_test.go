package api

import (
	"strings"
	"testing"
)

func TestWebhookImage(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		wantRepo   string
		wantTag    string
		wantDigest string
		wantErr    string
	}{
		{
			name:       "repository and tag",
			configured: "dpinsel/ainsel-webhook-receiver:dev",
			wantRepo:   "dpinsel/ainsel-webhook-receiver",
			wantTag:    "dev",
		},
		{
			name:       "no tag defaults to latest",
			configured: "dpinsel/ainsel-webhook-receiver",
			wantRepo:   "dpinsel/ainsel-webhook-receiver",
			wantTag:    "latest",
		},
		{
			name:       "registry port is not a tag",
			configured: "zot.platform.svc.cluster.local:5000/ainsel/ainsel-webhook-receiver",
			wantRepo:   "zot.platform.svc.cluster.local:5000/ainsel/ainsel-webhook-receiver",
			wantTag:    "latest",
		},
		{
			name:       "tagged image in a registry with a port keeps its tag",
			configured: "zot.platform.svc.cluster.local:5000/ainsel/ainsel-webhook-receiver:v1.2.3",
			wantRepo:   "zot.platform.svc.cluster.local:5000/ainsel/ainsel-webhook-receiver",
			wantTag:    "v1.2.3",
		},
		{
			// The regression this exists for: a deploy pipeline that pinned digests
			// wrote "dev@sha256:…" into the chart's tag field, and the whole string
			// used to be copied into spec.image.tag - where the operator composed it
			// back onto the container image while the CR advertised a tag. The digest
			// is a real intent, so it is kept, in the field that means it.
			name:       "digest is kept, beside the tag it came from",
			configured: "dpinsel/ainsel-webhook-receiver:dev@sha256:521f386b0f6f",
			wantRepo:   "dpinsel/ainsel-webhook-receiver",
			wantTag:    "dev",
			wantDigest: "sha256:521f386b0f6f", // carried, not dropped
		},
		{
			name:       "digest with no tag pins the build",
			configured: "dpinsel/ainsel-webhook-receiver@sha256:521f386b0f6f",
			wantRepo:   "dpinsel/ainsel-webhook-receiver",
			wantTag:    "",
			wantDigest: "sha256:521f386b0f6f",
		},
		{
			name:       "surrounding whitespace is tolerated",
			configured: "  dpinsel/ainsel-webhook-receiver:dev\t",
			wantRepo:   "dpinsel/ainsel-webhook-receiver",
			wantTag:    "dev",
		},
		{
			name:       "empty is refused",
			configured: "",
			wantErr:    "not configured",
		},
		{
			name:       "empty tag with no digest is refused",
			configured: "dpinsel/ainsel-webhook-receiver:",
			wantErr:    "neither a tag nor a digest",
		},
		{
			name:       "missing repository is refused",
			configured: ":dev",
			wantErr:    "no repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ConnectorConfig{ImageWebhook: tt.configured}
			got, err := cfg.WebhookImage()
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("WebhookImage() = %+v, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("WebhookImage() returned unexpected error: %v", err)
			}
			if got.Repository != tt.wantRepo {
				t.Fatalf("Repository = %q, want %q", got.Repository, tt.wantRepo)
			}
			if got.Tag != tt.wantTag {
				t.Fatalf("Tag = %q, want %q", got.Tag, tt.wantTag)
			}
			if got.Digest != tt.wantDigest {
				t.Fatalf("Digest = %q, want %q", got.Digest, tt.wantDigest)
			}
			if strings.Contains(got.Tag, "@") {
				t.Fatalf("Tag = %q, want a tag with no digest in it", got.Tag)
			}
			// Whatever the two fields hold, they must compose back to a reference a
			// runtime accepts: either one of them names the image.
			if got.Tag == "" && got.Digest == "" {
				t.Fatal("neither tag nor digest was parsed, so nothing says what to run")
			}
		})
	}
}

func TestConnectorImageRefString(t *testing.T) {
	tests := []struct {
		name string
		ref  ConnectorImageRef
		want string
	}{
		{"tag", ConnectorImageRef{Repository: "r", Tag: "dev"}, "r:dev"},
		{"digest only", ConnectorImageRef{Repository: "r", Digest: "sha256:abc"}, "r@sha256:abc"},
		{"tag and digest", ConnectorImageRef{Repository: "r", Tag: "dev", Digest: "sha256:abc"}, "r:dev@sha256:abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
