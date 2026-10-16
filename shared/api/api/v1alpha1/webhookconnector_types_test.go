package v1alpha1

import (
	"strings"
	"testing"
)

func TestConnectorImageSplitEmbeddedDigest(t *testing.T) {
	t.Run("moves a pinned digest into its own field and keeps both halves", func(t *testing.T) {
		img := ConnectorImage{Repository: "dpinsel/ainsel-webhook-receiver", Tag: "dev@sha256:521f38"}
		if !img.SplitEmbeddedDigest() {
			t.Fatal("SplitEmbeddedDigest() reported no change for a tag carrying a digest")
		}
		if img.Tag != "dev" {
			t.Fatalf("Tag = %q, want %q", img.Tag, "dev")
		}
		if img.Digest != "sha256:521f38" {
			t.Fatalf("Digest = %q, want %q", img.Digest, "sha256:521f38")
		}
		if img.Repository != "dpinsel/ainsel-webhook-receiver" {
			t.Fatalf("Repository was modified: %q", img.Repository)
		}
	})

	t.Run("leaves a plain tag alone", func(t *testing.T) {
		img := ConnectorImage{Repository: "r", Tag: "latest", Digest: ""}
		if img.SplitEmbeddedDigest() {
			t.Fatal("SplitEmbeddedDigest() reported a change for a plain tag")
		}
		if img.Tag != "latest" || img.Digest != "" {
			t.Fatalf("image modified: %+v", img)
		}
	})

	t.Run("does not clobber an explicit digest", func(t *testing.T) {
		img := ConnectorImage{Repository: "r", Tag: "dev@sha256:aaa", Digest: "sha256:bbb"}
		img.SplitEmbeddedDigest()
		if img.Digest != "sha256:bbb" {
			t.Fatalf("Digest = %q, want the explicit %q to win", img.Digest, "sha256:bbb")
		}
	})

	t.Run("is idempotent", func(t *testing.T) {
		img := ConnectorImage{Repository: "r", Tag: "dev@sha256:521f38"}
		img.SplitEmbeddedDigest()
		if img.SplitEmbeddedDigest() {
			t.Fatal("second SplitEmbeddedDigest() reported a change")
		}
	})
}

func TestConnectorImageReference(t *testing.T) {
	tests := []struct {
		name    string
		image   ConnectorImage
		want    string
		wantErr string
	}{
		{
			name:  "tag only follows the tag",
			image: ConnectorImage{Repository: "dpinsel/ainsel-webhook-receiver", Tag: "dev"},
			want:  "dpinsel/ainsel-webhook-receiver:dev",
		},
		{
			name:  "digest only pins the build",
			image: ConnectorImage{Repository: "r", Digest: "sha256:521f38"},
			want:  "r@sha256:521f38",
		},
		{
			name:  "digest pins and the tag records where it came from",
			image: ConnectorImage{Repository: "r", Tag: "dev", Digest: "sha256:521f38"},
			want:  "r:dev@sha256:521f38",
		},
		{
			// The shape a pinned deploy leaves behind: composing the split fields
			// reproduces the string the receiver already runs, so tidying the CR
			// does not restart anything.
			name:  "a split legacy pin composes back to what was already running",
			image: ConnectorImage{Repository: "r", Tag: "dev", Digest: "sha256:521f38"},
			want:  "r:dev@sha256:521f38",
		},
		{
			name:    "neither tag nor digest is refused rather than guessing latest",
			image:   ConnectorImage{Repository: "r"},
			wantErr: "neither a tag nor a digest",
		},
		{
			name:    "no repository is refused",
			image:   ConnectorImage{Tag: "dev"},
			wantErr: "no repository",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.image.Reference()
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Reference() = %q, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Reference() returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Reference() = %q, want %q", got, tt.want)
			}
		})
	}
}
