package channels

import (
	"fmt"
	"strings"

	ainselapishared "github.com/ainsel-project/ainsel/shared/api"
)

// validFilterOps is the set of operators ainselapishared.Filter.Match
// implements. Match returns false for any op it does not recognise, so an op
// outside this set is not merely unusual — it makes the filter never match,
// which on a bridge means the whole edge goes dark. Validation is defined
// here, next to the matcher it mirrors, so every write path (the HTTP API and
// the MCP tool) rejects the same shapes for the same reason.
var validFilterOps = map[string]bool{
	"eq": true, "neq": true,
	"prefix": true, "suffix": true,
	"contains": true, "not-contains": true, "contains not": true,
	"in": true, "not-in": true, "not in": true,
	"regex": true,
}

// listFilterOps are the operators that compare against Values rather than
// Value. With no values they can never match ("in") or always match
// ("not-in"), so an empty list is treated as a typo.
var listFilterOps = map[string]bool{
	"in": true, "not-in": true, "not in": true,
}

// ValidateFilterGroups checks a bridge gate — a disjunction of filter groups —
// for the shapes that would silently never match at transfer time: an empty
// group, a missing field or op, an op Filter.Match does not implement, and a
// list op with no values. A nil or empty gate is valid: it means the bridge
// stays unconditional. The error names the offending group and filter so the
// caller can report exactly which entry to fix.
func ValidateFilterGroups(groups [][]ainselapishared.Filter) error {
	for gi, group := range groups {
		if len(group) == 0 {
			return fmt.Errorf("filters group %d is empty", gi)
		}
		for fi, f := range group {
			if strings.TrimSpace(f.Field) == "" || strings.TrimSpace(f.Op) == "" {
				return fmt.Errorf("filters group %d, filter %d needs field and op", gi, fi)
			}
			if !validFilterOps[f.Op] {
				return fmt.Errorf("filters group %d, filter %d has unsupported op %q (supported: eq, neq, prefix, suffix, contains, not-contains, in, not-in, regex)", gi, fi, f.Op)
			}
			if listFilterOps[f.Op] && len(f.Values) == 0 {
				return fmt.Errorf("filters group %d, filter %d needs a non-empty values list for op %q", gi, fi, f.Op)
			}
		}
	}
	return nil
}
