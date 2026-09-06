package inbox

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
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
//
// The intake queue belongs to the workspace, not to the reader: a signal row
// carries no user column, so archiving one takes it off every member's list.
// That is shared state, and guests — the read-only workspace role — are held
// out of it the same way they are held out of labels or pages.
func Archive(deps Deps) func(context.Context, *ArchiveInboxInput) (*ArchiveInboxOutput, error) {
	return func(ctx context.Context, in *ArchiveInboxInput) (*ArchiveInboxOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolve.WorkspaceMemberForWrite(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}
		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		// Only matches items that are still in the inbox, so a zero count
		// means the caller archived nothing. Answering ok for that is how an
		// inbox that refuses to empty looks like it worked.
		rows, err := deps.Queries.ArchiveInboxItem(ctx, generated.ArchiveInboxItemParams{
			WorkspaceID: wsID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if rows == 0 {
			return nil, httpErr(apierrors.WsInboxNotFound)
		}

		// The kind names the signals table because that is the row the id
		// identifies: the inbox is a view over signals, and a consumer
		// told the change happened to an intake_items row would resolve
		// this id against a table it is not in.
		//
		// Best effort: the update is committed on its own connection by the
		// time this runs, so failing the request here would tell the caller
		// nothing happened while the item has already left every member's
		// queue — and their retry could not repair the log either, because
		// the item is no longer in the inbox and the retry answers 404.
		deps.Mutations.Record(ctx, mutationlog.Actor{UserID: actorID, WorkspaceID: wsID}, mutationlog.Mutation{
			EventType:    eventbus.SignalArchived,
			AuditAction:  "inbox.archive",
			ResourceType: "inbox_item",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"inboxItemId": pub.String(),
			},
			CallSite: "inbox.Archive",
		})

		out := &ArchiveInboxOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// Snooze handles POST /inbox/{id}/snooze.
//
// Same shared-state reasoning as [Archive]: the snoozed item disappears from
// the queue every member reads, so the workspace write floor applies.
func Snooze(deps Deps) func(context.Context, *SnoozeInboxInput) (*SnoozeInboxOutput, error) {
	return func(ctx context.Context, in *SnoozeInboxInput) (*SnoozeInboxOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolve.WorkspaceMemberForWrite(ctx, deps.DB, in.WorkspaceID, actorID)
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
		// Not an existence check: snoozing to the timestamp the item
		// already carries changes no column and MySQL counts zero.
		if _, err := deps.Queries.SnoozeInboxItem(ctx, generated.SnoozeInboxItemParams{
			ReceivedAt:  until,
			WorkspaceID: wsID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// The kind names the signals table for the same reason as
		// [Archive]: snoozing moves the received_at of a signal row, and
		// the id in the record is that row's.
		//
		// Best effort for the same reason as [Archive]: the update is
		// committed before this runs, and the endpoint answers ok whether or
		// not the deadline it was given differs from the one the item
		// already carried, so a lost log row must not turn that into a
		// failure. Nothing is derived from the event row — the deadline
		// lives on the item — so there is no state a strict append would
		// protect.
		deps.Mutations.Record(ctx, mutationlog.Actor{UserID: actorID, WorkspaceID: wsID}, mutationlog.Mutation{
			EventType:    eventbus.SignalSnoozed,
			AuditAction:  "inbox.snooze",
			ResourceType: "inbox_item",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"inboxItemId": pub.String(),
				"snoozeUntil": in.Body.SnoozeUntil,
			},
			CallSite: "inbox.Snooze",
		})

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
