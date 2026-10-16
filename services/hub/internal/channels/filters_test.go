package channels

import (
	"strings"
	"testing"

	ainselapishared "github.com/ainsel-project/ainsel/shared/api"
)

// The gate is only as good as the shapes it accepts: Filter.Match returns false
// for any op it does not implement, so a typo'd op is not a harmless oddity —
// it makes the whole bridge deliver nothing, silently, behind a 201. These
// tests pin the write-time validation that keeps that failure from shipping.
func TestValidateFilterGroups(t *testing.T) {
	cases := []struct {
		name    string
		groups  [][]ainselapishared.Filter
		wantErr string // empty means valid
	}{
		{name: "nil gate is unconditional", groups: nil},
		{name: "empty gate is unconditional", groups: [][]ainselapishared.Filter{}},
		{
			name:    "empty group",
			groups:  [][]ainselapishared.Filter{{}},
			wantErr: "group 0 is empty",
		},
		{
			name:    "missing field",
			groups:  [][]ainselapishared.Filter{{{Field: " ", Op: "eq", Value: "push"}}},
			wantErr: "needs field and op",
		},
		{
			name:    "missing op",
			groups:  [][]ainselapishared.Filter{{{Field: "type"}}},
			wantErr: "needs field and op",
		},
		{
			name:    "unknown op",
			groups:  [][]ainselapishared.Filter{{{Field: "type", Op: "equals", Value: "push"}}},
			wantErr: `unsupported op "equals"`,
		},
		{
			name:    "in without values",
			groups:  [][]ainselapishared.Filter{{{Field: "type", Op: "in"}}},
			wantErr: `non-empty values list for op "in"`,
		},
		{
			name:    "not-in with empty values",
			groups:  [][]ainselapishared.Filter{{{Field: "sender", Op: "not-in", Values: []string{}}}},
			wantErr: `non-empty values list for op "not-in"`,
		},
		{
			name:   "eq with empty value is valid",
			groups: [][]ainselapishared.Filter{{{Field: "deleted", Op: "eq"}}},
		},
		{
			name: "every implemented op is accepted",
			groups: [][]ainselapishared.Filter{{
				{Field: "a", Op: "eq", Value: "x"},
				{Field: "a", Op: "neq", Value: "x"},
				{Field: "a", Op: "prefix", Value: "x"},
				{Field: "a", Op: "suffix", Value: "x"},
				{Field: "a", Op: "contains", Value: "x"},
				{Field: "a", Op: "not-contains", Value: "x"},
				{Field: "a", Op: "contains not", Value: "x"},
				{Field: "a", Op: "in", Values: []string{"x"}},
				{Field: "a", Op: "not-in", Values: []string{"x"}},
				{Field: "a", Op: "not in", Values: []string{"x"}},
				{Field: "a", Op: "regex", Value: "^x$"},
			}},
		},
		{
			name: "valid multi-group gate",
			groups: [][]ainselapishared.Filter{
				{{Field: "type", Op: "in", Values: []string{"push"}}},
				{
					{Field: "type", Op: "eq", Value: "issue_comment"},
					{Field: "comment.body", Op: "contains", Value: "@dev-agent"},
				},
			},
		},
		{
			name: "error names the offending position",
			groups: [][]ainselapishared.Filter{
				{{Field: "type", Op: "eq", Value: "push"}},
				{{Field: "type", Op: "eq", Value: "x"}, {Field: "type", Op: "match", Value: "y"}},
			},
			wantErr: "group 1, filter 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFilterGroups(tc.groups)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
