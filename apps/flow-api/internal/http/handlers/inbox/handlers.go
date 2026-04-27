package inbox

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// List handles GET /inbox. When workspaceId is provided it scopes the
// result to that workspace (after verifying membership). When omitted it
// lists inbox items across every workspace the actor is an active member
// of.
func List(deps Deps) func(context.Context, *ListInboxInput) (*ListInboxOutput, error) {
	return func(ctx context.Context, in *ListInboxInput) (*ListInboxOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		out := &ListInboxOutput{}
		out.Body.Items = []Item{}

		if in.WorkspaceID != "" {
			wsID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
			if err != nil {
				return nil, err
			}
			rows, err := deps.Queries.ListInbox(ctx, generated.ListInboxParams{
				WorkspaceID: wsID,
				Limit:       limit,
				Offset:      in.Offset,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, Item{
					ID:          r.PublicID.String(),
					WorkspaceID: bytesToUUIDString(r.WorkspacePublicID),
					TaskID:      nullBytesToUUIDString(r.TaskPublicID),
					TaskTitle:   nullStr(r.TaskTitle),
					Source:      string(r.Source),
					Kind:        r.Kind,
					ExternalID:  nullStr(r.ExternalID),
					Payload:     r.PayloadJson,
					ReceivedAt:  r.ReceivedAt.Unix(),
					CreatedAt:   r.CreatedAt.Unix(),
				})
			}
			if len(rows) > 0 {
				out.Body.Total = totalAsInt64(rows[0].Total)
			}
			return out, nil
		}

		rows, err := deps.Queries.ListInboxForUser(ctx, generated.ListInboxForUserParams{
			UserID: actorID,
			Limit:  limit,
			Offset: in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		for _, r := range rows {
			out.Body.Items = append(out.Body.Items, Item{
				ID:          r.PublicID.String(),
				WorkspaceID: bytesToUUIDString(r.WorkspacePublicID),
				TaskID:      nullBytesToUUIDString(r.TaskPublicID),
				TaskTitle:   nullStr(r.TaskTitle),
				Source:      string(r.Source),
				Kind:        r.Kind,
				ExternalID:  nullStr(r.ExternalID),
				Payload:     r.PayloadJson,
				ReceivedAt:  r.ReceivedAt.Unix(),
				CreatedAt:   r.CreatedAt.Unix(),
			})
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Archive handles POST /inbox/{id}/archive.
func Archive(deps Deps) func(context.Context, *ArchiveInboxInput) (*ArchiveInboxOutput, error) {
	return func(ctx context.Context, in *ArchiveInboxInput) (*ArchiveInboxOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
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
		out := &ArchiveInboxOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// Snooze handles POST /inbox/{id}/snooze.
func Snooze(deps Deps) func(context.Context, *SnoozeInboxInput) (*SnoozeInboxOutput, error) {
	return func(ctx context.Context, in *SnoozeInboxInput) (*SnoozeInboxOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
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
		until := handlerutil.UnixToTime(in.Body.SnoozeUntil)
		if err := deps.Queries.SnoozeInboxItem(ctx, generated.SnoozeInboxItemParams{
			ReceivedAt:  until,
			WorkspaceID: wsID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &SnoozeInboxOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// bytesToUUIDString / nullBytesToUUIDString delegate to handlerutil so
// the byte→UUID conversion shapes share a single implementation across
// handler packages.
var (
	bytesToUUIDString     = handlerutil.BytesToUUIDString
	nullBytesToUUIDString = handlerutil.NullBytesToUUIDString
)
