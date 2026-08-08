package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
	"github.com/libraz/nodate-flow/packages/go-shared/memberkit"
	sharedtoken "github.com/libraz/nodate-flow/packages/go-shared/token"
)

// PrefixInvite is the user-visible prefix for workspace invite tokens.
// Re-exported from the centralised packages/go-shared/token catalogue
// so callers in this package keep their existing import surface while
// the constant lives in a single source of truth.
const PrefixInvite = sharedtoken.PrefixInvite

// InviteAcceptPathPrefix is flow-web's public workspace-invite route with
// its token segment removed. The route is singular (`/invite/$token`);
// the plural `/invites/accept` next to it belongs to calendar event
// invites, so a link built against the wrong one resolves to nothing and
// the invitee gets a 404 instead of a workspace.
//
// A cross-language guard in invite_link_test.go reads flow-web's
// generated route tree and fails if this prefix stops matching a route
// that exists.
const InviteAcceptPathPrefix = "/invite/"

// InviteAcceptURL builds the link an invitee follows to join a
// workspace. Every surface that hands out an invite goes through here so
// the email and the copy-link dialog cannot drift apart.
func InviteAcceptURL(webURL, token string) string {
	return strings.TrimRight(webURL, "/") + InviteAcceptPathPrefix + token
}

// errInviteExhausted rolls the accept transaction back when the invite
// has no use slots left. It never reaches the client — the handler
// answers with WORKSPACE_INVITE.EXHAUSTED — and it is deliberately not
// a transient error so the retry loop leaves it alone.
var errInviteExhausted = errors.New("workspace: invite exhausted")

// uint32FromNullInt32 extracts a uint32 from a sql.NullInt32; returns
// 0 when the null flag is unset.
func uint32FromNullInt32(n sql.NullInt32) uint32 {
	if !n.Valid {
		return 0
	}
	return uint32(n.Int32) //#nosec G115 -- created_by_user_id is users.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
}

// CreateInvite handles POST /workspaces/{wsId}/invites. It generates a
// shareable invite token and stores its SHA-256 hash. The plaintext
// token is returned exactly once in the response.
func CreateInvite(deps InviteDeps) func(context.Context, *CreateInviteInput) (*CreateInviteOutput, error) {
	return func(ctx context.Context, in *CreateInviteInput) (*CreateInviteOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := authn.ActorFromContext(ctx)
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

		// Privilege-escalation guard: an actor may not issue an invite for
		// a role that outranks their own (only an owner may grant owner).
		if err := memberkit.EnsureRoleWithinActor(memberkit.Role(ws.Role), memberkit.Role(role)); err != nil {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

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
			CreatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
			MaxUses:         maxUses,
			ExpiresAt:       expiresAt,
			Label:           label,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Best-effort email delivery when both the sender and target
		// email are available.
		if deps.EmailSender != nil && in.Body.Email != "" {
			link := InviteAcceptURL(deps.WebURL, plaintext)
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
			ExpiresAt: nullTimeUnix(expiresAt),
			CreatedAt: now.Unix(),
		}

		return &CreateInviteOutput{Body: CreateInviteOutputBody{
			Invite: invite,
			Token:  plaintext,
		}}, nil
	}
}

// ListInvites handles GET /workspaces/{wsId}/invites.
func ListInvites(deps InviteDeps) func(context.Context, *ListInvitesInput) (*ListInvitesOutput, error) {
	return func(ctx context.Context, in *ListInvitesInput) (*ListInvitesOutput, error) {
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
		out := &ListInvitesOutput{}
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
func RevokeInvite(deps InviteDeps) func(context.Context, *RevokeInviteInput) (*RevokeInviteOutput, error) {
	return func(ctx context.Context, in *RevokeInviteInput) (*RevokeInviteOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.InviteID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		// Scoped to this workspace and to invites that are still live, so
		// zero rows means the caller named an invite that is not theirs
		// to revoke — or one that is already gone. Either way there is
		// nothing to report as revoked.
		rows, err := deps.Queries.RevokeWorkspaceInvite(ctx, generated.RevokeWorkspaceInviteParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if rows == 0 {
			return nil, httpErr(apierrors.WsWorkspaceInviteNotFound)
		}
		out := &RevokeInviteOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// AcceptInvite handles POST /invites/{token}/accept. The caller must be
// authenticated but does not need to be a member of any workspace.
func AcceptInvite(deps InviteDeps) func(context.Context, *AcceptInviteInput) (*AcceptInviteOutput, error) {
	return func(ctx context.Context, in *AcceptInviteInput) (*AcceptInviteOutput, error) {
		actorID, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		hash := auth.HashOpaque(in.Token)
		invite, err := deps.Queries.FindWorkspaceInviteByTokenHash(ctx, hash)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceNotFound, apierrors.InternalUnexpected))
		}

		// Validate expiry.
		if invite.ExpiresAt.Valid && invite.ExpiresAt.Time.Before(time.Now()) {
			return nil, httpErr(apierrors.WsWorkspaceInviteExpired)
		}

		// Fast-path rejection of an already-exhausted invite. This is a
		// best-effort optimisation only: the authoritative guard is the
		// atomic conditional increment inside the redemption tx below
		// (IncrementInviteUseCount), which closes the TOCTOU window that
		// this non-transactional read cannot.
		if invite.MaxUses.Valid && int32(invite.UseCount) >= invite.MaxUses.Int32 { //#nosec G115 -- workspace_invites.use_count is INT UNSIGNED capped by max_uses (INT)
			return nil, httpErr(apierrors.WsWorkspaceInviteExhausted)
		}

		// Idempotency: if already a member, return workspace info.
		if _, merr := deps.Queries.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
			WorkspaceID: invite.WorkspaceID,
			UserID:      actorID,
		}); merr == nil {
			wsRow, werr := deps.Queries.FindWorkspaceInviteWorkspaceName(ctx, hash)
			if werr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			return &AcceptInviteOutput{Body: AcceptWorkspaceInviteOutputBody{
				WorkspaceID:   wsRow.WorkspacePublicID.String(),
				WorkspaceName: wsRow.Name,
				Role:          string(invite.Role),
			}}, nil
		}

		// Create the member + materialise personal calendar + use-count
		// bump in a single tx so a mid-flight failure never leaves the
		// user half-joined.
		now := time.Now()
		// Concurrent redemptions of the same invite contend on the invite
		// row and on the workspace's member rows, so this is a natural
		// deadlock site; retry the whole transaction rather than telling
		// the invitee their link is broken.
		exhausted := false
		txErr := dbretry.InTx(ctx, deps.DB, "workspace.AcceptInvite", nil, func(ctx context.Context, tx *sql.Tx) error {
			exhausted = false
			txQueries := deps.Queries.WithTx(tx)

			// Atomically claim a use slot first. The conditional UPDATE only
			// affects a row while use_count < max_uses, so concurrent accepts
			// racing on the same invite can never collectively exceed the cap:
			// exactly max_uses redemptions see affected == 1; the rest see 0
			// and bail out as exhausted. Running this inside the tx (before the
			// member add) means a loser's transaction rolls back cleanly without
			// having created a half-joined member.
			affected, err := txQueries.IncrementInviteUseCount(ctx, invite.ID)
			if err != nil {
				return err
			}
			if affected == 0 {
				// Not a failure of the transaction: the cap is simply
				// full. Flag it and roll back with a sentinel so the
				// retry loop does not treat it as work to repeat.
				exhausted = true
				return errInviteExhausted
			}

			_, err = memberkit.AddWorkspaceMember(ctx, tx, memberkit.AddWorkspaceMemberArgs{
				WorkspaceID:            invite.WorkspaceID,
				UserID:                 actorID,
				Role:                   memberkit.Role(invite.Role),
				InvitedByUserID:        uint32FromNullInt32(invite.CreatedByUserID),
				InvitedAt:              invite.CreatedAt,
				JoinedAt:               now,
				EnsurePersonalCalendar: true,
			})
			return err
		})
		if exhausted {
			return nil, httpErr(apierrors.WsWorkspaceInviteExhausted)
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		wsRow, err := deps.Queries.FindWorkspaceInviteWorkspaceName(ctx, hash)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &AcceptInviteOutput{Body: AcceptWorkspaceInviteOutputBody{
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceNotFound, apierrors.InternalUnexpected))
		}

		// Validate expiry.
		if invite.ExpiresAt.Valid && invite.ExpiresAt.Time.Before(time.Now()) {
			return nil, httpErr(apierrors.WsWorkspaceInviteExpired)
		}

		// Validate use count.
		if invite.MaxUses.Valid && int32(invite.UseCount) >= invite.MaxUses.Int32 { //#nosec G115 -- workspace_invites.use_count is INT UNSIGNED capped by max_uses (INT)
			return nil, httpErr(apierrors.WsWorkspaceInviteExhausted)
		}

		wsRow, err := deps.Queries.FindWorkspaceInviteWorkspaceName(ctx, hash)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &InviteInfoOutput{Body: InviteInfoOutputBody{
			WorkspaceName: wsRow.Name,
			Role:          string(invite.Role),
			ExpiresAt:     nullTimeUnix(invite.ExpiresAt),
		}}, nil
	}
}
