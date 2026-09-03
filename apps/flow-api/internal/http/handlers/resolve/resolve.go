package resolve

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
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
		return 0, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceNotFound, apierrors.InternalUnexpected))
	}
	if err := handlerutil.CheckWorkspaceMember(ctx, db, wsID, actorID); err != nil {
		return 0, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceAccessDenied, apierrors.InternalUnexpected))
	}
	return wsID, nil
}

// WorkspaceMemberForWrite is [WorkspaceMember] with the workspace write floor
// applied: guests, the read-only workspace role, are refused.
//
// It exists for the handlers whose workspace arrives in the request rather
// than in the path. Routes carrying {wsId} get the same floor from the chi
// group they are mounted on; a route that resolves its own workspace has no
// group to hang one on, and the floor has to be asked for here or not at all.
//
// The refusal matches the group floor's, so the same caller writing to the
// same workspace gets one answer whichever route carried them there.
func WorkspaceMemberForWrite(ctx context.Context, db *sql.DB, wsPublic string, actorID uint32) (uint32, error) {
	wsID, err := WorkspaceMember(ctx, db, wsPublic, actorID)
	if err != nil {
		return 0, err
	}
	role, err := acl.CheckWorkspaceMember(ctx, db, wsID, actorID, apierrors.WsWorkspaceAccessDenied)
	if err != nil {
		return 0, err
	}
	if !role.AtLeast(acl.WorkspaceRoleMember) {
		return 0, httpErr(apierrors.WsMemberRoleDenied)
	}
	return wsID, nil
}
