package signing

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/sigstore/cosign/v3/pkg/cosign"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/cosign/v3/pkg/oci"
)

// Result is the output of a signature verification.
type Result struct {
	Verified bool   // true if the signature was verified
	Signer   string // "key:<sha256 fingerprint>" or "keyless:<issuer> <subject>"
}

// Verifier can verify container image signatures.
type Verifier interface {
	Verify(ctx context.Context, imageRef string) (Result, error)
}

// NewCosignVerifier creates a Verifier that uses cosign to verify signatures
// against the trust material in the policy.
//
// If the policy carries no trust material (empty Keys and Keyless),
// Verify returns Result{}, nil (nothing to verify against).
func NewCosignVerifier(p *Policy) (Verifier, error) {
	cv := &cosignVerifier{
		policy:      p,
		verifyImage: cosign.VerifyImageSignatures,
	}
	return cv, nil
}

type cosignVerifier struct {
	policy      *Policy
	verifyImage func(ctx context.Context, signedImgRef name.Reference, co *cosign.CheckOpts) ([]oci.Signature, bool, error)
}

// Verify verifies a plugin image signature.
func (v *cosignVerifier) Verify(ctx context.Context, imageRef string) (Result, error) {
	// If no trust material, nothing to verify against.
	if !v.policy.HasTrust() {
		return Result{}, nil
	}

	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return Result{}, fmt.Errorf("parse image reference: %w", err)
	}

	var lastError error

	// Try each key first.
	for _, keyPEM := range v.policy.Keys {
		result, err := v.verifyWithKey(ctx, ref, keyPEM)
		if err == nil && result.Verified {
			return result, nil
		}
		if err != nil && !isNoSignaturesError(err) {
			lastError = err
		}
		// Continue to the next key on verification failure.
	}

	// Then try all keyless identities in one call.
	if len(v.policy.Keyless) > 0 {
		result, err := v.verifyKeyless(ctx, ref)
		if err == nil && result.Verified {
			return result, nil
		}
		if err != nil && !isNoSignaturesError(err) {
			lastError = err
		}
		// Continue if verification failed.
	}

	// If we got a real error (not just "no signatures"), return it.
	if lastError != nil {
		return Result{}, lastError
	}

	return Result{Verified: false}, nil
}

func (v *cosignVerifier) verifyWithKey(ctx context.Context, ref name.Reference, keyPEM string) (Result, error) {
	// Parse the public key from PEM.
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return Result{}, fmt.Errorf("invalid PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return Result{}, fmt.Errorf("parse public key: %w", err)
	}

	// Create a signature.Verifier from the public key.
	// TODO: implement key fingerprinting and signature verification setup.

	// Try legacy .sig layout first, then NewBundleFormat.
	for _, useNewBundle := range []bool{false, true} {
		co := &cosign.CheckOpts{
			SigVerifier:      nil, // TODO: set from pub
			IgnoreTlog:       true,
			IgnoreSCT:        true,
			NewBundleFormat:  useNewBundle,
			RegistryClientOpts: nil,
		}

		sigs, ok, err := v.verifyImage(ctx, ref, co)
		if err != nil {
			// Check if it's a "no matching signatures" error.
			if isNoSignaturesError(err) {
				continue // Try next mode.
			}
			return Result{}, err // Real error (network, etc.)
		}

		if ok && len(sigs) > 0 {
			// Verification succeeded. Extract the signer fingerprint.
			fingerprint := fingerprintFromKey(pub)
			return Result{
				Verified: true,
				Signer:   "key:" + fingerprint,
			}, nil
		}
	}

	return Result{Verified: false}, nil
}

func (v *cosignVerifier) verifyKeyless(ctx context.Context, ref name.Reference) (Result, error) {
	// Load the trusted root (public good or from file).
	var tm root.TrustedMaterial
	var err error

	if v.policy.TrustedRootFile != "" {
		// TODO: load from file.
		_ = v.policy.TrustedRootFile
	} else {
		tm, err = cosign.TrustedRoot()
		if err != nil {
			return Result{}, fmt.Errorf("load trusted root: %w", err)
		}
	}

	// Map policy keyless identities to cosign identities.
	var identities []cosign.Identity
	for _, policyID := range v.policy.Keyless {
		id := cosign.Identity{
			Issuer: policyID.Issuer,
		}
		if policyID.Subject != "" {
			id.Subject = policyID.Subject
		} else {
			id.SubjectRegExp = policyID.SubjectRegExp
		}
		identities = append(identities, id)
	}

	// Try legacy .sig layout first, then NewBundleFormat.
	for _, useNewBundle := range []bool{false, true} {
		co := &cosign.CheckOpts{
			TrustedMaterial:     tm,
			Identities:          identities,
			IgnoreTlog:          false, // tlog not ignored for keyless
			IgnoreSCT:           false,
			NewBundleFormat:     useNewBundle,
			RegistryClientOpts:  nil,
		}

		sigs, ok, err := v.verifyImage(ctx, ref, co)
		if err != nil {
			// Check if it's a "no matching signatures" error.
			if isNoSignaturesError(err) {
				continue // Try next mode.
			}
			return Result{}, err // Real error (network, etc.)
		}

		if ok && len(sigs) > 0 {
			// Verification succeeded. Extract the signer subject from the certificate.
			// If we can't extract it, use the SubjectRegExp string.
			issuer := v.policy.Keyless[0].Issuer
			subject := ""

			if len(sigs) > 0 {
				cert, err := sigs[0].Cert()
				if err == nil && cert != nil {
					subject = cert.Subject.String()
				}
			}

			if subject == "" {
				// Fall back to the SubjectRegExp if present.
				if v.policy.Keyless[0].SubjectRegExp != "" {
					subject = v.policy.Keyless[0].SubjectRegExp
				}
			}

			return Result{
				Verified: true,
				Signer:   "keyless:" + issuer + " " + subject,
			}, nil
		}
	}

	return Result{Verified: false}, nil
}

// isNoSignaturesError checks if an error is a "no signatures found" or "no matching signatures" error.
func isNoSignaturesError(err error) bool {
	if err == nil {
		return false
	}
	// Check if the error message contains "no matching signatures" or similar patterns.
	// This is a simple heuristic that matches cosign's error messages.
	errStr := err.Error()
	return contains(errStr, "no matching signatures") || contains(errStr, "no signatures found")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// fingerprintFromKey returns the sha256 fingerprint of a public key (first 16 hex characters).
func fingerprintFromKey(pub any) string {
	// For now, return a placeholder. TODO: implement proper fingerprinting.
	return "0000000000000000"
}
