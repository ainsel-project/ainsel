// Package signing models the plugin signing policy: the trust material the
// platform accepts when it verifies that a plugin image was published by
// whoever claims to have published it.
//
// The policy is a single YAML file mounted into the operator (see the Helm
// chart's plugin-signing ConfigMap). It is read once at startup, validated,
// and hashed so a reconcile can tell whether the trust material changed since
// the last verification. The policy itself makes no verification decisions —
// it only describes what is trusted; verification lives with cosign.
package signing

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// DefaultPolicyPath is where the operator expects its policy file, and where
// the Helm chart mounts it.
const DefaultPolicyPath = "/etc/ainsel/plugin-signing/policy.yaml"

// KeylessIdentity is a sigstore keyless identity: certificates issued by
// Issuer are trusted when their subject matches Subject (exact) or
// SubjectRegExp (a regexp that must be anchored with ^ and $; Validate rejects it otherwise). Exactly one of the two must be set —
// a regexp that was meant to be an exact string, or vice versa, is a
// misconfiguration, not a refinement.
type KeylessIdentity struct {
	Issuer        string `yaml:"issuer" json:"issuer"`
	Subject       string `yaml:"subject,omitempty" json:"subject,omitempty"`
	SubjectRegExp string `yaml:"subjectRegExp,omitempty" json:"subjectRegExp,omitempty"`
}

// Policy is the operator's view of which signatures count.
//
// With RequireSignature false, an unsigned image is accepted; the trust
// material below then only decides whether a signature that *is* present
// verifies. With it true, a plugin image must carry a signature that verifies
// against Keys or Keyless.
//
// TrustedRootFile points at a sigstore trusted_root.json for installations
// with no path to the public TUF mirror; empty means the public good root.
type Policy struct {
	RequireSignature bool              `yaml:"requireSignature" json:"requireSignature"`
	Keys             []string          `yaml:"keys" json:"keys"`
	Keyless          []KeylessIdentity `yaml:"keyless" json:"keyless"`
	TrustedRootFile  string            `yaml:"trustedRootFile" json:"trustedRootFile"`
}

// LoadPolicy reads and validates the policy at path.
//
// A missing file is not an error: it yields the empty policy, which requires
// no signature and trusts nothing — the phase-1 behaviour, so an install that
// never configured signing keeps working. Any other read failure, a malformed
// file, or a policy that fails Validate is returned as an error, and the
// caller is expected to fail closed rather than proceed with an unvalidated
// policy.
func LoadPolicy(path string) (*Policy, error) {
	// #nosec G304 -- the path is the operator's own flag/chart configuration,
	// never request-supplied input.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Policy{}, nil
		}
		return nil, fmt.Errorf("read signing policy %s: %w", path, err)
	}

	var p Policy
	// Strict so a typo'd key ("signingKeys:", "require_signature:") fails
	// loudly instead of loading as "no trust material configured".
	if err := yaml.UnmarshalStrict(data, &p); err != nil {
		return nil, fmt.Errorf("parse signing policy %s: %w", path, err)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("invalid signing policy %s: %w", path, err)
	}
	return &p, nil
}

// Validate reports every problem with the policy in one error, so an
// administrator fixing a file sees all of it at once. Each problem names the
// offending index (keys[0], keyless[1]).
func (p *Policy) Validate() error {
	var errs []error

	for i, key := range p.Keys {
		if err := validateKeyPEM(i, key); err != nil {
			errs = append(errs, err)
		}
	}

	for i, id := range p.Keyless {
		prefix := fmt.Sprintf("keyless[%d]", i)
		if strings.TrimSpace(id.Issuer) == "" {
			errs = append(errs, fmt.Errorf("%s: issuer is required", prefix))
		}
		hasSubject := id.Subject != ""
		hasRegExp := id.SubjectRegExp != ""
		switch {
		case hasSubject && hasRegExp:
			errs = append(errs, fmt.Errorf("%s: set exactly one of subject or subjectRegExp, not both", prefix))
		case !hasSubject && !hasRegExp:
			errs = append(errs, fmt.Errorf("%s: one of subject or subjectRegExp is required", prefix))
		case hasRegExp:
			if _, err := regexp.Compile(id.SubjectRegExp); err != nil {
				errs = append(errs, fmt.Errorf("%s: subjectRegExp: %w", prefix, err))
			} else if !isAnchored(id.SubjectRegExp) {
				// cosign matches the regexp anywhere in the certificate
				// identity: an unanchored github\.com/acme would also accept
				// github.com/acme-evil. Require the anchors instead of adding
				// them silently, so the file says what it enforces.
				errs = append(errs, fmt.Errorf("%s: subjectRegExp must be anchored with ^ and $", prefix))
			}
		}
	}

	return errors.Join(errs...)
}

// HasTrust reports whether the policy carries any trust material at all. A
// policy with RequireSignature true and no trust material can never verify a
// signature, so callers use this to distinguish "signing is off" from
// "signing is on but nothing is trusted".
func (p *Policy) HasTrust() bool {
	return len(p.Keys)+len(p.Keyless) > 0
}

// Hash returns the hex sha256 of the policy's canonical JSON form. Two
// policies that say the same thing hash the same, including whether a list is
// empty or absent; any change to the trust material changes the hash.
//
// A reconcile uses it to decide whether a previously verified signature is
// still covered by the same trust material.
func (p *Policy) Hash() string {
	// Nil and empty are the same policy; without this the hash would churn on a
	// file that merely dropped an empty list.
	keys := p.Keys
	if keys == nil {
		keys = []string{}
	}
	keyless := make([]canonicalKeyless, 0, len(p.Keyless))
	for _, id := range p.Keyless {
		keyless = append(keyless, canonicalKeyless(id))
	}

	data, err := json.Marshal(canonicalPolicy{
		RequireSignature: p.RequireSignature,
		Keys:             keys,
		Keyless:          keyless,
		TrustedRootFile:  p.TrustedRootFile,
	})
	if err != nil {
		// Unreachable: the canonical form is strings and slices of strings.
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// canonicalPolicy is the shape Hash marshals. Field order is fixed by the
// struct definition and every field is always present, which is what makes
// the digest stable.
type canonicalPolicy struct {
	RequireSignature bool               `json:"requireSignature"`
	Keys             []string           `json:"keys"`
	Keyless          []canonicalKeyless `json:"keyless"`
	TrustedRootFile  string             `json:"trustedRootFile"`
}

type canonicalKeyless struct {
	Issuer        string `json:"issuer"`
	Subject       string `json:"subject"`
	SubjectRegExp string `json:"subjectRegExp"`
}

// isAnchored reports whether re starts with ^ and ends with an unescaped $.
func isAnchored(re string) bool {
	if !strings.HasPrefix(re, "^") || !strings.HasSuffix(re, "$") {
		return false
	}
	// Count the backslashes before the final $: an odd number escapes it.
	n := 0
	for i := len(re) - 2; i >= 0 && re[i] == '\\'; i-- {
		n++
	}
	return n%2 == 0
}

// validateKeyPEM checks that pemBytes is a PEM block holding a usable public
// key — the same shapes cosign accepts for --key.
func validateKeyPEM(index int, pemBytes string) error {
	block, _ := pem.Decode([]byte(pemBytes))
	if block == nil {
		return fmt.Errorf("keys[%d]: not a PEM block", index)
	}

	var pub any
	var err error
	switch block.Type {
	case "PUBLIC KEY": // PKIX, what openssl and cosign emit
		pub, err = x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY": // PKCS#1
		pub, err = x509.ParsePKCS1PublicKey(block.Bytes)
	default:
		return fmt.Errorf("keys[%d]: unsupported PEM type %q, want a PUBLIC KEY", index, block.Type)
	}
	if err != nil {
		return fmt.Errorf("keys[%d]: %w", index, err)
	}

	if key, ok := pub.(*rsa.PublicKey); ok && (key.N == nil || key.N.BitLen() < 2048) {
		// Mirrors the minimum key size cosign enforces, so a weak key is
		// rejected when the policy loads rather than on the first verify.
		return fmt.Errorf("keys[%d]: RSA public key below 2048 bits is not accepted", index)
	}
	return nil
}
