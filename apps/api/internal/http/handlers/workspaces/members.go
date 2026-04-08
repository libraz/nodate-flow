package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// resolveUserInternalID looks up the internal numeric users.id for the
// given public UUID. It is used by the membership handlers because the
// generated FindUserByPublicId query targets v_users which does not expose
// the internal id column.
func resolveUserInternalID(ctx context.Context, db *sql.DB, pub types.PublicID) (uint32, error) {
	const q = `SELECT id FROM users WHERE public_id = ? LIMIT 1`
	var uid uint32
	if err := db.QueryRowContext(ctx, q, pub).Scan(&uid); err != nil {
		return 0, err
	}
	return uid, nil
}

// ListMembers handles GET /workspaces/{wsId}/members.
func ListMembers(deps Deps) func(context.Context, *ListMembersInput) (*ListMembersOutput, error) {
	return func(ctx context.Context, in *ListMembersInput) (*ListMembersOutput, error) {
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
		out := &ListMembersOutput{}
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
func InviteMember(deps Deps) func(context.Context, *InviteMemberInput) (*InviteMemberOutput, error) {
	return func(ctx context.Context, in *InviteMemberInput) (*InviteMemberOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		email := strings.ToLower(strings.TrimSpace(in.Body.Email))
		role := generated.WorkspaceMembersRole(in.Body.Role)

		var userID uint32
		var userPub types.PublicID
		var displayName string

		existing, err := deps.Queries.FindUserByEmailIncludingDisabled(ctx, email)
		switch {
		case err == nil:
			userID = existing.ID
			userPub = existing.PublicID
			displayName = existing.DisplayName
		case errors.Is(err, sql.ErrNoRows):
			userPub = types.New()
			id, ierr := deps.Queries.CreateStubUser(ctx, generated.CreateStubUserParams{
				PublicID:        userPub,
				Email:           email,
				DisplayName:     email,
				Locale:          "en",
				ThemePreference: generated.UsersThemePreferenceSystem,
			})
			if ierr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			userID = uint32(id)
			displayName = email
		default:
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Idempotency: if already a member, return that record.
		if existingMem, merr := deps.Queries.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
			WorkspaceID: ws.ID,
			UserID:      userID,
		}); merr == nil {
			return &InviteMemberOutput{Body: WorkspaceMember{
				ID:          existingMem.PublicID.String(),
				UserID:      userPub.String(),
				Email:       email,
				DisplayName: displayName,
				Role:        string(existingMem.Role),
				InvitedAt:   nullTime(existingMem.InvitedAt),
				JoinedAt:    nullTime(existingMem.JoinedAt),
				CreatedAt:   existingMem.CreatedAt,
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

		return &InviteMemberOutput{Body: WorkspaceMember{
			ID:          memPub.String(),
			UserID:      userPub.String(),
			Email:       email,
			DisplayName: displayName,
			Role:        string(role),
			InvitedAt:   now,
			CreatedAt:   now,
		}}, nil
	}
}

// UpdateMemberRole handles PATCH /workspaces/{wsId}/members/{userId}.
func UpdateMemberRole(deps Deps) func(context.Context, *UpdateMemberRoleInput) (*UpdateMemberRoleOutput, error) {
	return func(ctx context.Context, in *UpdateMemberRoleInput) (*UpdateMemberRoleOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
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
		return &UpdateMemberRoleOutput{Body: WorkspaceMember{
			ID:        mem.PublicID.String(),
			UserID:    userPub.String(),
			Role:      string(role),
			InvitedAt: nullTime(mem.InvitedAt),
			JoinedAt:  nullTime(mem.JoinedAt),
			CreatedAt: mem.CreatedAt,
		}}, nil
	}
}

// RemoveMember handles DELETE /workspaces/{wsId}/members/{userId}.
func RemoveMember(deps Deps) func(context.Context, *RemoveMemberInput) (*RemoveMemberOutput, error) {
	return func(ctx context.Context, in *RemoveMemberInput) (*RemoveMemberOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
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
		if err := deps.Queries.RemoveWorkspaceMemberByUserId(ctx, generated.RemoveWorkspaceMemberByUserIdParams{
			WorkspaceID: ws.ID,
			UserID:      uid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &RemoveMemberOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
