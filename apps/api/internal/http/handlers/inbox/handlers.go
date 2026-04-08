package inbox

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

// resolveWorkspace validates the caller's membership of the workspace
// identified by a public UUID.
func resolveWorkspace(ctx context.Context, db *sql.DB, wsPublic string, actorID uint32) (uint32, error) {
	if wsPublic == "" {
		return 0, httpErr(apierrors.WsWorkspaceNotFound)
	}
	pub, err := types.Parse(wsPublic)
	if err != nil {
		return 0, httpErr(apierrors.WsWorkspaceNotFound)
	}
	const wsLookup = `SELECT id FROM workspaces WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	var wsID uint32
	if err := db.QueryRowContext(ctx, wsLookup, pub).Scan(&wsID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.WsWorkspaceNotFound)
		}
		return 0, httpErr(apierrors.InternalUnexpected)
	}
	const wsMemQuery = `SELECT 1 FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	var one int
	if err := db.QueryRowContext(ctx, wsMemQuery, wsID, actorID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		return 0, httpErr(apierrors.InternalUnexpected)
	}
	return wsID, nil
}

// List handles GET /inbox.
func List(deps Deps) func(context.Context, *ListInput) (*ListOutput, error) {
	return func(ctx context.Context, in *ListInput) (*ListOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolveWorkspace(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := deps.Queries.ListInbox(ctx, generated.ListInboxParams{
			WorkspaceID: wsID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListOutput{}
		out.Body.Items = []Item{}
		for _, r := range rows {
			out.Body.Items = append(out.Body.Items, Item{
				ID:         r.PublicID.String(),
				TaskID:     nullStr(r.TaskPublicID),
				TaskTitle:  nullStr(r.TaskTitle),
				Source:     string(r.Source),
				Kind:       r.Kind,
				ExternalID: nullStr(r.ExternalID),
				Payload:    r.PayloadJson,
				ReceivedAt: r.ReceivedAt,
				CreatedAt:  r.CreatedAt,
			})
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Archive handles POST /inbox/{id}/archive.
func Archive(deps Deps) func(context.Context, *ArchiveInput) (*ArchiveOutput, error) {
	return func(ctx context.Context, in *ArchiveInput) (*ArchiveOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolveWorkspace(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}
		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		if err := deps.Queries.ArchiveInboxItem(ctx, generated.ArchiveInboxItemParams{
			WorkspaceID: wsID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ArchiveOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// Snooze handles POST /inbox/{id}/snooze.
func Snooze(deps Deps) func(context.Context, *SnoozeInput) (*SnoozeOutput, error) {
	return func(ctx context.Context, in *SnoozeInput) (*SnoozeOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolveWorkspace(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}
		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		if in.Body.SnoozeUntil <= 0 {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		until := time.Unix(in.Body.SnoozeUntil, 0).UTC()
		if err := deps.Queries.SnoozeInboxItem(ctx, generated.SnoozeInboxItemParams{
			ReceivedAt:  until,
			WorkspaceID: wsID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &SnoozeOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
