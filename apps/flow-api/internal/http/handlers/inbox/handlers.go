package inbox

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
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
			// An inbox item names the task a signal was linked to, and
			// that link is not automatically the reader's to follow.
			// The task columns are blanked for anyone the task's own
			// visibility excludes; the signal itself still lists.
			wsRole, err := acl.CheckWorkspaceMember(ctx, deps.DB, wsID, actorID, nil)
			if err != nil {
				return nil, err
			}
			vis := acl.ListVisibilityArgs(actorID, wsRole)
			rows, err := deps.Queries.ListInbox(ctx, generated.ListInboxParams{
				WorkspaceID:   wsID,
				IsElevated:    vis.IsElevated,
				ActorUserID:   vis.ActorUserID,
				ActorUserID_2: vis.ActorUserID,
				ActorUserID_3: vis.ActorUserID,
				Limit:         limit,
				Offset:        in.Offset,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			for _, r := range rows {
				taskID, taskTitle := nullBytesToUUIDString(r.TaskPublicID), nullStr(r.TaskTitle)
				if !r.TaskVisible.Valid || !r.TaskVisible.Bool {
					taskID, taskTitle = "", ""
				}
				out.Body.Items = append(out.Body.Items, Item{
					ID:          r.PublicID.String(),
					WorkspaceID: bytesToUUIDString(r.WorkspacePublicID),
					TaskID:      taskID,
					TaskTitle:   taskTitle,
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

		// The cross-workspace feed spans workspaces whose roles differ,
		// so there is no single elevated flag to compute. The actor is
		// treated as unelevated throughout and sees task titles only
		// where the task's own visibility admits them; an admin loses
		// nothing they can still reach through that workspace's own
		// inbox, which does compute the flag.
		userVis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRoleMember)
		rows, err := deps.Queries.ListInboxForUser(ctx, generated.ListInboxForUserParams{
			UserID:        actorID,
			IsElevated:    userVis.IsElevated,
			ActorUserID:   userVis.ActorUserID,
			ActorUserID_2: userVis.ActorUserID,
			ActorUserID_3: userVis.ActorUserID,
			Limit:         limit,
			Offset:        in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		for _, r := range rows {
			taskID, taskTitle := nullBytesToUUIDString(r.TaskPublicID), nullStr(r.TaskTitle)
			if !r.TaskVisible.Valid || !r.TaskVisible.Bool {
				taskID, taskTitle = "", ""
			}
			out.Body.Items = append(out.Body.Items, Item{
				ID:          r.PublicID.String(),
				WorkspaceID: bytesToUUIDString(r.WorkspacePublicID),
				TaskID:      taskID,
				TaskTitle:   taskTitle,
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
