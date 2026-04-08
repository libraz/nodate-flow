package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

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
	in *ListTasksInput,
) ([]generated.ListTasksForWorkspaceRow, int64, error) {
	var (
		where []string
		args  []any
	)
	where = append(where, "v.workspace_id = ?")
	args = append(args, workspaceID)

	if len(projectPublicID) > 0 {
		where = append(where, "v.project_public_id = ?")
		args = append(args, projectPublicID)
	}
	if in.Q != "" {
		where = append(where, "LOWER(v.title) LIKE ?")
		args = append(args, "%"+strings.ToLower(in.Q)+"%")
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
  v.parent_task_public_id,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.sort_weight,
  v.updated_at,
  v.created_at,
  v.primary_assignee_public_id,
  v.assignee_count,
  COUNT(*) OVER() AS total
FROM v_task_list v
WHERE %s
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
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
			&r.ParentTaskPublicID,
			&r.Title,
			&r.DerivedState,
			&r.Priority,
			&r.DueOn,
			&r.StartedOn,
			&r.CompletedAt,
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
// caller passes a malformed assignee UUID; the handler maps it to a 404
// so presence information never leaks.
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

func actorPtr(ctx context.Context) *int64 {
	uid, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return nil
	}
	v := int64(uid)
	return &v
}

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
		const wsMemQuery = `SELECT 1 FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
		var one int
		if err := deps.DB.QueryRowContext(ctx, wsMemQuery, prj.WorkspaceID, actorID).Scan(&one); err != nil {
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

		pub := types.New()
		desc := sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""}
		taskID, err := deps.Queries.CreateTask(ctx, generated.CreateTaskParams{
			PublicID:        pub,
			WorkspaceID:     prj.WorkspaceID,
			ProjectID:       prj.ID,
			ParentTaskID:    sql.NullInt32{},
			CreatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},
			Title:           in.Body.Title,
			Description:     desc,
			Priority:        in.Body.Priority,
			DueOn:           due,
			StartedOn:       start,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskCreated,
			WorkspaceID: prj.WorkspaceID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskID,
			Payload: map[string]any{
				"taskId":    pub.String(),
				"projectId": prjPub.String(),
				"title":     in.Body.Title,
			},
		})

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

		const wsMemQuery = `SELECT 1 FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`

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
			var one int
			if err := deps.DB.QueryRowContext(ctx, wsMemQuery, prj.WorkspaceID, actorID).Scan(&one); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, httpErr(apierrors.WsProjectAccessDenied)
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			pubBytes := prjPub.UUID()
			if hasListFilters(in) {
				in.Limit = limit
				frows, total, ferr := listTasksFiltered(ctx, deps.DB, prj.WorkspaceID, pubBytes[:], in)
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
		const wsLookup = `SELECT id FROM workspaces WHERE public_id = ? AND enabled = TRUE LIMIT 1`
		var wsInternal uint32
		if err := deps.DB.QueryRowContext(ctx, wsLookup, wsPub).Scan(&wsInternal); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsWorkspaceNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		var one int
		if err := deps.DB.QueryRowContext(ctx, wsMemQuery, wsInternal, actorID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if hasListFilters(in) {
			in.Limit = limit
			frows, total, ferr := listTasksFiltered(ctx, deps.DB, wsInternal, nil, in)
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

		if err := deps.Queries.UpdateTask(ctx, generated.UpdateTaskParams{
			Title:       newTitle,
			Description: newDesc,
			Priority:    newPriority,
			DueOn:       newDue,
			StartedOn:   newStart,
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskUpdated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId": task.PublicID.String(),
			},
		})

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

// Disable handles DELETE /tasks/{id}.
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
		if err := deps.Queries.DisableTask(ctx, generated.DisableTaskParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskDisabled,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId": task.PublicID.String(),
			},
		})
		out := &DisableTaskOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
