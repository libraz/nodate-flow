package tasks

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// Archive returns a handler for POST /tasks/{id}/archive.
func Archive(deps Deps) func(context.Context, *ArchiveTaskInput) (*ArchiveTaskOutput, error) {
	return func(ctx context.Context, _ *ArchiveTaskInput) (*ArchiveTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		if err := deps.Queries.ArchiveTask(ctx, generated.ArchiveTaskParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskArchived,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload:     map[string]any{"taskId": task.PublicID.String()},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.Archive"),
				slog.String("event_type", string(eventbus.TaskArchived)),
				slog.Int64("workspace_id", int64(ws.ID)),
				slog.Int64("task_id", taskInternal),
			)
		}

		if actorID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.archived",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "task",
				ResourceID:   task.PublicID.String(),
			})
		}

		out := &ArchiveTaskOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// Unarchive returns a handler for POST /tasks/{id}/unarchive.
func Unarchive(deps Deps) func(context.Context, *UnarchiveTaskInput) (*UnarchiveTaskOutput, error) {
	return func(ctx context.Context, _ *UnarchiveTaskInput) (*UnarchiveTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		if err := deps.Queries.UnarchiveTask(ctx, generated.UnarchiveTaskParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskUnarchived,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload:     map[string]any{"taskId": task.PublicID.String()},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.Unarchive"),
				slog.String("event_type", string(eventbus.TaskUnarchived)),
				slog.Int64("workspace_id", int64(ws.ID)),
				slog.Int64("task_id", taskInternal),
			)
		}

		if actorID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.unarchived",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "task",
				ResourceID:   task.PublicID.String(),
			})
		}

		out := &UnarchiveTaskOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// ListArchived returns a handler for GET /workspaces/{wsId}/tasks/archived.
//
// Pagination: when `cursor` is non-empty the keyset path runs and the
// response carries `nextCursor`; otherwise the historical OFFSET path
// runs unchanged. The keyset variant keys on (archived_at, public_id),
// not (created_at, public_id) — archived rows are sorted newest-
// archived-first, so the cursor naturally tracks archived_at.
func ListArchived(deps Deps) func(context.Context, *ListArchivedTasksInput) (*ListArchivedTasksOutput, error) {
	return func(ctx context.Context, in *ListArchivedTasksInput) (*ListArchivedTasksOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		out := &ListArchivedTasksOutput{}
		out.Body.Tasks = make([]TaskListItem, 0)

		if in.Cursor != "" {
			cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
			if derr != nil {
				return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
			}
			rows, qerr := deps.Queries.ListArchivedTasksForWorkspaceKeyset(ctx, generated.ListArchivedTasksForWorkspaceKeysetParams{
				WorkspaceID:      ws.ID,
				CursorArchivedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
				CursorPublicID:   cursorPID,
				Limit:            in.Limit + 1,
			})
			if qerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			hasMore := int32(len(rows)) > in.Limit
			if hasMore {
				rows = rows[:in.Limit]
			}
			for _, r := range rows {
				out.Body.Tasks = append(out.Body.Tasks, mapArchivedTaskListItemKeyset(r))
			}
			if hasMore {
				last := rows[len(rows)-1]
				// archived_at is the keyset key for this query, not
				// created_at. v_task_list_archived only emits rows
				// with archived_at IS NOT NULL so .Time is reliable.
				nc := handlerutil.EncodeCursor(last.ArchivedAt.Time, last.PublicID)
				out.Body.NextCursor = &nc
			}
			return out, nil
		}

		rows, err := deps.Queries.ListArchivedTasksForWorkspace(ctx, generated.ListArchivedTasksForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       in.Limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out.Body.Tasks = make([]TaskListItem, 0, len(rows))
		for _, r := range rows {
			out.Body.Tasks = append(out.Body.Tasks, mapArchivedTaskListItem(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
			if int64(in.Offset+in.Limit) < out.Body.Total {
				last := rows[len(rows)-1]
				// Archived list keys on archived_at, not created_at.
				nc := handlerutil.EncodeCursor(last.ArchivedAt.Time, last.PublicID)
				out.Body.NextCursor = &nc
			}
		}
		return out, nil
	}
}

// mapArchivedTaskListItem converts an archived task row to a TaskListItem DTO.
func mapArchivedTaskListItem(r generated.ListArchivedTasksForWorkspaceRow) TaskListItem {
	item := TaskListItem{
		ID:                r.PublicID.String(),
		ProjectID:         bytesToUUIDString(r.ProjectPublicID),
		ProjectName:       r.ProjectName,
		ProjectIdentifier: r.ProjectIdentifier.String,
		TaskNumber:        int32(r.TaskNumber),
		ParentTaskID:      nullBytesToUUIDString(r.ParentTaskPublicID),
		Title:             r.Title,
		Visibility:        string(r.Visibility),
		DerivedState:      string(r.DerivedState),
		Priority:          r.Priority,
		DueOn:             nullDate(r.DueOn),
		StartedOn:         nullDate(r.StartedOn),
		CompletedAt:       nullTimeUnix(r.CompletedAt),
		ArchivedAt:        nullTimeUnix(r.ArchivedAt),
		LabelIDs:          nullStr(r.LabelIds),
		SortWeight:        r.SortWeight,
		PrimaryAssigneeID: rawBytesToUUIDPtr(r.PrimaryAssigneePublicID),
		AssigneeCount:     r.AssigneeCount,
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
	return item
}

// mapArchivedTaskListItemKeyset is the keyset twin of
// mapArchivedTaskListItem — same projection, no Total column.
func mapArchivedTaskListItemKeyset(r generated.ListArchivedTasksForWorkspaceKeysetRow) TaskListItem {
	return TaskListItem{
		ID:                r.PublicID.String(),
		ProjectID:         bytesToUUIDString(r.ProjectPublicID),
		ProjectName:       r.ProjectName,
		ProjectIdentifier: r.ProjectIdentifier.String,
		TaskNumber:        int32(r.TaskNumber),
		ParentTaskID:      nullBytesToUUIDString(r.ParentTaskPublicID),
		Title:             r.Title,
		Visibility:        string(r.Visibility),
		DerivedState:      string(r.DerivedState),
		Priority:          r.Priority,
		DueOn:             nullDate(r.DueOn),
		StartedOn:         nullDate(r.StartedOn),
		CompletedAt:       nullTimeUnix(r.CompletedAt),
		ArchivedAt:        nullTimeUnix(r.ArchivedAt),
		LabelIDs:          nullStr(r.LabelIds),
		SortWeight:        r.SortWeight,
		PrimaryAssigneeID: rawBytesToUUIDPtr(r.PrimaryAssigneePublicID),
		AssigneeCount:     r.AssigneeCount,
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}
