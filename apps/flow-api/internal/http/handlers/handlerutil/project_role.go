package handlerutil

import (
	"context"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// RequireProjectRole is the project role floor for writes that name their
// project in the body or reach it through a resource, where no path
// parameter exists to hang middleware.RequireProjectRole on.
//
// The chain is the one the middleware runs: enabled workspace membership,
// then the project_members role, with workspace owners and admins passing
// as elevated. Every denial is WS.PROJECT.ACCESS_DENIED; only transport
// failures come back as INTERNAL.UNEXPECTED. db may be a transaction, so a
// background job can re-apply the floor against its initiator.
func RequireProjectRole(ctx context.Context, db acl.DB, workspaceID, projectID, actorID uint32, minRole acl.ProjectRole) *apierr.Spec {
	wsRole, err := acl.CheckWorkspaceMember(ctx, db, workspaceID, actorID, apierrors.WsProjectAccessDenied)
	if err != nil {
		return aclSpec(err)
	}
	prjRole, _, err := acl.LookupProjectMembership(ctx, db, workspaceID, projectID, actorID, wsRole)
	if err != nil {
		return aclSpec(err)
	}
	if prjRole == acl.ProjectRoleElevated || prjRole.AtLeast(minRole) {
		return nil
	}
	return apierrors.WsProjectAccessDenied
}

// aclSpec keeps an access decision carried by an acl error on its own spec
// instead of collapsing it into INTERNAL.UNEXPECTED.
func aclSpec(err error) *apierr.Spec {
	var ae *apierrors.APIError
	if errors.As(err, &ae) && ae.Spec != nil {
		return ae.Spec
	}
	return apierr.SpecForErrNoRows(err, apierrors.WsProjectAccessDenied, apierrors.InternalUnexpected)
}
