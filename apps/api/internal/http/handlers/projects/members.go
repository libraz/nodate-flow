package projects

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

func resolveUserInternalID(ctx context.Context, db *sql.DB, pub types.PublicID) (uint32, error) {
	const q = `SELECT id FROM users WHERE public_id = ? LIMIT 1`
	var uid uint32
	if err := db.QueryRowContext(ctx, q, pub).Scan(&uid); err != nil {
		return 0, err
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
		uid, err := resolveUserInternalID(ctx, deps.DB, userPub)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsMemberNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		// User must already be a workspace member.
		if _, err := deps.Queries.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
			WorkspaceID: ws.ID,
			UserID:      uid,
		}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsMemberNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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
				AddedAt:   nullTime(existing.AddedAt),
				CreatedAt: existing.CreatedAt,
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
			AddedAt:   timePtr(now),
			CreatedAt: now,
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
		userPub, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.WsMemberNotFound)
		}
		uid, err := resolveUserInternalID(ctx, deps.DB, userPub)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsMemberNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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
