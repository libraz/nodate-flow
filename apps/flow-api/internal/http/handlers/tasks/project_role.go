package tasks

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/acl"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
)

func requireProjectEditor(ctx context.Context, db *sql.DB, workspaceID, projectID, actorID uint32) *apierr.Spec {
	wsRole, err := acl.CheckWorkspaceMember(ctx, db, workspaceID, actorID, apierrors.WsProjectAccessDenied)
	if err != nil {
		return apierr.SpecForErrNoRows(err, apierrors.WsProjectAccessDenied, apierrors.InternalUnexpected)
	}
	prjRole, _, err := acl.LookupProjectMembership(ctx, db, workspaceID, projectID, actorID, wsRole)
	if err != nil {
		return apierr.SpecForErrNoRows(err, apierrors.WsProjectAccessDenied, apierrors.InternalUnexpected)
	}
	if prjRole == acl.ProjectRoleElevated || prjRole.AtLeast(acl.ProjectRoleEditor) {
		return nil
	}
	return apierrors.WsProjectAccessDenied
}
