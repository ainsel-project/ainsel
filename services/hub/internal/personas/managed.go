package personas

import (
	"context"

	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
)

// callerIsManagerFromCtx reports whether the request caller carried on ctx is
// the named manager of a persona whose managedBy value is managedBy — the one
// exception to the managed-row refusal (issue #190). The rule itself lives in
// plugincreds.Caller.IsManagerOf, next to the identity it derives from, so the
// CRD-backed guard sites in the API layer and this DB-backed one cannot drift.
//
// Internal writers are unaffected by construction: pluginsync, the seed loader
// and the hub's copy-on-write persona paths run on contexts with no plugin
// caller, so the exception can only be reached through an authenticated plugin
// request.
func callerIsManagerFromCtx(ctx context.Context, managedBy string) bool {
	caller, ok := plugincreds.CallerFrom(ctx)
	return ok && caller.IsManagerOf(managedBy)
}

// managedRefusal reports whether this persona must refuse the write on ctx:
// it is externally managed and the caller is not the manager it names.
func managedRefusal(ctx context.Context, managedBy string) bool {
	return IsManaged(managedBy) && !callerIsManagerFromCtx(ctx, managedBy)
}
