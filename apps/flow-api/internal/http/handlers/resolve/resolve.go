package resolve

import (
	"context"
	"database/sql"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
)

// httpErr converts an apierrors.Spec into a huma error. This duplicates the
// per-package helper intentionally so that the resolve package stays
// self-contained.
func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

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
	const wsMemQuery = `SELECT 1 FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	var one int
	if err := db.QueryRowContext(ctx, wsMemQuery, wsID, actorID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		return 0, httpErr(apierrors.InternalUnexpected)
	}
	return wsID, nil
}
