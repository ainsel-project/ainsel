package skills

import (
	"context"

	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
)

// callerIsManagerFromCtx reports whether the request caller carried on ctx is
// the named manager of a row whose managedBy value is managedBy — the one
// exception to the managed-row refusal (issue #190). The rule itself lives in
// plugincreds.Caller.IsManagerOf, next to the identity it derives from, so the
// CRD-backed guard sites in the API layer and this DB-backed one cannot drift.
//
// The bare legacy "plugin" marker never matches: its owner is encoded in the
// row's tags, not in the value, and those rows stay pluginsync-owned — so the
// exact 409 wording #189 preserved for them is untouched.
//
// Internal writers are unaffected by construction: pluginsync and the
// reconciler go through UpsertManaged/DeleteManaged, which never consult this
// check, and hub-internal calls run on contexts without a caller.
func callerIsManagerFromCtx(ctx context.Context, managedBy string) bool {
	caller, ok := plugincreds.CallerFrom(ctx)
	return ok && caller.IsManagerOf(managedBy)
}

// RefusesWrite reports whether this row must refuse the write on ctx: it is
// externally managed and the caller is not the manager the value names.
func RefusesWrite(ctx context.Context, row *Skill) bool {
	return IsManaged(row.ManagedBy) && !callerIsManagerFromCtx(ctx, row.ManagedBy)
}
