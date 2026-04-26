package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

// escapeLike escapes the MySQL LIKE metacharacters %, _, and \ in a
// user-supplied search term so they are matched literally.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// allowedDerivedStates gate-keeps the `state` query parameter so that
// callers can only filter by known derived_state enum values.
var allowedDerivedStates = map[string]struct{}{
	"open":      {},
	"waiting":   {},
	"review":    {},
	"done":      {},
	"cancelled": {},
}

// hasListFilters reports whether any optional filter is set on the list
// query input; it is used to choose between the sqlc fast path and the
// dynamic SQL path.
func hasListFilters(in *ListTasksInput) bool {
	return in.Q != "" || len(in.State) > 0 || in.Assignee != ""
}

// needsDynamicQuery reports whether the request must go through the dynamic
// SQL path. This is true when there are explicit filters, or when the actor
// is not a workspace admin/owner (since non-elevated users need Layer 4
// visibility filtering that the static sqlc queries cannot express).
func needsDynamicQuery(in *ListTasksInput, wsRole middleware.WorkspaceRole) bool {
	if hasListFilters(in) {
		return true
	}
	return !wsRole.AtLeast(middleware.WorkspaceRoleAdmin)
}

// listTasksFiltered runs a dynamic SELECT against v_task_list applying the
// optional q / state / assignee filters. It bypasses sqlc because sqlc
// cannot express dynamic WHERE fragments. The shape of the returned rows
// matches the sqlc ListTasksForWorkspace projection so the existing
// mapper can reuse them.
func listTasksFiltered(
	ctx context.Context,
	db *sql.DB,
	workspaceID uint32,
	projectPublicID []byte,
	actorID uint32,
	wsRole middleware.WorkspaceRole,
	in *ListTasksInput,
) ([]generated.ListTasksForWorkspaceRow, int64, error) {
	var (
		where []string
		args  []any
	)
	where = append(where, "v.workspace_id = ?")
	args = append(args, workspaceID)

	// Apply Layer 4 task visibility filter.
	if visFrag, visArgs := middleware.TaskVisibilityFilter(actorID, wsRole); visFrag != "" {
		where = append(where, visFrag)
		args = append(args, visArgs...)
	}

	if len(projectPublicID) > 0 {
		where = append(where, "v.project_public_id = ?")
		args = append(args, projectPublicID)
	}
	if in.Q != "" {
		where = append(where, "LOWER(v.title) LIKE ?")
		args = append(args, "%"+escapeLike(strings.ToLower(in.Q))+"%")
	}
	if len(in.State) > 0 {
		placeholders := make([]string, 0, len(in.State))
		for _, s := range in.State {
			if _, ok := allowedDerivedStates[s]; !ok {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, s)
		}
		if len(placeholders) > 0 {
			where = append(where, "v.derived_state IN ("+strings.Join(placeholders, ",")+")")
		}
	}
	if in.Assignee != "" {
		assigneePub, err := types.Parse(in.Assignee)
		if err != nil {
			return nil, 0, errInvalidAssignee
		}
		pub := assigneePub.UUID()
		where = append(where, `EXISTS (
			SELECT 1 FROM task_actors ta
			INNER JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
			INNER JOIN tasks tk ON tk.public_id = v.public_id AND tk.enabled = TRUE
			WHERE ta.task_id = tk.id
			  AND ta.enabled = TRUE
			  AND ta.role = 'assignee'
			  AND u.public_id = ?
		)`)
		args = append(args, pub[:])
	}

	query := fmt.Sprintf(`SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.project_identifier,
  v.task_number,
  v.parent_task_public_id,
  v.title,
  v.visibility,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.archived_at,
  v.label_ids,
  v.sort_weight,
  v.updated_at,
  v.created_at,
  v.primary_assignee_public_id,
  v.assignee_count,
  COUNT(*) OVER() AS total
FROM v_task_list v
WHERE %s
ORDER BY v.sort_weight ASC, v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?`, strings.Join(where, " AND "))

	args = append(args, in.Limit, in.Offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var (
		out   []generated.ListTasksForWorkspaceRow
		total int64
	)
	for rows.Next() {
		var r generated.ListTasksForWorkspaceRow
		if err := rows.Scan(
			&r.PublicID,
			&r.ProjectPublicID,
			&r.ProjectName,
			&r.ProjectIdentifier,
			&r.TaskNumber,
			&r.ParentTaskPublicID,
			&r.Title,
			&r.Visibility,
			&r.DerivedState,
			&r.Priority,
			&r.DueOn,
			&r.StartedOn,
			&r.CompletedAt,
			&r.ArchivedAt,
			&r.LabelIds,
			&r.SortWeight,
			&r.UpdatedAt,
			&r.CreatedAt,
			&r.PrimaryAssigneePublicID,
			&r.AssigneeCount,
			&r.Total,
		); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) > 0 {
		total = totalAsInt64(out[0].Total)
	}
	return out, total, nil
}

// errInvalidAssignee is a sentinel surfaced by listTasksFiltered when the
// caller passes a malformed assignee UUID; the handler maps it to an empty
// result set so presence information never leaks. This is an internal-only
// sentinel — it is never returned to the client; the handler swallows it
// and returns a 200 with zero items.
var errInvalidAssignee = errors.New("tasks: invalid assignee uuid")

func parseDateOrNullTime(s string) (sql.NullTime, error) {
	if s == "" {
		return sql.NullTime{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return sql.NullTime{}, err
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

// actorPtr delegates to handlerutil.ActorPtr.
var actorPtr = handlerutil.ActorPtr

// Create handles POST /tasks. The acting workspace and project are
// resolved from the projectId in the body via FindProjectByPublicIdGlobal.
func Create(deps Deps) func(context.Context, *CreateTaskInput) (*CreateTaskOutput, error) {
	return func(ctx context.Context, in *CreateTaskInput) (*CreateTaskOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		prjPub, err := types.Parse(in.Body.ProjectID)
		if err != nil {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		prj, err := deps.Queries.FindProjectByPublicIdGlobal(ctx, prjPub)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsProjectNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		// Workspace membership check (handler-level since /tasks has no
		// workspace path parameter to attach RequireWorkspaceMember to).
		if err := handlerutil.CheckWorkspaceMember(ctx, deps.DB, prj.WorkspaceID, actorID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsProjectAccessDenied)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		due, err := parseDateOrNullTime(in.Body.DueOn)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyDateFormatInvalid)
		}
		start, err := parseDateOrNullTime(in.Body.StartOn)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyDateFormatInvalid)
		}
		// Cross-field invariant: when both dueOn and startedOn are
		// provided, dueOn must not be earlier than startedOn. Same-day
		// (equal) is allowed; NULL on either side means "unconstrained".
		if due.Valid && start.Valid && due.Time.Before(start.Time) {
			return nil, httpErr(apierrors.ValidationBodyDueBeforeStart)
		}

		pub := types.New()
		desc := sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""}
		vis := generated.TasksVisibilityPublic
		if in.Body.Visibility != "" {
			vis = generated.TasksVisibility(in.Body.Visibility)
		}

		// Allocate next task number inside a transaction for gap-lock safety.
		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer tx.Rollback() //nolint:errcheck
		qtx := deps.Queries.WithTx(tx)
		nextNum, err := qtx.AssignTaskNumber(ctx, prj.ID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskID, err := qtx.CreateTask(ctx, generated.CreateTaskParams{
			PublicID:        pub,
			WorkspaceID:     prj.WorkspaceID,
			ProjectID:       prj.ID,
			TaskNumber:      uint32(nextNum),
			ParentTaskID:    sql.NullInt32{},
			CreatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},
			UpdatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},
			Title:           in.Body.Title,
			Description:     desc,
			Priority:        in.Body.Priority,
			DueOn:           due,
			StartedOn:       start,
			Visibility:      vis,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Attach actors. When the caller passed no explicit actor list we
		// auto-attach them as the sole `assignee` so the task shows up on
		// their /me/tasks and /me/tasks-with-dates feeds (calendar quick-
		// create UX). An explicit non-empty list is treated as
		// authoritative — the creator is NOT merged in. See the bug at
		// docs/bugs/2026-04-23-web-calendar-quick-create-task-invisible.md
		// for the motivating flow.
		if len(in.Body.Actors) == 0 {
			actorPub := types.New()
			if _, err := qtx.AddActor(ctx, generated.AddActorParams{
				PublicID:    actorPub,
				WorkspaceID: prj.WorkspaceID,
				TaskID:      uint32(taskID),
				UserID:      sql.NullInt32{Int32: int32(actorID), Valid: true},
				Role:        generated.TaskActorsRoleAssignee,
			}); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		} else {
			for _, a := range in.Body.Actors {
				userPub, perr := types.Parse(a.UserID)
				if perr != nil {
					return nil, httpErr(apierrors.WsMemberNotFound)
				}
				uid, lerr := qtx.FindUserInternalIdByPublicId(ctx, userPub)
				if lerr != nil {
					if errors.Is(lerr, sql.ErrNoRows) {
						return nil, httpErr(apierrors.WsMemberNotFound)
					}
					return nil, httpErr(apierrors.InternalUnexpected)
				}
				role := generated.TaskActorsRoleAssignee
				if a.Role != "" {
					role = generated.TaskActorsRole(a.Role)
				}
				actorPub := types.New()
				if _, aerr := qtx.AddActor(ctx, generated.AddActorParams{
					PublicID:    actorPub,
					WorkspaceID: prj.WorkspaceID,
					TaskID:      uint32(taskID),
					UserID:      sql.NullInt32{Int32: int32(uid), Valid: true},
					Role:        role,
				}); aerr != nil {
					return nil, httpErr(apierrors.InternalUnexpected)
				}
			}
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskCreated,
			WorkspaceID: prj.WorkspaceID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskID,
			Payload: map[string]any{
				"taskId":    pub.String(),
				"projectId": prjPub.String(),
				"title":     in.Body.Title,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.Create"),
				slog.String("event_type", string(eventbus.TaskCreated)),
				logutil.LogEntityPID("project", prjPub),
				slog.String("task_public_id", pub.String()),
			)
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "task.create",
			ActorID:      actorID,
			WorkspaceID:  prj.WorkspaceID,
			ResourceType: "task",
			ResourceID:   pub.String(),
			Metadata:     map[string]any{"title": in.Body.Title, "projectId": in.Body.ProjectID},
		})
		if deps.Embedder != nil {
			// Write-time embedding upsert (ADR 0003). Failures are swallowed
			// so the task write still succeeds; the weekly reindex cron
			// picks up any rows that missed.
			_ = deps.Embedder.EmbedTask(ctx, uint32(taskID), in.Body.Title, in.Body.Description)
		}

		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: prj.WorkspaceID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &CreateTaskOutput{Body: rowToTaskFromFind(row)}, nil
	}
}

// List handles GET /tasks. When projectId is provided the list is scoped
// to that project; otherwise workspaceId must be provided.
//
// Pagination: when `cursor` is non-empty AND no filters are active AND
// the actor has admin/owner role (i.e. needsDynamicQuery == false), the
// keyset path is used and the response carries a `nextCursor`. In every
// other case the historical OFFSET path runs and `nextCursor` stays nil,
// matching the additive contract from the keyset rollout plan.
func List(deps Deps) func(context.Context, *ListTasksInput) (*ListTasksOutput, error) {
	return func(ctx context.Context, in *ListTasksInput) (*ListTasksOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		out := &ListTasksOutput{}
		out.Body.Tasks = []TaskListItem{}

		if in.ProjectID != "" {
			prjPub, err := types.Parse(in.ProjectID)
			if err != nil {
				return nil, httpErr(apierrors.WsProjectNotFound)
			}
			prj, err := deps.Queries.FindProjectByPublicIdGlobal(ctx, prjPub)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, httpErr(apierrors.WsProjectNotFound)
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			wsRoleStr, err := handlerutil.WorkspaceMemberRole(ctx, deps.DB, prj.WorkspaceID, actorID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, httpErr(apierrors.WsProjectAccessDenied)
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			wsRole := middleware.WorkspaceRole(wsRoleStr)
			pubBytes := prjPub.UUID()
			if needsDynamicQuery(in, wsRole) {
				in.Limit = limit
				frows, total, ferr := listTasksFiltered(ctx, deps.DB, prj.WorkspaceID, pubBytes[:], actorID, wsRole, in)
				if ferr != nil {
					if errors.Is(ferr, errInvalidAssignee) {
						return out, nil
					}
					return nil, httpErr(apierrors.InternalUnexpected)
				}
				for _, r := range frows {
					out.Body.Tasks = append(out.Body.Tasks, rowToTaskListItemFromWorkspace(r))
				}
				out.Body.Total = total
				return out, nil
			}
			// Keyset path — only when caller opted in via ?cursor=...
			// (an empty string conflicts with the OFFSET-default
			// behaviour, so we gate on non-empty). We fetch limit+1
			// rows and use the (limit+1)-th as a "has more" sentinel
			// so callers can stop on the page that emits a nil
			// nextCursor instead of needing one extra empty round-trip.
			if in.Cursor != "" {
				cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
				if derr != nil {
					return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
				}
				rows, qerr := deps.Queries.ListTasksForProjectKeyset(ctx, generated.ListTasksForProjectKeysetParams{
					WorkspaceID:     prj.WorkspaceID,
					ProjectPublicID: pubBytes[:],
					CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
					CursorPublicID:  cursorPID,
					Limit:           limit + 1,
				})
				if qerr != nil {
					return nil, httpErr(apierrors.InternalUnexpected)
				}
				hasMore := int32(len(rows)) > limit
				if hasMore {
					rows = rows[:limit]
				}
				for _, r := range rows {
					out.Body.Tasks = append(out.Body.Tasks, rowToTaskListItemFromProjectKeyset(r))
				}
				if hasMore {
					last := rows[len(rows)-1]
					nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
					out.Body.NextCursor = &nc
				}
				return out, nil
			}
			rows, err := deps.Queries.ListTasksForProject(ctx, generated.ListTasksForProjectParams{
				WorkspaceID:     prj.WorkspaceID,
				ProjectPublicID: pubBytes[:],
				Limit:           limit,
				Offset:          in.Offset,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			for _, r := range rows {
				out.Body.Tasks = append(out.Body.Tasks, rowToTaskListItemFromProject(r))
			}
			if len(rows) > 0 {
				out.Body.Total = totalAsInt64(rows[0].Total)
				// Bridge for first-page callers: the OFFSET path also
				// emits a nextCursor when more rows exist so callers
				// can switch to the keyset path on the second request
				// without ever needing a sentinel "first page" cursor.
				if int64(in.Offset+limit) < out.Body.Total {
					last := rows[len(rows)-1]
					nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
					out.Body.NextCursor = &nc
				}
			}
			return out, nil
		}

		if in.WorkspaceID == "" {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		wsPub, err := types.Parse(in.WorkspaceID)
		if err != nil {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		wsInternal, err := deps.Queries.GetWorkspaceIdByPublicId(ctx, wsPub)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsWorkspaceNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		wsRoleStr2, err := handlerutil.WorkspaceMemberRole(ctx, deps.DB, wsInternal, actorID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		wsRole2 := middleware.WorkspaceRole(wsRoleStr2)
		if needsDynamicQuery(in, wsRole2) {
			in.Limit = limit
			frows, total, ferr := listTasksFiltered(ctx, deps.DB, wsInternal, nil, actorID, wsRole2, in)
			if ferr != nil {
				if errors.Is(ferr, errInvalidAssignee) {
					return out, nil
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			for _, r := range frows {
				out.Body.Tasks = append(out.Body.Tasks, rowToTaskListItemFromWorkspace(r))
			}
			out.Body.Total = total
			return out, nil
		}
		// Keyset path — workspace scope, opted in via non-empty cursor.
		// limit+1 fetch lets us emit a nil nextCursor on the terminal
		// page without forcing an extra empty round-trip (see the
		// project-scope branch above for the full rationale).
		if in.Cursor != "" {
			cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
			if derr != nil {
				return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
			}
			rows, qerr := deps.Queries.ListTasksForWorkspaceKeyset(ctx, generated.ListTasksForWorkspaceKeysetParams{
				WorkspaceID:     wsInternal,
				StateFilter:     "", // empty string skips the filter (see SQL comment)
				CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
				CursorPublicID:  cursorPID,
				Limit:           limit + 1,
			})
			if qerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			hasMore := int32(len(rows)) > limit
			if hasMore {
				rows = rows[:limit]
			}
			for _, r := range rows {
				out.Body.Tasks = append(out.Body.Tasks, rowToTaskListItemFromWorkspaceKeyset(r))
			}
			if hasMore {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
			return out, nil
		}
		rows, err := deps.Queries.ListTasksForWorkspace(ctx, generated.ListTasksForWorkspaceParams{
			WorkspaceID: wsInternal,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		for _, r := range rows {
			out.Body.Tasks = append(out.Body.Tasks, rowToTaskListItemFromWorkspace(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
			if int64(in.Offset+limit) < out.Body.Total {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
		}
		return out, nil
	}
}

// Get handles GET /tasks/{id}.
func Get(deps Deps) func(context.Context, *GetTaskInput) (*GetTaskOutput, error) {
	return func(ctx context.Context, in *GetTaskInput) (*GetTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &GetTaskOutput{Body: rowToTaskFromFind(row)}, nil
	}
}

// Patch handles PATCH /tasks/{id}. derived_state is intentionally not
// writable here; the constraint engine and event bus mutate it.
func Patch(deps Deps) func(context.Context, *PatchTaskInput) (*PatchTaskOutput, error) {
	return func(ctx context.Context, in *PatchTaskInput) (*PatchTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		// Resolve the actor once so itemkit calls (which refuse a zero
		// actor) and the downstream audit entry stay consistent. The auth
		// middleware is expected to have populated this; a missing actor
		// here is a 401, not a 404.
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthTokenMissingOrMalformed)
		}
		current, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		newTitle := current.Title
		if in.Body.Title != nil && *in.Body.Title != "" {
			newTitle = *in.Body.Title
		}
		newDesc := current.Description
		if in.Body.Description != nil {
			newDesc = sql.NullString{String: *in.Body.Description, Valid: *in.Body.Description != ""}
		}
		newPriority := current.Priority
		if in.Body.Priority != nil {
			newPriority = *in.Body.Priority
		}
		newDue := current.DueOn
		if in.Body.DueOn != nil {
			parsed, err := parseDateOrNullTime(*in.Body.DueOn)
			if err != nil {
				return nil, httpErr(apierrors.ValidationBodyDateFormatInvalid)
			}
			newDue = parsed
		}
		newStart := current.StartedOn
		if in.Body.StartOn != nil {
			parsed, err := parseDateOrNullTime(*in.Body.StartOn)
			if err != nil {
				return nil, httpErr(apierrors.ValidationBodyDateFormatInvalid)
			}
			newStart = parsed
		}
		// Cross-field invariant: after applying the patch, dueOn must not
		// be earlier than startedOn. Same-day (equal) is allowed. We only
		// run the check when BOTH values would be present after the patch;
		// NULL on either side means "unconstrained" and is always valid.
		// The inputs merge the request body with the existing persisted
		// task, so the check also catches a body that touches only one
		// side but inverts the pair against the other side's stored value.
		if newDue.Valid && newStart.Valid && newDue.Time.Before(newStart.Time) {
			return nil, httpErr(apierrors.ValidationBodyDueBeforeStart)
		}
		newSortWeight := current.SortWeight
		if in.Body.SortWeight != nil {
			newSortWeight = *in.Body.SortWeight
		}
		newVisibility := current.Visibility
		if in.Body.Visibility != nil {
			newVisibility = generated.TasksVisibility(*in.Body.Visibility)
		}

		titleChanged := in.Body.Title != nil && *in.Body.Title != "" && newTitle != current.Title
		dueOnChanged := in.Body.DueOn != nil && newDue != current.DueOn

		needsItemkit := false
		if titleChanged || dueOnChanged {
			linkedCount, err := deps.Queries.CountActiveCalendarEventsByTaskId(ctx,
				sql.NullInt32{Int32: int32(task.ID), Valid: true},
			)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			needsItemkit = linkedCount > 0
		}

		updateParams := generated.UpdateTaskParams{
			Title:           newTitle,
			Description:     newDesc,
			Priority:        newPriority,
			DueOn:           newDue,
			StartedOn:       newStart,
			SortWeight:      newSortWeight,
			Visibility:      newVisibility,
			UpdatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},
			WorkspaceID:     ws.ID,
			PublicID:        types.FromUUID(task.PublicID),
		}

		if !needsItemkit {
			if err := deps.Queries.UpdateTask(ctx, updateParams); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		} else {
			tx, err := deps.DB.BeginTx(ctx, nil)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			defer tx.Rollback() //nolint:errcheck
			qtx := deps.Queries.WithTx(tx)
			if err := qtx.UpdateTask(ctx, updateParams); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			if titleChanged {
				if err := itemkit.RenameItem(ctx, tx, itemkit.RenameItemArgs{
					WorkspaceID: ws.ID,
					ActorUserID: actorID,
					TaskID:      task.ID,
					NewTitle:    newTitle,
				}); err != nil {
					return nil, translateItemkitTaskError(err)
				}
			}
			if dueOnChanged {
				snap, err := itemkit.ResolveSnapConfig(ctx, tx, ws.ID, actorID)
				if err != nil {
					return nil, httpErr(apierrors.InternalUnexpected)
				}
				var t time.Time
				if newDue.Valid {
					t = newDue.Time
				}
				if err := itemkit.RescheduleTask(ctx, tx, itemkit.RescheduleTaskArgs{
					WorkspaceID: ws.ID,
					TaskID:      task.ID,
					ActorUserID: actorID,
					SetDueOn:    true,
					DueOn:       t,
					Snap:        snap,
				}); err != nil {
					return nil, translateItemkitTaskError(err)
				}
			}
			if err := tx.Commit(); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "task.update",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "task",
			ResourceID:   task.PublicID.String(),
		})
		if deps.Embedder != nil && (in.Body.Title != nil || in.Body.Description != nil) {
			_ = deps.Embedder.EmbedTask(ctx, task.ID, newTitle, nullStr(newDesc))
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskUpdated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId": task.PublicID.String(),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.Patch"),
				slog.String("event_type", string(eventbus.TaskUpdated)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
			)
		}

		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &PatchTaskOutput{Body: rowToTaskFromFind(row)}, nil
	}
}

// translateItemkitTaskError converts an itemkit invariant into the
// public error code so PATCH /tasks returns 422 instead of 500 when
// the call violates a cross-table invariant. Thin alias over the
// shared classifier in handlerutil so the task and calendar
// translators stay in lockstep on which itemkit messages map to
// public codes.
var translateItemkitTaskError = handlerutil.TranslateTaskItemkitError

// Disable handles DELETE /tasks/{id}. The write routes through
// itemkit.DeleteTask so any linked calendar_events cascade
// soft-disabled in the same transaction.
func Disable(deps Deps) func(context.Context, *DisableTaskInput) (*DisableTaskOutput, error) {
	return func(ctx context.Context, in *DisableTaskInput) (*DisableTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, _ := middleware.ActorFromContext(ctx)
		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer tx.Rollback() //nolint:errcheck
		if err := itemkit.DeleteTask(ctx, tx, ws.ID, task.ID, actorID); err != nil {
			return nil, translateItemkitTaskError(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if actorID != 0 {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.delete",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "task",
				ResourceID:   task.PublicID.String(),
			})
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskDisabled,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId": task.PublicID.String(),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.Disable"),
				slog.String("event_type", string(eventbus.TaskDisabled)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
			)
		}
		out := &DisableTaskOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
