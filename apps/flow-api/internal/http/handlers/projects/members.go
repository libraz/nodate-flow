package projects

import (
	"context"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/projectkit"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// resolveWorkspaceMember resolves a user public id to the internal id of an
// enabled member of the workspace. A user of another workspace and an
// unknown id are both WS.MEMBER.NOT_FOUND, so the answer says nothing about
// accounts outside the caller's workspace.
func resolveWorkspaceMember(ctx context.Context, deps Deps, wsID uint32, pub types.PublicID) (uint32, error) {
	uid, err := deps.Queries.FindWorkspaceMemberUserInternalIdByPublicId(ctx, generated.FindWorkspaceMemberUserInternalIdByPublicIdParams{
		WorkspaceID: wsID,
		PublicID:    pub,
	})
	if err != nil {
		return 0, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsMemberNotFound, apierrors.InternalUnexpected))
	}
	return uid, nil
}

// ListMembers handles GET /projects/{prjId}/members.
func ListMembers(deps Deps) func(context.Context, *ListProjectMembersInput) (*ListProjectMembersOutput, error) {
	return func(ctx context.Context, in *ListProjectMembersInput) (*ListProjectMembersOutput, error) {
		prj, ok := middleware.ProjectFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := deps.Queries.ListProjectMembers(ctx, generated.ListProjectMembersParams{
			WorkspaceID: ws.ID,
			ProjectID:   prj.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		actorID, _ := middleware.ActorFromContext(ctx)
		emails, err := handlerutil.NewEmailVisibility(ctx, deps.Queries, ws.Role, actorID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListProjectMembersOutput{}
		out.Body.Members = make([]ProjectMember, 0, len(rows))
		for _, r := range rows {
			out.Body.Members = append(out.Body.Members, rowToProjectMember(r, emails))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// AddMember handles POST /projects/{prjId}/members.
func AddMember(deps Deps) func(context.Context, *AddProjectMemberInput) (*AddProjectMemberOutput, error) {
	return func(ctx context.Context, in *AddProjectMemberInput) (*AddProjectMemberOutput, error) {
		prj, ok := middleware.ProjectFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		userPub, err := types.Parse(in.Body.UserID)
		if err != nil {
			return nil, httpErr(apierrors.WsMemberNotFound)
		}
		// The user must already be a workspace member.
		uid, err := resolveWorkspaceMember(ctx, deps, ws.ID, userPub)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		memPub := types.New()
		role := generated.ProjectMembersRole(in.Body.Role)
		var added bool
		txErr := dbretry.InTx(ctx, deps.DB, "projects.AddMember", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			var err error
			added, err = projectkit.AddMember(ctx, tx, projectkit.MemberArgs{
				WorkspaceID: ws.ID,
				ProjectID:   prj.ID,
				UserID:      uid,
				Role:        role,
				PublicID:    memPub,
				AddedAt:     now,
			})
			return err
		})
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if !added {
			existing, err := deps.Queries.FindProjectMemberByUserId(ctx, generated.FindProjectMemberByUserIdParams{
				ProjectID: prj.ID,
				UserID:    uid,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			return &AddProjectMemberOutput{Body: ProjectMember{
				ID:        existing.PublicID.String(),
				UserID:    userPub.String(),
				Role:      string(existing.Role),
				AddedAt:   nullTimeUnix(existing.AddedAt),
				CreatedAt: existing.CreatedAt.Unix(),
			}}, nil
		}
		return &AddProjectMemberOutput{Body: ProjectMember{
			ID:        memPub.String(),
			UserID:    userPub.String(),
			Role:      string(role),
			AddedAt:   int64Ptr(now.Unix()),
			CreatedAt: now.Unix(),
		}}, nil
	}
}

// RemoveMember handles DELETE /projects/{prjId}/members/{userId}.
func RemoveMember(deps Deps) func(context.Context, *RemoveProjectMemberInput) (*RemoveProjectMemberOutput, error) {
	return func(ctx context.Context, in *RemoveProjectMemberInput) (*RemoveProjectMemberOutput, error) {
		prj, ok := middleware.ProjectFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		userPub, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.WsMemberNotFound)
		}
		uid, err := resolveWorkspaceMember(ctx, deps, ws.ID, userPub)
		if err != nil {
			return nil, err
		}
		if err := deps.Queries.RemoveProjectMemberByUserId(ctx, generated.RemoveProjectMemberByUserIdParams{
			ProjectID: prj.ID,
			UserID:    uid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &RemoveProjectMemberOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
