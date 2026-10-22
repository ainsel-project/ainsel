package signing

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/sigstore/cosign/v3/pkg/cosign"
	"github.com/sigstore/cosign/v3/pkg/cosign/bundle"
	"github.com/sigstore/cosign/v3/pkg/oci"
)

// mockSignature implements oci.Signature with just enough data for the tests.
type mockSignature struct {
	cert []byte // DER-encoded certificate, or nil
}

// Layer interface methods
func (m *mockSignature) Digest() (v1.Hash, error) {
	return v1.Hash{}, nil
}

func (m *mockSignature) DiffID() (v1.Hash, error) {
	return v1.Hash{}, nil
}

func (m *mockSignature) Compressed() (io.ReadCloser, error) {
	return nil, nil
}

func (m *mockSignature) Uncompressed() (io.ReadCloser, error) {
	return nil, nil
}

func (m *mockSignature) Size() (int64, error) {
	return 0, nil
}

func (m *mockSignature) MediaType() (types.MediaType, error) {
	return "application/octet-stream", nil
}

// Signature interface methods
func (m *mockSignature) Annotations() (map[string]string, error) {
	return nil, nil
}

func (m *mockSignature) Payload() ([]byte, error) {
	return []byte("test payload"), nil
}

func (m *mockSignature) Signature() ([]byte, error) {
	return []byte("test signature"), nil
}

func (m *mockSignature) Base64Signature() (string, error) {
	return "dGVzdCBzaWduYXR1cmU=", nil // base64("test signature")
}

func (m *mockSignature) Cert() (*x509.Certificate, error) {
	if m.cert == nil {
		return nil, nil
	}
	return x509.ParseCertificate(m.cert)
}

func (m *mockSignature) Chain() ([]*x509.Certificate, error) {
	return nil, nil
}

func (m *mockSignature) Bundle() (*bundle.RekorBundle, error) {
	return nil, nil
}

func (m *mockSignature) RFC3161Timestamp() (*bundle.RFC3161Timestamp, error) {
	return nil, nil
}

func TestVerifierTwoKeysSecondMatches(t *testing.T) {
	// Policy with 2 keys; stub succeeds only for the 2nd key's verifier.
	key1 := testPublicKeyPEM(t)
	key2 := testPublicKeyPEM(t)

	p := &Policy{
		RequireSignature: true,
		Keys:             []string{key1, key2},
	}

	verifier, err := NewCosignVerifier(p)
	if err != nil {
		t.Fatalf("NewCosignVerifier: %v", err)
	}

	cv, ok := verifier.(*cosignVerifier)
	if !ok {
		t.Fatalf("verifier is not *cosignVerifier: %T", verifier)
	}

	// Stub the verifyImage function to succeed only for the 2nd key.
	attemptCount := 0
	cv.verifyImage = func(ctx context.Context, ref name.Reference, co *cosign.CheckOpts) ([]oci.Signature, bool, error) {
		// Verify that SigVerifier is set (non-nil) for key mode.
		if co.SigVerifier == nil {
			t.Error("SigVerifier is nil, want non-nil verifier")
		}

		attemptCount++
		if attemptCount == 2 {
			// Succeed on the second key's verifier.
			return []oci.Signature{&mockSignature{}}, true, nil
		}
		// Return "no matching signatures" for the first key.
		return nil, false, fmt.Errorf("no matching signatures: test")
	}

	result, err := verifier.Verify(context.Background(), "registry.example.com/plugin@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.Verified {
		t.Error("Verified = false, want true")
	}
	if len(result.Signer) == 0 || result.Signer[:4] != "key:" {
		t.Errorf("Signer = %q, want key:<fingerprint>", result.Signer)
	}

	// Verify fingerprint is 16 hex characters
	if len(result.Signer) != 20 {
		t.Errorf("Signer length = %d, want 20 (key: + 16 hex chars)", len(result.Signer))
	}
}

func TestVerifierKeylessWithRegExp(t *testing.T) {
	// Keyless only; stub asserts identities are correct and succeeds.
	p := &Policy{
		RequireSignature: true,
		Keyless: []KeylessIdentity{{
			Issuer:        "https://token.actions.githubusercontent.com",
			SubjectRegExp: `^https://github\.com/ainsel-project/.+/\.github/workflows/release\.yml@refs/tags/v.+$`,
		}},
	}

	verifier, err := NewCosignVerifier(p)
	if err != nil {
		t.Fatalf("NewCosignVerifier: %v", err)
	}

	cv, ok := verifier.(*cosignVerifier)
	if !ok {
		t.Fatalf("verifier is not *cosignVerifier: %T", verifier)
	}

	// Stub to check that identities are passed correctly.
	cv.verifyImage = func(ctx context.Context, ref name.Reference, co *cosign.CheckOpts) ([]oci.Signature, bool, error) {
		if len(co.Identities) == 0 {
			t.Fatal("Identities is empty")
		}
		id := co.Identities[0]
		if id.Issuer != "https://token.actions.githubusercontent.com" {
			t.Errorf("Issuer = %q, want https://token.actions.githubusercontent.com", id.Issuer)
		}
		if id.SubjectRegExp != `^https://github\.com/ainsel-project/.+/\.github/workflows/release\.yml@refs/tags/v.+$` {
			t.Errorf("SubjectRegExp = %q", id.SubjectRegExp)
		}
		return []oci.Signature{&mockSignature{}}, true, nil
	}

	result, err := verifier.Verify(context.Background(), "registry.example.com/plugin@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.Verified {
		t.Error("Verified = false, want true")
	}
	expected := "keyless:https://token.actions.githubusercontent.com"
	if len(result.Signer) < len(expected) || result.Signer[:len(expected)] != expected {
		t.Errorf("Signer = %q, want keyless:https://token.actions.githubusercontent.com <subject>", result.Signer)
	}
}

func TestVerifierNoMatchingSignaturesError(t *testing.T) {
	// Stub returns cosign's no-matching-signatures error for every mode.
	key := testPublicKeyPEM(t)
	p := &Policy{
		RequireSignature: true,
		Keys:             []string{key},
		Keyless: []KeylessIdentity{{
			Issuer:        "https://token.actions.githubusercontent.com",
			SubjectRegExp: "^.*$",
		}},
	}

	verifier, err := NewCosignVerifier(p)
	if err != nil {
		t.Fatalf("NewCosignVerifier: %v", err)
	}

	cv, ok := verifier.(*cosignVerifier)
	if !ok {
		t.Fatalf("verifier is not *cosignVerifier: %T", verifier)
	}

	// Stub to always return no-matching-signatures error.
	cv.verifyImage = func(ctx context.Context, ref name.Reference, co *cosign.CheckOpts) ([]oci.Signature, bool, error) {
		return nil, false, fmt.Errorf("no matching signatures: test")
	}

	result, err := verifier.Verify(context.Background(), "registry.example.com/plugin@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1")
	if err != nil {
		t.Fatalf("Verify: %v, want nil error", err)
	}
	if result.Verified {
		t.Error("Verified = true, want false")
	}
}

func TestVerifierNetworkError(t *testing.T) {
	// Stub returns a network error.
	key := testPublicKeyPEM(t)
	p := &Policy{
		RequireSignature: true,
		Keys:             []string{key},
	}

	verifier, err := NewCosignVerifier(p)
	if err != nil {
		t.Fatalf("NewCosignVerifier: %v", err)
	}

	cv, ok := verifier.(*cosignVerifier)
	if !ok {
		t.Fatalf("verifier is not *cosignVerifier: %T", verifier)
	}

	// Stub to return a network error.
	cv.verifyImage = func(ctx context.Context, ref name.Reference, co *cosign.CheckOpts) ([]oci.Signature, bool, error) {
		return nil, false, fmt.Errorf("network error: connection refused")
	}

	result, err := verifier.Verify(context.Background(), "registry.example.com/plugin@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1")
	if err == nil {
		t.Fatal("Verify error = nil, want network error")
	}
	if result.Verified {
		t.Error("Verified = true, want false")
	}
}

func TestVerifierEmptyPolicy(t *testing.T) {
	// Empty policy should never call verifyImage.
	p := &Policy{}

	verifier, err := NewCosignVerifier(p)
	if err != nil {
		t.Fatalf("NewCosignVerifier: %v", err)
	}

	cv, ok := verifier.(*cosignVerifier)
	if !ok {
		t.Fatalf("verifier is not *cosignVerifier: %T", verifier)
	}

	// Stub that should never be called.
	stubCalled := false
	cv.verifyImage = func(ctx context.Context, ref name.Reference, co *cosign.CheckOpts) ([]oci.Signature, bool, error) {
		stubCalled = true
		return nil, false, fmt.Errorf("stub should not be called")
	}

	result, err := verifier.Verify(context.Background(), "registry.example.com/plugin@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1")
	if err != nil {
		t.Fatalf("Verify: %v, want nil error", err)
	}
	if result.Verified {
		t.Error("Verified = true, want false")
	}
	if stubCalled {
		t.Error("verifyImage was called, want it to not be called")
	}
}

func TestNewCosignVerifierTrustedRootFileMissing(t *testing.T) {
	// NewCosignVerifier should fail when TrustedRootFile is set to a non-existent path.
	p := &Policy{
		RequireSignature: true,
		TrustedRootFile:  "/nonexistent/trusted-root.json",
	}

	_, err := NewCosignVerifier(p)
	if err == nil {
		t.Fatal("NewCosignVerifier error = nil, want error for missing file")
	}
}
