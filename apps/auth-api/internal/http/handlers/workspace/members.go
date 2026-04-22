package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// resolveUserInternalID looks up the internal numeric users.id for the
// given public UUID.
func resolveUserInternalID(ctx context.Context, q *generated.Queries, pub types.PublicID) (uint32, error) {
	return q.FindUserInternalIdByPublicId(ctx, pub)
}

// ListMembers handles GET /workspaces/{wsId}/members.
func ListMembers(deps Deps) func(context.Context, *ListWorkspaceMembersInput) (*ListWorkspaceMembersOutput, error) {
	return func(ctx context.Context, in *ListWorkspaceMembersInput) (*ListWorkspaceMembersOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := deps.Queries.ListWorkspaceMembers(ctx, generated.ListWorkspaceMembersParams{
			WorkspaceID: ws.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListWorkspaceMembersOutput{}
		out.Body.Members = make([]WorkspaceMember, 0, len(rows))
		for _, r := range rows {
			out.Body.Members = append(out.Body.Members, rowToMember(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// InviteMember handles POST /workspaces/{wsId}/members. If the email is
// not yet registered, a stub user is created so the invite can land.
func InviteMember(deps Deps) func(context.Context, *AddWorkspaceMemberInput) (*AddWorkspaceMemberOutput, error) {
	return func(ctx context.Context, in *AddWorkspaceMemberInput) (*AddWorkspaceMemberOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		emailAddr := strings.ToLower(strings.TrimSpace(in.Body.Email))
		role := generated.WorkspaceMembersRole(in.Body.Role)

		var userID uint32
		var userPub types.PublicID
		var displayName string

		existing, err := deps.Queries.FindUserByEmailIncludingDisabled(ctx, emailAddr)
		switch {
		case err == nil:
			userID = existing.ID
			userPub = existing.PublicID
			displayName = existing.DisplayName
		case errors.Is(err, sql.ErrNoRows):
			userPub = types.New()
			id, ierr := deps.Queries.CreateStubUser(ctx, generated.CreateStubUserParams{
				PublicID:        userPub,
				Email:           emailAddr,
				DisplayName:     emailAddr,
				Locale:          "en",
				Timezone:        "UTC",
				Country:         sql.NullString{},
				ThemePreference: generated.UsersThemePreferenceSystem,
			})
			if ierr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			userID = uint32(id)
			displayName = emailAddr
		default:
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Idempotency: if already a member, return that record.
		if existingMem, merr := deps.Queries.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
			WorkspaceID: ws.ID,
			UserID:      userID,
		}); merr == nil {
			return &AddWorkspaceMemberOutput{Body: WorkspaceMember{
				ID:          existingMem.PublicID.String(),
				UserID:      userPub.String(),
				Email:       emailAddr,
				DisplayName: displayName,
				Role:        string(existingMem.Role),
				InvitedAt:   nullTimeUnix(existingMem.InvitedAt),
				JoinedAt:    nullTimeUnix(existingMem.JoinedAt),
				CreatedAt:   existingMem.CreatedAt.Unix(),
			}}, nil
		} else if !errors.Is(merr, sql.ErrNoRows) {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		now := time.Now()
		memPub := types.New()
		if _, err := deps.Queries.CreateWorkspaceMember(ctx, generated.CreateWorkspaceMemberParams{
			PublicID:        memPub,
			WorkspaceID:     ws.ID,
			UserID:          userID,
			Role:            role,
			InvitedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},
			InvitedAt:       sql.NullTime{Time: now, Valid: true},
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &AddWorkspaceMemberOutput{Body: WorkspaceMember{
			ID:          memPub.String(),
			UserID:      userPub.String(),
			Email:       emailAddr,
			DisplayName: displayName,
			Role:        string(role),
			InvitedAt:   int64Ptr(now.Unix()),
			CreatedAt:   now.Unix(),
		}}, nil
	}
}

// UpdateMemberRole handles PATCH /workspaces/{wsId}/members/{userId}.
func UpdateMemberRole(deps Deps) func(context.Context, *UpdateWorkspaceMemberRoleInput) (*UpdateWorkspaceMemberRoleOutput, error) {
	return func(ctx context.Context, in *UpdateWorkspaceMemberRoleInput) (*UpdateWorkspaceMemberRoleOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		userPub, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.WsMemberNotFound)
		}
		uid, err := resolveUserInternalID(ctx, deps.Queries, userPub)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsMemberNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		mem, err := deps.Queries.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
			WorkspaceID: ws.ID,
			UserID:      uid,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsMemberNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		role := generated.WorkspaceMembersRole(in.Body.Role)
		if err := deps.Queries.UpdateMemberRoleByUserId(ctx, generated.UpdateMemberRoleByUserIdParams{
			Role:        role,
			WorkspaceID: ws.ID,
			UserID:      uid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &UpdateWorkspaceMemberRoleOutput{Body: WorkspaceMember{
			ID:        mem.PublicID.String(),
			UserID:    userPub.String(),
			Role:      string(role),
			InvitedAt: nullTimeUnix(mem.InvitedAt),
			JoinedAt:  nullTimeUnix(mem.JoinedAt),
			CreatedAt: mem.CreatedAt.Unix(),
		}}, nil
	}
}

// RemoveMember handles DELETE /workspaces/{wsId}/members/{userId}.
func RemoveMember(deps Deps) func(context.Context, *RemoveWorkspaceMemberInput) (*RemoveWorkspaceMemberOutput, error) {
	return func(ctx context.Context, in *RemoveWorkspaceMemberInput) (*RemoveWorkspaceMemberOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		userPub, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.WsMemberNotFound)
		}
		uid, err := resolveUserInternalID(ctx, deps.Queries, userPub)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsMemberNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err := deps.Queries.RemoveWorkspaceMemberByUserId(ctx, generated.RemoveWorkspaceMemberByUserIdParams{
			WorkspaceID: ws.ID,
			UserID:      uid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &RemoveWorkspaceMemberOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
