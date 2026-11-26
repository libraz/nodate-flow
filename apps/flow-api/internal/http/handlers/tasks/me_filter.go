package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
)

// myTasksTail is the ordering shared by every sort key. actor_role makes
// the order total for a task the caller holds in two roles.
const myTasksTail = "v.created_at DESC, v.public_id DESC, v.actor_role ASC"

// myTasksOrderBy maps the validated `sort` query value to its ORDER BY
// clause. The Huma enum rejects any other value before the handler runs.
var myTasksOrderBy = map[string]string{
	"":           "v.priority DESC, v.due_on ASC, " + myTasksTail,
	"dueOn":      "v.due_on IS NULL ASC, v.due_on ASC, v.priority DESC, " + myTasksTail,
	"-dueOn":     "v.due_on IS NULL ASC, v.due_on DESC, v.priority DESC, " + myTasksTail,
	"priority":   "v.priority ASC, v.due_on IS NULL ASC, v.due_on ASC, " + myTasksTail,
	"-priority":  "v.priority DESC, v.due_on IS NULL ASC, v.due_on ASC, " + myTasksTail,
	"updatedAt":  "COALESCE(v.updated_at, v.created_at) ASC, " + myTasksTail,
	"-updatedAt": "COALESCE(v.updated_at, v.created_at) DESC, " + myTasksTail,
	"createdAt":  "v.created_at ASC, v.public_id ASC, v.actor_role ASC",
	"-createdAt": myTasksTail,
	"title":      "v.title ASC, " + myTasksTail,
	"-title":     "v.title DESC, " + myTasksTail,
}

// hasMyTasksFilters reports whether any filter or sort field of the
// /me/tasks query is set, which selects the dynamic SQL path.
func hasMyTasksFilters(in *ListMyTasksInput) bool {
	return in.Q != "" || len(in.State) > 0 || len(in.Priority) > 0 ||
		in.WorkspaceID != "" || len(in.Role) > 0 || in.Sort != ""
}

// inClause builds an `<col> IN (?,...)` fragment, or `1 = 0` when no value
// survived validation so an all-invalid filter matches nothing instead of
// widening to the unfiltered list.
func inClause(col string, vals []any) string {
	if len(vals) == 0 {
		return "1 = 0"
	}
	return col + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(vals)), ",") + ")"
}

// listMyTasksFiltered runs the ListMyTasksGlobal projection with optional
// q / state / priority / workspaceId / role predicates and a caller-chosen
// ORDER BY. It bypasses sqlc because sqlc cannot express dynamic WHERE
// fragments, and scans into the sqlc row type so rowToMyTaskListItem is
// reused. The caller sets in.Limit before the call.
//
// The window count is kept because v_my_tasks has no per-row subqueries, so
// COUNT(*) OVER() costs one pass rather than one pass per matching row.
//
// task-visibility: not-applicable — v_my_tasks emits only rows where the reader holds an enabled task_actors row, and the caller binds its own user_public_id.
func listMyTasksFiltered(
	ctx context.Context,
	db *sql.DB,
	userPublicID types.PublicID,
	in *ListMyTasksInput,
) ([]generated.ListMyTasksGlobalRow, error) {
	where := []string{"v.user_public_id = ?"}
	args := []any{userPublicID}

	if in.Q != "" {
		where = append(where, "LOWER(v.title) LIKE ?")
		args = append(args, "%"+stringutil.EscapeLike(strings.ToLower(in.Q))+"%")
	}
	if len(in.State) > 0 {
		var vals []any
		for _, s := range in.State {
			if handlerutil.IsTaskDerivedState(s) {
				vals = append(vals, s)
			}
		}
		where = append(where, inClause("v.derived_state", vals))
		args = append(args, vals...)
	}
	if len(in.Priority) > 0 {
		var vals []any
		for _, p := range in.Priority {
			if p >= handlerutil.PriorityNone && p <= handlerutil.PriorityMax {
				vals = append(vals, p)
			}
		}
		where = append(where, inClause("v.priority", vals))
		args = append(args, vals...)
	}
	if len(in.Role) > 0 {
		var vals []any
		for _, r := range in.Role {
			if role, err := parseActorRole(r); err == nil {
				vals = append(vals, string(role))
			}
		}
		where = append(where, inClause("v.actor_role", vals))
		args = append(args, vals...)
	}
	if in.WorkspaceID != "" {
		wsPub, err := types.Parse(in.WorkspaceID)
		if err != nil {
			// Malformed id: zero rows, never an error, so presence
			// information does not leak.
			where = append(where, "1 = 0")
		} else {
			where = append(where, "w.public_id = ?")
			args = append(args, wsPub)
		}
	}

	//#nosec G201 -- WHERE fragments and ORDER BY come from static literals in this file; every user-supplied value is bound via a placeholder.
	query := fmt.Sprintf(`SELECT
  v.public_id,
  w.public_id AS workspace_public_id,
  w.name AS workspace_name,
  v.project_public_id,
  v.project_name,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.actor_role,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_my_tasks v
INNER JOIN workspaces w
  ON w.id = v.workspace_id AND w.enabled = TRUE
WHERE %s
ORDER BY %s
LIMIT ? OFFSET ?`, strings.Join(where, " AND "), myTasksOrderBy[in.Sort])

	args = append(args, in.Limit, in.Offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []generated.ListMyTasksGlobalRow{}
	for rows.Next() {
		var r generated.ListMyTasksGlobalRow
		if err := rows.Scan(
			&r.PublicID,
			&r.WorkspacePublicID,
			&r.WorkspaceName,
			&r.ProjectPublicID,
			&r.ProjectName,
			&r.Title,
			&r.DerivedState,
			&r.Priority,
			&r.DueOn,
			&r.StartedOn,
			&r.ActorRole,
			&r.UpdatedAt,
			&r.CreatedAt,
			&r.Total,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
