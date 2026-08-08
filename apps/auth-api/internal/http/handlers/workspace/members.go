package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/memberkit"
)

// resolveUserInternalID looks up the internal numeric users.id for the
// given public UUID.
func resolveUserInternalID(ctx context.Context, q *generated.Queries, pub types.PublicID) (uint32, error) {
	return q.FindUserInternalIdByPublicId(ctx, pub)
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

// AddMember handles POST /workspaces/{wsId}/members: an admin grants
// membership directly, with no token and no acceptance step. When the
// address has no account yet a placeholder user is created to hang the
// membership on, which whoever later signs in with that address adopts.
//
// That is a deliberate admin capability, not an invitation, and the two
// are not interchangeable: the address is never asked and never
// verified, so an address entered here by mistake hands a stranger a
// working seat. Callers that want the recipient to consent — anything
// driven by a user-supplied address rather than an admin's own roster —
// belong on POST /workspaces/{wsId}/invites, which mails a token the
// recipient has to redeem.
func AddMember(deps Deps) func(context.Context, *AddMemberInput) (*AddMemberOutput, error) {
	return func(ctx context.Context, in *AddMemberInput) (*AddMemberOutput, error) {
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

		// Privilege-escalation guard: an actor may not grant a role that
		// outranks their own (only an owner may grant owner).
		if err := memberkit.EnsureRoleWithinActor(memberkit.Role(ws.Role), memberkit.Role(role)); err != nil {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

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
			userID = uint32(id) //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
			displayName = emailAddr
		default:
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Route member creation through memberkit so the personal
		// calendar layer and (if applicable) holiday subscription
		// materialise in the same transaction as the member row.
		now := time.Now()
		var mkRes memberkit.AddWorkspaceMemberResult
		if err := dbretry.InTx(ctx, deps.DB, "workspace.AddMember", nil, func(ctx context.Context, tx *sql.Tx) error {
			res, err := memberkit.AddWorkspaceMember(ctx, tx, memberkit.AddWorkspaceMemberArgs{
				WorkspaceID:            ws.ID,
				UserID:                 userID,
				Role:                   memberkit.Role(role),
				InvitedByUserID:        actorID,
				InvitedAt:              now,
				EnsurePersonalCalendar: true,
			})
			if err != nil {
				return err
			}
			mkRes = res
			return nil
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Fetch the member row to build the response with canonical
		// timestamps. This second read is deliberate: memberkit does
		// not marshal to the handler DTO.
		mem, err := deps.Queries.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
			WorkspaceID: ws.ID,
			UserID:      userID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		_ = mkRes // result struct reserved for future UI signals (created calendar, etc.)
		return &AddMemberOutput{Body: WorkspaceMember{
			ID:          mem.PublicID.String(),
			UserID:      userPub.String(),
			Email:       emailAddr,
			DisplayName: displayName,
			Role:        string(mem.Role),
			InvitedAt:   nullTimeUnix(mem.InvitedAt),
			JoinedAt:    nullTimeUnix(mem.JoinedAt),
			CreatedAt:   mem.CreatedAt.Unix(),
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
		actorID, _ := authn.ActorFromContext(ctx)

		// Privilege-escalation guard: an actor may not promote a member to
		// a role that outranks their own (only an owner may grant owner).
		if err := memberkit.EnsureRoleWithinActor(memberkit.Role(ws.Role), memberkit.Role(in.Body.Role)); err != nil {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

		userPub, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.WsMemberNotFound)
		}
		uid, err := resolveUserInternalID(ctx, deps.Queries, userPub)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsMemberNotFound, apierrors.InternalUnexpected))
		}

		// The closure returns memberkit's error unchanged so the mapping
		// below stays a pure function of the error value; nothing in it
		// depends on which attempt produced it.
		if err := dbretry.InTx(ctx, deps.DB, "workspace.UpdateMemberRole", nil, func(ctx context.Context, tx *sql.Tx) error {
			return memberkit.UpdateMemberRole(ctx, tx, memberkit.UpdateMemberRoleArgs{
				WorkspaceID: ws.ID,
				UserID:      uid,
				NewRole:     memberkit.Role(in.Body.Role),
				ActorUserID: actorID,
			})
		}); err != nil {
			switch {
			case errors.Is(err, memberkit.ErrSelfModify):
				return nil, httpErr(apierrors.WsMemberSelfModify)
			case errors.Is(err, memberkit.ErrLastOwner):
				return nil, httpErr(apierrors.WsMemberLastOwner)
			case errors.Is(err, memberkit.ErrRoleEscalation):
				// The actor does not outrank the member they are acting
				// on (an admin reaching for an owner). Same refusal the
				// grant side already returns.
				return nil, httpErr(apierrors.WsMemberRoleDenied)
			}
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsMemberNotFound, apierrors.InternalUnexpected))
		}

		mem, err := deps.Queries.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
			WorkspaceID: ws.ID,
			UserID:      uid,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &UpdateMemberRoleOutput{Body: WorkspaceMember{
			ID:        mem.PublicID.String(),
			UserID:    userPub.String(),
			Role:      string(mem.Role),
			InvitedAt: nullTimeUnix(mem.InvitedAt),
			JoinedAt:  nullTimeUnix(mem.JoinedAt),
			CreatedAt: mem.CreatedAt.Unix(),
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
		actorID, _ := authn.ActorFromContext(ctx)
		userPub, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.WsMemberNotFound)
		}
		uid, err := resolveUserInternalID(ctx, deps.Queries, userPub)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsMemberNotFound, apierrors.InternalUnexpected))
		}

		if err := dbretry.InTx(ctx, deps.DB, "workspace.RemoveMember", nil, func(ctx context.Context, tx *sql.Tx) error {
			_, err := memberkit.RemoveWorkspaceMember(ctx, tx, memberkit.RemoveWorkspaceMemberArgs{
				WorkspaceID: ws.ID,
				UserID:      uid,
				ActorUserID: actorID,
			})
			return err
		}); err != nil {
			switch {
			case errors.Is(err, memberkit.ErrSelfModify):
				return nil, httpErr(apierrors.WsMemberSelfModify)
			case errors.Is(err, memberkit.ErrLastOwner):
				return nil, httpErr(apierrors.WsMemberLastOwner)
			case errors.Is(err, memberkit.ErrRoleEscalation):
				// The actor does not outrank the member they are acting
				// on (an admin reaching for an owner). Same refusal the
				// grant side already returns.
				return nil, httpErr(apierrors.WsMemberRoleDenied)
			}
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsMemberNotFound, apierrors.InternalUnexpected))
		}

		out := &RemoveMemberOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
