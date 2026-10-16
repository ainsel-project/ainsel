package api

import (
	"fmt"
	"os"
	"strings"
)

type ConnectorConfig struct {
	PlatformExternalURL string
	ImageWebhook        string
}

func LoadConnectorConfig() ConnectorConfig {
	return ConnectorConfig{
		PlatformExternalURL: os.Getenv("PLATFORM_EXTERNAL_URL"),
		ImageWebhook:        os.Getenv("CONNECTOR_IMAGE_WEBHOOK"),
	}
}

func (c ConnectorConfig) WebhookEndpoint(connectorName string) string {
	base := strings.TrimRight(c.PlatformExternalURL, "/")
	return fmt.Sprintf("%s/webhooks/%s", base, connectorName)
}

// ConnectorImageRef is a parsed connector image reference.
type ConnectorImageRef struct {
	Repository string
	Tag        string
	// Digest is the digest the configured value carried after "@", if any. It is
	// carried through to the connector, which then runs that exact build; the tag
	// beside it records which tag the build came from.
	Digest string
}

// String renders the reference the way a container runtime would take it.
func (r ConnectorImageRef) String() string {
	switch {
	case r.Digest != "" && r.Tag != "":
		return r.Repository + ":" + r.Tag + "@" + r.Digest
	case r.Digest != "":
		return r.Repository + "@" + r.Digest
	default:
		return r.Repository + ":" + r.Tag
	}
}

// WebhookImage parses CONNECTOR_IMAGE_WEBHOOK ("repository[:tag][@digest]") into
// the fields a new connector's spec should carry.
//
// A digest is kept rather than folded into the tag, because the two mean different
// things: a tag is a moving pointer the receiver follows, a digest is a demand that
// it stop moving. Flattening one into the other is what made a pinned connector
// indistinguishable from a moving one — the pipeline's deploy-time digest landed in
// spec.image.tag, the operator composed it back onto the container image, and the
// receiver was frozen while its CR still advertised a tag.
func (c ConnectorConfig) WebhookImage() (ConnectorImageRef, error) {
	ref := strings.TrimSpace(c.ImageWebhook)
	if ref == "" {
		return ConnectorImageRef{}, fmt.Errorf("CONNECTOR_IMAGE_WEBHOOK not configured")
	}

	out := ConnectorImageRef{}
	if base, digest, found := strings.Cut(ref, "@"); found {
		out.Digest = digest
		ref = base
	}

	// A colon is a tag separator only when what follows it is not a path: in
	// "zot.platform.svc:5000/ainsel/receiver" the colon is a registry port and the
	// reference simply has no tag. The last colon is the candidate, so a tagged
	// image in that registry keeps its tag.
	repository, tag, hasTag := ref, "", false
	if i := strings.LastIndex(ref, ":"); i >= 0 {
		if tail := ref[i+1:]; !strings.Contains(tail, "/") {
			repository, tag, hasTag = ref[:i], tail, true
		}
	}
	if !hasTag && out.Digest == "" {
		// Nothing pins the image, so the tag has to say what to pull; an absent tag
		// means latest, the same default a container runtime would apply.
		tag = "latest"
	}

	if repository == "" {
		return ConnectorImageRef{}, fmt.Errorf("CONNECTOR_IMAGE_WEBHOOK %q has no repository", c.ImageWebhook)
	}
	if tag == "" && out.Digest == "" {
		return ConnectorImageRef{}, fmt.Errorf("CONNECTOR_IMAGE_WEBHOOK %q has neither a tag nor a digest", c.ImageWebhook)
	}

	out.Repository = repository
	out.Tag = tag
	return out, nil
}
