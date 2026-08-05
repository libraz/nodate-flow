package tasks

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// aclSpec maps an error returned by the acl package onto a response spec.
//
// The acl helpers report an access decision as an *APIError carrying the
// canonical code (project access denied, workspace access denied, bearer
// token bound to another workspace); only transport failures come back as
// raw database errors. Reading the attached spec first keeps those
// decisions on their own 403 instead of collapsing every one of them into
// INTERNAL.UNEXPECTED.
func aclSpec(err error, denied, internal *apierr.Spec) *apierr.Spec {
	var ae *apierrors.APIError
	if errors.As(err, &ae) && ae.Spec != nil {
		return ae.Spec
	}
	return apierr.SpecForErrNoRows(err, denied, internal)
}

func requireProjectEditor(ctx context.Context, db *sql.DB, workspaceID, projectID, actorID uint32) *apierr.Spec {
	wsRole, err := acl.CheckWorkspaceMember(ctx, db, workspaceID, actorID, apierrors.WsProjectAccessDenied)
	if err != nil {
		return aclSpec(err, apierrors.WsProjectAccessDenied, apierrors.InternalUnexpected)
	}
	prjRole, _, err := acl.LookupProjectMembership(ctx, db, workspaceID, projectID, actorID, wsRole)
	if err != nil {
		return aclSpec(err, apierrors.WsProjectAccessDenied, apierrors.InternalUnexpected)
	}
	if prjRole == acl.ProjectRoleElevated || prjRole.AtLeast(acl.ProjectRoleEditor) {
		return nil
	}
	return apierrors.WsProjectAccessDenied
}
