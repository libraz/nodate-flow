package projects

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
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
		out := &ListProjectMembersOutput{}
		out.Body.Members = make([]ProjectMember, 0, len(rows))
		for _, r := range rows {
			out.Body.Members = append(out.Body.Members, rowToProjectMember(r))
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
		// Idempotency.
		if existing, err := deps.Queries.FindProjectMemberByUserId(ctx, generated.FindProjectMemberByUserIdParams{
			ProjectID: prj.ID,
			UserID:    uid,
		}); err == nil {
			return &AddProjectMemberOutput{Body: ProjectMember{
				ID:        existing.PublicID.String(),
				UserID:    userPub.String(),
				Role:      string(existing.Role),
				AddedAt:   nullTimeUnix(existing.AddedAt),
				CreatedAt: existing.CreatedAt.Unix(),
			}}, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		now := time.Now()
		memPub := types.New()
		role := generated.ProjectMembersRole(in.Body.Role)
		if _, err := deps.Queries.AddProjectMember(ctx, generated.AddProjectMemberParams{
			PublicID:    memPub,
			WorkspaceID: ws.ID,
			ProjectID:   prj.ID,
			UserID:      uid,
			Role:        role,
			AddedAt:     sql.NullTime{Time: now, Valid: true},
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
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
