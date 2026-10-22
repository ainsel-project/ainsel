package signing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testPublicKeyPEM returns a PEM-encoded PKIX public key, the shape an
// administrator puts in the policy file's `keys` list.
func testPublicKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// writePolicy writes policy YAML to a unique file inside a temp dir and
// returns its path.
func writePolicy(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return path
}

// indentBlock indents every line of s by n spaces so it can sit inside a YAML
// block scalar.
func indentBlock(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

func policyYAML(t *testing.T, requireSignature bool, keyPEM string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("requireSignature: " + boolString(requireSignature) + "\n")
	if keyPEM != "" {
		b.WriteString("keys:\n  - |\n")
		b.WriteString(indentBlock(keyPEM, 4) + "\n")
	}
	b.WriteString("keyless:\n")
	b.WriteString("  - issuer: https://token.actions.githubusercontent.com\n")
	b.WriteString("    subjectRegExp: ^https://github\\.com/ainsel-project/.+/\\.github/workflows/release\\.yml@refs/tags/v.+$\n")
	b.WriteString("trustedRootFile: \"\"\n")
	return b.String()
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestDefaultPolicyPath(t *testing.T) {
	// The chart mounts the policy ConfigMap here; the flag default and the
	// chart mount path are only consistent if this stays put.
	if DefaultPolicyPath != "/etc/ainsel/plugin-signing/policy.yaml" {
		t.Errorf("DefaultPolicyPath = %q, want %q", DefaultPolicyPath, "/etc/ainsel/plugin-signing/policy.yaml")
	}
}

func TestLoadPolicyMissingFileIsEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	p, err := LoadPolicy(missing)
	if err != nil {
		t.Fatalf("LoadPolicy(missing) error = %v, want nil", err)
	}
	if p == nil {
		t.Fatal("LoadPolicy(missing) = nil, want empty policy")
	}
	if p.RequireSignature {
		t.Error("empty policy RequireSignature = true, want false")
	}
	if len(p.Keys) != 0 || len(p.Keyless) != 0 {
		t.Errorf("empty policy has trust material: keys=%d keyless=%d, want 0/0", len(p.Keys), len(p.Keyless))
	}
	if p.HasTrust() {
		t.Error("empty policy HasTrust() = true, want false")
	}
}

func TestLoadPolicyRoundTrip(t *testing.T) {
	key := testPublicKeyPEM(t)
	path := writePolicy(t, policyYAML(t, true, key))

	p, err := LoadPolicy(path)
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if !p.RequireSignature {
		t.Error("RequireSignature = false, want true")
	}
	if len(p.Keys) != 1 {
		t.Fatalf("len(Keys) = %d, want 1", len(p.Keys))
	}
	if !strings.Contains(p.Keys[0], "BEGIN PUBLIC KEY") {
		t.Errorf("Keys[0] is not the PEM we wrote: %q", p.Keys[0])
	}
	if len(p.Keyless) != 1 {
		t.Fatalf("len(Keyless) = %d, want 1", len(p.Keyless))
	}
	want := KeylessIdentity{
		Issuer:        "https://token.actions.githubusercontent.com",
		SubjectRegExp: `^https://github\.com/ainsel-project/.+/\.github/workflows/release\.yml@refs/tags/v.+$`,
	}
	if p.Keyless[0] != want {
		t.Errorf("Keyless[0] = %+v, want %+v", p.Keyless[0], want)
	}
	if !p.HasTrust() {
		t.Error("HasTrust() = false, want true")
	}
}

func TestLoadPolicyErrors(t *testing.T) {
	t.Run("malformed yaml", func(t *testing.T) {
		path := writePolicy(t, "requireSignature: [this is not a bool\n")
		if _, err := LoadPolicy(path); err == nil {
			t.Fatal("LoadPolicy error = nil, want parse error")
		}
	})

	t.Run("invalid key names the index", func(t *testing.T) {
		path := writePolicy(t, "requireSignature: true\nkeys:\n  - not a pem block\n")
		_, err := LoadPolicy(path)
		if err == nil {
			t.Fatal("LoadPolicy error = nil, want validation error")
		}
		if !strings.Contains(err.Error(), "keys[0]") {
			t.Errorf("error %q does not name keys[0]", err)
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		// A typo must not silently load as "no trust configured".
		path := writePolicy(t, "requireSignature: true\nsigningKeys: []\n")
		if _, err := LoadPolicy(path); err == nil {
			t.Fatal("LoadPolicy error = nil, want unknown-field error")
		}
	})
}

func TestPolicyHash(t *testing.T) {
	key := testPublicKeyPEM(t)
	body := policyYAML(t, true, key)

	// Two files with identical content hash the same.
	a, err := LoadPolicy(writePolicy(t, body))
	if err != nil {
		t.Fatalf("LoadPolicy(a): %v", err)
	}
	b, err := LoadPolicy(writePolicy(t, body))
	if err != nil {
		t.Fatalf("LoadPolicy(b): %v", err)
	}
	if a.Hash() != b.Hash() {
		t.Errorf("identical policies hash differently: %s vs %s", a.Hash(), b.Hash())
	}
	if len(a.Hash()) != 64 {
		t.Errorf("Hash() = %q, want a 64-char sha256 hex digest", a.Hash())
	}

	// Dropping the key changes the hash.
	withoutKey, err := LoadPolicy(writePolicy(t, policyYAML(t, true, "")))
	if err != nil {
		t.Fatalf("LoadPolicy(withoutKey): %v", err)
	}
	if a.Hash() == withoutKey.Hash() {
		t.Error("hash unchanged after removing the key")
	}

	// Requiring a signature changes the hash.
	noRequire, err := LoadPolicy(writePolicy(t, policyYAML(t, false, key)))
	if err != nil {
		t.Fatalf("LoadPolicy(noRequire): %v", err)
	}
	if a.Hash() == noRequire.Hash() {
		t.Error("hash unchanged after flipping requireSignature")
	}
}

func TestPolicyHashIgnoresEmptyVsMissing(t *testing.T) {
	// A nil slice and an empty slice are the same policy, so the status hash
	// must not churn between an empty file and an absent one.
	if got, want := (&Policy{}).Hash(), (&Policy{Keys: []string{}, Keyless: []KeylessIdentity{}}).Hash(); got != want {
		t.Errorf("nil vs empty slices hash differently: %s vs %s", got, want)
	}
}

func TestPolicyValidate(t *testing.T) {
	validKey := testPublicKeyPEM(t)

	t.Run("valid", func(t *testing.T) {
		p := &Policy{
			RequireSignature: true,
			Keys:             []string{validKey},
			Keyless: []KeylessIdentity{{
				Issuer:  "https://token.actions.githubusercontent.com",
				Subject: "https://github.com/ainsel-project/ainsel/.github/workflows/release.yml@refs/tags/v1.2.3",
			}},
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil", err)
		}
	})

	t.Run("empty policy is valid", func(t *testing.T) {
		if err := (&Policy{}).Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil", err)
		}
	})

	t.Run("bad pem names the index", func(t *testing.T) {
		p := &Policy{RequireSignature: true, Keys: []string{"-----BEGIN PUBLIC KEY-----\nnot der\n-----END PUBLIC KEY-----\n"}}
		err := p.Validate()
		if err == nil {
			t.Fatal("Validate() = nil, want error")
		}
		if !strings.Contains(err.Error(), "keys[0]") {
			t.Errorf("error %q does not name keys[0]", err)
		}
	})

	t.Run("private key rejected", func(t *testing.T) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatalf("marshal private key: %v", err)
		}
		p := &Policy{Keys: []string{string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))}}
		if err := p.Validate(); err == nil {
			t.Fatal("Validate() = nil, want error for a private key")
		}
	})

	t.Run("keyless without issuer", func(t *testing.T) {
		p := &Policy{Keyless: []KeylessIdentity{{Subject: "someone"}}}
		err := p.Validate()
		if err == nil {
			t.Fatal("Validate() = nil, want error")
		}
		if !strings.Contains(err.Error(), "keyless[0]") {
			t.Errorf("error %q does not name keyless[0]", err)
		}
	})

	t.Run("keyless with both subject and regexp", func(t *testing.T) {
		p := &Policy{Keyless: []KeylessIdentity{{
			Issuer:        "https://token.actions.githubusercontent.com",
			Subject:       "someone",
			SubjectRegExp: "^someone$",
		}}}
		err := p.Validate()
		if err == nil {
			t.Fatal("Validate() = nil, want error")
		}
		if !strings.Contains(err.Error(), "keyless[0]") {
			t.Errorf("error %q does not name keyless[0]", err)
		}
		if !strings.Contains(err.Error(), "subject") || !strings.Contains(err.Error(), "subjectRegExp") {
			t.Errorf("error %q should name both subject and subjectRegExp", err)
		}
	})

	t.Run("keyless with neither subject nor regexp", func(t *testing.T) {
		p := &Policy{Keyless: []KeylessIdentity{{Issuer: "https://token.actions.githubusercontent.com"}}}
		if err := p.Validate(); err == nil {
			t.Fatal("Validate() = nil, want error")
		}
	})

	t.Run("invalid regexp", func(t *testing.T) {
		p := &Policy{Keyless: []KeylessIdentity{{
			Issuer:        "https://token.actions.githubusercontent.com",
			SubjectRegExp: "^(unclosed",
		}}}
		err := p.Validate()
		if err == nil {
			t.Fatal("Validate() = nil, want error")
		}
		if !strings.Contains(err.Error(), "keyless[0]") || !strings.Contains(err.Error(), "subjectRegExp") {
			t.Errorf("error %q should name keyless[0] and subjectRegExp", err)
		}
	})

	t.Run("all problems are reported", func(t *testing.T) {
		p := &Policy{
			Keys:    []string{"nope", "also nope"},
			Keyless: []KeylessIdentity{{Subject: "x"}, {Issuer: "y"}},
		}
		err := p.Validate()
		if err == nil {
			t.Fatal("Validate() = nil, want error")
		}
		for _, want := range []string{"keys[0]", "keys[1]", "keyless[0]", "keyless[1]"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %s", err, want)
			}
		}
	})
}

func TestPolicyHasTrust(t *testing.T) {
	if (&Policy{}).HasTrust() {
		t.Error("empty policy HasTrust() = true, want false")
	}
	if !(&Policy{Keys: []string{"x"}}).HasTrust() {
		t.Error("policy with a key HasTrust() = false, want true")
	}
	if !(&Policy{Keyless: []KeylessIdentity{{Issuer: "i", Subject: "s"}}}).HasTrust() {
		t.Error("policy with a keyless identity HasTrust() = false, want true")
	}
}
