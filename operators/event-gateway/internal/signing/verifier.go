package signing

import (
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/sigstore/cosign/v3/pkg/cosign"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
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
//
// If the policy specifies a TrustedRootFile, it is loaded and validated now.
// A missing or invalid file returns an error.
func NewCosignVerifier(p *Policy) (Verifier, error) {
	var trustedRoot root.TrustedMaterial

	// Load trusted root file if specified, so errors are caught early.
	if p.TrustedRootFile != "" {
		tm, err := root.NewTrustedRootFromPath(p.TrustedRootFile)
		if err != nil {
			return nil, fmt.Errorf("load trusted root file %s: %w", p.TrustedRootFile, err)
		}
		trustedRoot = tm
	}

	cv := &cosignVerifier{
		policy:         p,
		trustedRoot:    trustedRoot,
		verifyImage:    cosign.VerifyImageSignatures,
	}
	return cv, nil
}

type cosignVerifier struct {
	policy      *Policy
	trustedRoot root.TrustedMaterial
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
	// Parse the public key from PEM using cryptoutils.
	pub, err := cryptoutils.UnmarshalPEMToPublicKey([]byte(keyPEM))
	if err != nil {
		return Result{}, fmt.Errorf("parse public key: %w", err)
	}

	// Create a signature.Verifier from the public key.
	sigVerifier, err := signature.LoadVerifier(pub, crypto.SHA256)
	if err != nil {
		return Result{}, fmt.Errorf("load signature verifier: %w", err)
	}

	// Try legacy .sig layout first, then NewBundleFormat.
	for _, useNewBundle := range []bool{false, true} {
		co := &cosign.CheckOpts{
			SigVerifier:        sigVerifier,
			IgnoreTlog:         true,
			IgnoreSCT:          true,
			NewBundleFormat:    useNewBundle,
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
			fingerprint, err := fingerprintFromKey(pub)
			if err != nil {
				return Result{}, fmt.Errorf("compute fingerprint: %w", err)
			}
			return Result{
				Verified: true,
				Signer:   "key:" + fingerprint,
			}, nil
		}
	}

	return Result{Verified: false}, nil
}

func (v *cosignVerifier) verifyKeyless(ctx context.Context, ref name.Reference) (Result, error) {
	// Get the trusted root (either preloaded from file or load public good TUF now).
	var tm root.TrustedMaterial
	var err error

	if v.trustedRoot != nil {
		tm = v.trustedRoot
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

// fingerprintFromKey returns the sha256 fingerprint of a public key (first 16 hex characters of DER encoding).
func fingerprintFromKey(pub any) (string, error) {
	// Marshal the public key to DER format.
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}

	// Compute SHA256 of the DER bytes.
	sum := sha256.Sum256(der)

	// Return first 16 hex characters (64 bits).
	return hex.EncodeToString(sum[:])[:16], nil
}
