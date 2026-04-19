package resolve

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// WorkspaceMember validates that the given actor is an enabled member of the
// workspace identified by wsPublic (a UUID v7 string). It returns the internal
// workspace ID or a huma error suitable for returning from a handler.
func WorkspaceMember(ctx context.Context, db *sql.DB, wsPublic string, actorID uint32) (uint32, error) {
	if wsPublic == "" {
		return 0, httpErr(apierrors.WsWorkspaceNotFound)
	}
	pub, err := types.Parse(wsPublic)
	if err != nil {
		return 0, httpErr(apierrors.WsWorkspaceNotFound)
	}
	const wsLookup = `SELECT id FROM workspaces WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	var wsID uint32
	if err := db.QueryRowContext(ctx, wsLookup, pub).Scan(&wsID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.WsWorkspaceNotFound)
		}
		return 0, httpErr(apierrors.InternalUnexpected)
	}
	if err := handlerutil.CheckWorkspaceMember(ctx, db, wsID, actorID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		return 0, httpErr(apierrors.InternalUnexpected)
	}
	return wsID, nil
}
