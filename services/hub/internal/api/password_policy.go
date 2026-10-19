package api

import (
	"fmt"
	"strings"
)

// MaxPasswordLen is the hard password length cap. It is a DoS guard (hashing
// cost and request size), not a policy knob: making it configurable invites
// maxLength < minLength misconfigurations, so it stays fixed and the policy
// only exposes the minimum.
const MaxPasswordLen = 128

// defaultPasswordMinLen reproduces the historical hard-coded minimum so the
// zero-config default is byte-identical to the pre-policy behavior.
const defaultPasswordMinLen = 8

// Password character class names accepted by
// HUB_LOCAL_PASSWORD_CHARACTER_CLASSES and the chart value
// auth.local.passwordPolicy.requireCharacterClasses. Classes are defined
// ASCII-only on purpose: the hub check and the frontend hint can then never
// disagree on Unicode edge cases.
const (
	PasswordClassLower  = "lower"
	PasswordClassUpper  = "upper"
	PasswordClassDigit  = "digit"
	PasswordClassSymbol = "symbol"
)

// PasswordPolicy is the configurable password rule set enforced on all three
// password-setting paths (create, admin reset, own change). The zero value is
// not valid; use DefaultPasswordPolicy() or a Server's effective policy.
type PasswordPolicy struct {
	// MinLength is the minimum password length (1..MaxPasswordLen).
	// Values <= 0 are normalized to defaultPasswordMinLen by Validate.
	MinLength int
	// RequireClasses lists character-class names (PasswordClass*); a
	// password must contain at least one character of each listed class.
	// Empty means no class requirements.
	RequireClasses []string
}

// DefaultPasswordPolicy returns the policy that matches the historical
// behavior: minimum 8, maximum MaxPasswordLen, no character classes.
func DefaultPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{MinLength: defaultPasswordMinLen}
}

// ParsePasswordCharacterClasses parses a comma-separated class list (the
// HUB_LOCAL_PASSWORD_CHARACTER_CLASSES format). Empty entries are ignored,
// names are case-insensitive, duplicates are collapsed. Unknown names are an
// error so a typo fails the hub at startup instead of silently dropping a
// requirement.
func ParsePasswordCharacterClasses(v string) ([]string, error) {
	var out []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(v, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		switch part {
		case PasswordClassLower, PasswordClassUpper, PasswordClassDigit, PasswordClassSymbol:
		default:
			return nil, fmt.Errorf("unknown character class %q (want lower, upper, digit, or symbol)", part)
		}
		if !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	return out, nil
}

// classSatisfied reports whether p contains at least one character of the
// given class. symbol is any ASCII printable non-alphanumeric (0x20..0x7E).
func classSatisfied(class, p string) bool {
	for i := 0; i < len(p); i++ {
		c := p[i]
		isAlnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		switch class {
		case PasswordClassLower:
			if c >= 'a' && c <= 'z' {
				return true
			}
		case PasswordClassUpper:
			if c >= 'A' && c <= 'Z' {
				return true
			}
		case PasswordClassDigit:
			if c >= '0' && c <= '9' {
				return true
			}
		case PasswordClassSymbol:
			if c >= 0x20 && c <= 0x7E && !isAlnum {
				return true
			}
		}
	}
	return false
}

// classRequirementText renders a single class as a policy violation fragment.
func classRequirementText(class string) string {
	switch class {
	case PasswordClassLower:
		return "1 lowercase letter"
	case PasswordClassUpper:
		return "1 uppercase letter"
	case PasswordClassDigit:
		return "1 digit"
	case PasswordClassSymbol:
		return "1 symbol"
	}
	return "1 character of class " + class
}

// Validate reports every violated rule in one error, e.g.
// "password does not meet the password policy: at least 12 characters;
// 1 uppercase letter; 1 digit". Policy violations are surfaced at set-time to
// an authenticated actor (or an admin), so listing the rules is safe: the
// policy is not a secret.
func (p PasswordPolicy) Validate(password string) error {
	minLen := p.MinLength
	if minLen <= 0 {
		minLen = defaultPasswordMinLen
	}
	var violations []string
	if len(password) < minLen {
		violations = append(violations, fmt.Sprintf("at least %d characters", minLen))
	}
	if len(password) > MaxPasswordLen {
		violations = append(violations, fmt.Sprintf("at most %d characters", MaxPasswordLen))
	}
	for _, class := range p.RequireClasses {
		if !classSatisfied(class, password) {
			violations = append(violations, classRequirementText(class))
		}
	}
	if len(violations) > 0 {
		return fmt.Errorf("password does not meet the password policy: %s", strings.Join(violations, "; "))
	}
	return nil
}
