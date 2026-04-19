package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/integrations/email"
)

// PrefixInvite is the user-visible prefix for workspace invite tokens.
const PrefixInvite = "inv_"

// InviteDeps extends the standard Deps with fields required by the
// invite link handlers. EmailSender may be nil when SMTP is not
// configured; invite creation still succeeds but no email is sent.
type InviteDeps struct {
	Deps
	EmailSender email.Sender
	WebURL      string
}

// CreateInvite handles POST /workspaces/{wsId}/invites. It generates a
// shareable invite token and stores its SHA-256 hash. The plaintext
// token is returned exactly once in the response.
func CreateInvite(deps InviteDeps) func(context.Context, *CreateWorkspaceInviteInput) (*CreateWorkspaceInviteOutput, error) {
	return func(ctx context.Context, in *CreateWorkspaceInviteInput) (*CreateWorkspaceInviteOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		plaintext, hash, err := auth.GenerateOpaque(PrefixInvite)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		now := time.Now()
		pub := types.New()
		role := generated.WorkspaceInvitesRole(in.Body.Role)

		var maxUses sql.NullInt32
		if in.Body.MaxUses != nil {
			maxUses = sql.NullInt32{Int32: *in.Body.MaxUses, Valid: true}
		}

		var expiresAt sql.NullTime
		if in.Body.ExpiresIn != nil {
			expiresAt = sql.NullTime{Time: now.Add(time.Duration(*in.Body.ExpiresIn) * time.Second), Valid: true}
		}

		var label sql.NullString
		if in.Body.Label != "" {
			label = sql.NullString{String: in.Body.Label, Valid: true}
		}

		if _, err := deps.Queries.CreateWorkspaceInvite(ctx, generated.CreateWorkspaceInviteParams{
			PublicID:        pub,
			WorkspaceID:     ws.ID,
			TokenHash:       hash,
			Role:            role,
			CreatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},
			MaxUses:         maxUses,
			ExpiresAt:       expiresAt,
			Label:           label,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Best-effort email delivery when both the sender and target
		// email are available.
		if deps.EmailSender != nil && in.Body.Email != "" {
			link := deps.WebURL + "/invites/" + plaintext
			_ = deps.EmailSender.Send(ctx, email.Message{
				To:      []string{in.Body.Email},
				Subject: "You've been invited to a workspace",
				Body:    "You have been invited to join a workspace. Click the link below to accept:\n\n" + link,
			})
		}

		invite := WorkspaceInvite{
			ID:        pub.String(),
			Role:      string(role),
			MaxUses:   nullInt32Ptr(maxUses),
			UseCount:  0,
			Label:     in.Body.Label,
			ExpiresAt: nullTime(expiresAt),
			CreatedAt: now,
		}

		return &CreateWorkspaceInviteOutput{Body: CreateWorkspaceInviteOutputBody{
			Invite: invite,
			Token:  plaintext,
		}}, nil
	}
}

// ListInvites handles GET /workspaces/{wsId}/invites.
func ListInvites(deps InviteDeps) func(context.Context, *ListWorkspaceInvitesInput) (*ListWorkspaceInvitesOutput, error) {
	return func(ctx context.Context, in *ListWorkspaceInvitesInput) (*ListWorkspaceInvitesOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := deps.Queries.ListWorkspaceInvites(ctx, generated.ListWorkspaceInvitesParams{
			WorkspaceID: ws.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListWorkspaceInvitesOutput{}
		out.Body.Invites = make([]WorkspaceInvite, 0, len(rows))
		for _, r := range rows {
			out.Body.Invites = append(out.Body.Invites, rowToInvite(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// RevokeInvite handles DELETE /workspaces/{wsId}/invites/{inviteId}.
func RevokeInvite(deps InviteDeps) func(context.Context, *RevokeWorkspaceInviteInput) (*RevokeWorkspaceInviteOutput, error) {
	return func(ctx context.Context, in *RevokeWorkspaceInviteInput) (*RevokeWorkspaceInviteOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.InviteID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err := deps.Queries.RevokeWorkspaceInvite(ctx, generated.RevokeWorkspaceInviteParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &RevokeWorkspaceInviteOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// AcceptInvite handles POST /invites/{token}/accept. The caller must be
// authenticated but does not need to be a member of any workspace. The
// token is hashed and looked up; if valid, the caller is added as a
// member with the role specified on the invite.
func AcceptInvite(deps InviteDeps) func(context.Context, *AcceptWorkspaceInviteInput) (*AcceptWorkspaceInviteOutput, error) {
	return func(ctx context.Context, in *AcceptWorkspaceInviteInput) (*AcceptWorkspaceInviteOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		hash := auth.HashOpaque(in.Token)
		invite, err := deps.Queries.FindWorkspaceInviteByTokenHash(ctx, hash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsWorkspaceNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Validate expiry.
		if invite.ExpiresAt.Valid && invite.ExpiresAt.Time.Before(time.Now()) {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		// Validate use count.
		if invite.MaxUses.Valid && int32(invite.UseCount) >= invite.MaxUses.Int32 {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		// Idempotency: if already a member, return workspace info.
		if _, merr := deps.Queries.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
			WorkspaceID: invite.WorkspaceID,
			UserID:      actorID,
		}); merr == nil {
			// Already a member — look up workspace name for the response.
			wsRow, werr := deps.Queries.FindWorkspaceInviteWorkspaceName(ctx, hash)
			if werr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			return &AcceptWorkspaceInviteOutput{Body: AcceptWorkspaceInviteOutputBody{
				WorkspaceID:   wsRow.WorkspacePublicID.String(),
				WorkspaceName: wsRow.Name,
				Role:          string(invite.Role),
			}}, nil
		}

		// Create workspace member.
		now := time.Now()
		memPub := types.New()
		memberRole := generated.WorkspaceMembersRole(invite.Role)
		if _, err := deps.Queries.CreateWorkspaceMember(ctx, generated.CreateWorkspaceMemberParams{
			PublicID:    memPub,
			WorkspaceID: invite.WorkspaceID,
			UserID:      actorID,
			Role:        memberRole,
			JoinedAt:    sql.NullTime{Time: now, Valid: true},
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Increment use count.
		if err := deps.Queries.IncrementInviteUseCount(ctx, invite.ID); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Look up workspace info for the response.
		wsRow, err := deps.Queries.FindWorkspaceInviteWorkspaceName(ctx, hash)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &AcceptWorkspaceInviteOutput{Body: AcceptWorkspaceInviteOutputBody{
			WorkspaceID:   wsRow.WorkspacePublicID.String(),
			WorkspaceName: wsRow.Name,
			Role:          string(invite.Role),
		}}, nil
	}
}

// InviteInfo handles GET /invites/{token}/info. This is a public
// endpoint (no auth required) that returns minimal workspace info for
// the invite preview page.
func InviteInfo(deps InviteDeps) func(context.Context, *InviteInfoInput) (*InviteInfoOutput, error) {
	return func(ctx context.Context, in *InviteInfoInput) (*InviteInfoOutput, error) {
		hash := auth.HashOpaque(in.Token)

		invite, err := deps.Queries.FindWorkspaceInviteByTokenHash(ctx, hash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsWorkspaceNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Validate expiry.
		if invite.ExpiresAt.Valid && invite.ExpiresAt.Time.Before(time.Now()) {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		// Validate use count.
		if invite.MaxUses.Valid && int32(invite.UseCount) >= invite.MaxUses.Int32 {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		wsRow, err := deps.Queries.FindWorkspaceInviteWorkspaceName(ctx, hash)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &InviteInfoOutput{Body: InviteInfoOutputBody{
			WorkspaceName: wsRow.Name,
			Role:          string(invite.Role),
			ExpiresAt:     nullTime(invite.ExpiresAt),
		}}, nil
	}
}

// rowToInvite converts a ListWorkspaceInvitesRow to the public DTO.
func rowToInvite(r generated.ListWorkspaceInvitesRow) WorkspaceInvite {
	return WorkspaceInvite{
		ID:            r.PublicID.String(),
		Role:          string(r.Role),
		MaxUses:       nullInt32Ptr(r.MaxUses),
		UseCount:      r.UseCount,
		Label:         nullStr(r.Label),
		CreatedByName: r.CreatedByName,
		ExpiresAt:     nullTime(r.ExpiresAt),
		CreatedAt:     r.CreatedAt,
	}
}

// nullInt32Ptr converts a sql.NullInt32 to a *int32, returning nil when NULL.
func nullInt32Ptr(n sql.NullInt32) *int32 {
	if n.Valid {
		v := n.Int32
		return &v
	}
	return nil
}
