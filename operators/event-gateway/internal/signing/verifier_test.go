package signing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/sigstore/cosign/v3/pkg/cosign"
	"github.com/sigstore/cosign/v3/pkg/cosign/bundle"
	"github.com/sigstore/cosign/v3/pkg/oci"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
)

// testCertWithIssuerAndSubject creates a self-signed certificate with Fulcio issuer extension and URI SAN.
// Fulcio issuer OID is 1.3.6.1.4.1.57264.1.1.
func testCertWithIssuerAndSubject(t *testing.T, issuer, subject string) *x509.Certificate {
	t.Helper()

	// Generate a key pair for the cert.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	// Create a certificate template.
	notBefore := time.Now()
	notAfter := notBefore.Add(time.Hour)

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test",
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageCodeSigning,
		},
	}

	// Add URI SAN.
	subjectURL, err := url.Parse(subject)
	if err != nil {
		t.Fatalf("parse subject URL: %v", err)
	}
	cert.URIs = []*url.URL{subjectURL}

	// Add Fulcio issuer extension (OID 1.3.6.1.4.1.57264.1.1).
	// The extension value is stored as raw UTF-8 bytes and recovered as a string.
	fulcioIssuerOID := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
	issuerExt := pkix.Extension{
		Id:       fulcioIssuerOID,
		Critical: false,
		Value:    []byte(issuer),
	}
	cert.ExtraExtensions = []pkix.Extension{issuerExt}

	// Self-sign the certificate.
	certDER, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	// Parse it back to get a proper *x509.Certificate.
	parsed, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	return parsed
}

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

	// Compute expected fingerprint for key2 independently.
	// We need to parse the PEM key and marshal it back to compute the fingerprint.
	pub2, err := cryptoutils.UnmarshalPEMToPublicKey([]byte(key2))
	if err != nil {
		t.Fatalf("unmarshal key2: %v", err)
	}
	der2, err := x509.MarshalPKIXPublicKey(pub2)
	if err != nil {
		t.Fatalf("marshal key2: %v", err)
	}
	sum2 := sha256.Sum256(der2)
	expectedFingerprint := hex.EncodeToString(sum2[:])[:16]
	expectedSigner := "key:" + expectedFingerprint

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
	// Note: verifyWithKey calls verifyImage twice per key (legacy and new bundle),
	// so we need to count calls > 2 to skip both attempts of the first key.
	attemptCount := 0
	cv.verifyImage = func(ctx context.Context, ref name.Reference, co *cosign.CheckOpts) ([]oci.Signature, bool, error) {
		// Verify that SigVerifier is set (non-nil) for key mode.
		if co.SigVerifier == nil {
			t.Error("SigVerifier is nil, want non-nil verifier")
		}

		attemptCount++
		if attemptCount > 2 {
			// Succeed on the first call for the second key (call #3).
			return []oci.Signature{&mockSignature{}}, true, nil
		}
		// Return "no matching signatures" for the first key's attempts.
		return nil, false, fmt.Errorf("no matching signatures: test")
	}

	result, err := verifier.Verify(context.Background(), "registry.example.com/plugin@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.Verified {
		t.Error("Verified = false, want true")
	}

	// Verify fingerprint is exactly correct
	if result.Signer != expectedSigner {
		t.Errorf("Signer = %q, want %q", result.Signer, expectedSigner)
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

	// Create a certificate with the matching issuer and subject.
	// Note: testCertWithIssuerAndSubject already returns the parsed cert.
	// We need to get the DER bytes for it.
	cert := testCertWithIssuerAndSubject(t, "https://token.actions.githubusercontent.com", "https://github.com/ainsel-project/test/.github/workflows/release.yml@refs/tags/v1.0.0")
	// The cert is already parsed, so we don't have the original DER bytes.
	// Instead, we'll just use the cert directly in mockSignature.
	certDER := cert.Raw // Use the Raw field which contains the DER bytes.

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
		// Return signature with certificate.
		return []oci.Signature{&mockSignature{cert: certDER}}, true, nil
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
	// Verify the subject is included.
	if !contains(result.Signer, "https://github.com") {
		t.Errorf("Signer = %q, want to contain subject", result.Signer)
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

	// Stub to always return error with "no matching signatures" that will match our isNoSignaturesError check.
	cv.verifyImage = func(ctx context.Context, ref name.Reference, co *cosign.CheckOpts) ([]oci.Signature, bool, error) {
		// Return an error that contains "no matching signatures" which our error detection handles.
		return nil, false, fmt.Errorf("no matching signatures: verification failed")
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

func TestVerifierKeylessMultipleIdentities(t *testing.T) {
	// Policy with 2 keyless identities; the stub returns a signature whose cert
	// has the SECOND identity's issuer/subject → Signer should name the second.
	p := &Policy{
		RequireSignature: true,
		Keyless: []KeylessIdentity{
			{
				Issuer:        "https://issuer1.example.com",
				SubjectRegExp: "^.*$",
			},
			{
				Issuer:        "https://issuer2.example.com",
				SubjectRegExp: "^.*$",
			},
		},
	}

	verifier, err := NewCosignVerifier(p)
	if err != nil {
		t.Fatalf("NewCosignVerifier: %v", err)
	}

	cv, ok := verifier.(*cosignVerifier)
	if !ok {
		t.Fatalf("verifier is not *cosignVerifier: %T", verifier)
	}

	// Create a certificate with the SECOND issuer (not the first).
	cert := testCertWithIssuerAndSubject(t, "https://issuer2.example.com", "https://github.com/test/workflow")
	certDER := cert.Raw // Use the DER bytes from the parsed certificate.

	cv.verifyImage = func(ctx context.Context, ref name.Reference, co *cosign.CheckOpts) ([]oci.Signature, bool, error) {
		// Verify both identities were passed to cosign (showing we tried both).
		if len(co.Identities) != 2 {
			t.Fatalf("Expected 2 identities, got %d", len(co.Identities))
		}
		if co.Identities[0].Issuer != "https://issuer1.example.com" {
			t.Errorf("Identities[0].Issuer = %q, want https://issuer1.example.com", co.Identities[0].Issuer)
		}
		if co.Identities[1].Issuer != "https://issuer2.example.com" {
			t.Errorf("Identities[1].Issuer = %q, want https://issuer2.example.com", co.Identities[1].Issuer)
		}
		// Return signature with cert from SECOND issuer.
		return []oci.Signature{&mockSignature{cert: certDER}}, true, nil
	}

	result, err := verifier.Verify(context.Background(), "registry.example.com/plugin@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.Verified {
		t.Error("Verified = false, want true")
	}

	// Signer should use the SECOND issuer from the cert, not the first from policy[0].
	expectedPrefix := "keyless:https://issuer2.example.com"
	if len(result.Signer) < len(expectedPrefix) || result.Signer[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("Signer = %q, want to start with %q", result.Signer, expectedPrefix)
	}
	// Should also contain the subject from cert
	if !contains(result.Signer, "https://github.com/test/workflow") {
		t.Errorf("Signer = %q, want to contain subject https://github.com/test/workflow", result.Signer)
	}
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
