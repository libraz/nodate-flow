package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
)

// hasListFilters reports whether any optional filter is set on the list
// query input; it is used to choose between the sqlc fast path and the
// dynamic SQL path.
func hasListFilters(in *ListTasksInput) bool {
	return in.Q != "" || len(in.State) > 0 || in.Assignee != "" || len(in.Priority) > 0 ||
		in.DueFrom != "" || in.DueThrough != "" || in.ParentTaskID != "" || in.TopLevel
}

// needsDynamicQuery reports whether GET /tasks must use dynamic SQL.
// User-facing filters need dynamic predicates, while non-admin users
// need the runtime-spliced form of the Layer-4 visibility filter.
func needsDynamicQuery(in *ListTasksInput, wsRole middleware.WorkspaceRole) bool {
	if hasListFilters(in) {
		return true
	}
	return !wsRole.AtLeast(middleware.WorkspaceRoleAdmin)
}

// listTotal resolves the total row count behind an OFFSET page without
// making every page pay for it. A page shorter than the limit is the
// last one, so offset+len is already the exact total and count() runs
// only when the page came back full. The callback issues the matching
// Count* query; it is never invoked on the short-page path.
func listTotal(offset, limit int32, pageLen int, count func() (int64, error)) (int64, error) {
	if int32(pageLen) < limit { //#nosec G115 -- page length is capped by the LIMIT bind, which the schema caps at 200
		return int64(offset) + int64(pageLen), nil
	}
	return count()
}

// listTasksFiltered runs a dynamic SELECT against v_task_list applying the
// optional q / state / assignee / priority / due date filters. It bypasses sqlc
// because sqlc cannot express dynamic WHERE fragments. The shape of the
// returned rows matches the sqlc ListTasksForWorkspace projection so the
// existing mapper can reuse them.
//
// The total comes from a second statement rather than COUNT(*) OVER(),
// when the page came back full or an empty page has a nonzero offset.
// The window function had to
// consume every matching row, and each of those rows pulls
// v_task_list's per-row label and assignee subqueries along with it, so
// a 50-row page of a large project paid for them thousands of times
// over. A nonempty short page is the last one, so the offset plus what
// came back is the exact total. An empty first page also has an exact
// total of zero.
func listTasksFiltered(
	ctx context.Context,
	db *sql.DB,
	workspaceID uint32,
	projectPublicID []byte,
	actorID uint32,
	wsRole middleware.WorkspaceRole,
	in *ListTasksInput,
	dueFrom, dueThrough sql.NullTime,
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
	if dueFrom.Valid {
		where = append(where, "v.due_on >= ?")
		args = append(args, dueFrom.Time.Format("2006-01-02"))
	}
	if dueThrough.Valid {
		where = append(where, "v.due_on <= ?")
		args = append(args, dueThrough.Time.Format("2006-01-02"))
	}
	// Substring match, deliberately, and not the FULLTEXT index the
	// tasks table declares. MySQL's default parser leaves Japanese
	// unusable -- searching the opening word of a title matches nothing
	// -- and the ngram parser trades that for the opposite failure,
	// returning rows that do not contain the term at all. Neither
	// answers the question this box asks, so the leading-wildcard scan
	// stays until a search backend that tokenises CJK is in play.
	if in.Q != "" {
		where = append(where, "LOWER(v.title) LIKE ?")
		args = append(args, "%"+stringutil.EscapeLike(strings.ToLower(in.Q))+"%")
	}
	if len(in.State) > 0 {
		placeholders := make([]string, 0, len(in.State))
		// The enum is spelled once, in the schema, and reached here through
		// the sqlc-generated constants. A list of state names kept beside
		// the filter is a copy that no longer fails to compile when the
		// column changes, and a filter that recognises a different set of
		// states than the column stores silently drops rows.
		for _, s := range in.State {
			if !handlerutil.IsTaskDerivedState(s) {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, s)
		}
		// No recognised state: match nothing rather than dropping the
		// clause, which would widen the result to the unfiltered list.
		if len(placeholders) == 0 {
			where = append(where, "1 = 0")
		} else {
			where = append(where, "v.derived_state IN ("+strings.Join(placeholders, ",")+")")
		}
	}
	if len(in.Priority) > 0 {
		placeholders := make([]string, 0, len(in.Priority))
		// The bounds are the ones the priority scale is defined with, not a
		// pair restated next to the filter: a value outside them cannot
		// match a stored row, so it is dropped rather than bound into the
		// IN list. Two surfaces holding their own copy of the ceiling is
		// how a filter comes to reject a priority the create body accepts.
		for _, p := range in.Priority {
			if p < handlerutil.PriorityNone || p > handlerutil.PriorityMax {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, p)
		}
		// Every value was out of range: match nothing rather than
		// dropping the clause, which would widen the result to the
		// unfiltered list.
		if len(placeholders) == 0 {
			where = append(where, "1 = 0")
		} else {
			where = append(where, "v.priority IN ("+strings.Join(placeholders, ",")+")")
		}
	}
	// parentTaskId was parsed and authorized by the handler before this
	// runs; the view's parent column is the public id.
	if in.ParentTaskID != "" {
		parentPub, err := types.Parse(in.ParentTaskID)
		if err != nil {
			return nil, 0, errParentNotFound
		}
		where = append(where, "v.parent_task_public_id = ?")
		args = append(args, parentPub)
	}
	if in.TopLevel {
		where = append(where, "v.parent_task_public_id IS NULL")
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

	//#nosec G201 -- WHERE fragments are static literals composed in this file; all user-supplied values are bound via parameter placeholders.
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
  v.assignee_count
FROM v_task_list v
WHERE %s
ORDER BY v.sort_weight ASC, v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?`, strings.Join(where, " AND "))

	pageArgs := append(append([]any{}, args...), in.Limit, in.Offset)
	rows, err := db.QueryContext(ctx, query, pageArgs...)
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
		); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if int32(len(out)) < in.Limit && (len(out) > 0 || in.Offset == 0) { //#nosec G115 -- page length is capped by the LIMIT bind, which the schema caps at 200
		return out, int64(in.Offset) + int64(len(out)), nil
	}
	//#nosec G201 -- same WHERE fragments as the page query above; user values stay bound.
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM v_task_list v WHERE %s", strings.Join(where, " AND "))
	if err := db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// errInvalidAssignee is the sentinel surfaced by listTasksFiltered when
// the caller passes a malformed assignee UUID; the handler maps it to
// an empty result set so presence information never leaks. It is an
// *apierr.APIError so callers can match it via errors.Is even though
// this code path swallows the error and returns 200 with zero items.
var errInvalidAssignee = apierr.New(apierrors.ValidationQueryFieldInvalid)

var errParentNotFound = apierr.New(apierrors.WsTaskNotFound)

// checkParentFilter validates the parentTaskId / topLevel pair and, when a
// parent is named, confirms it exists in the workspace and is visible to
// the actor, answering the same not-found an unknown task id gets.
func checkParentFilter(ctx context.Context, db *sql.DB, workspaceID, actorID uint32, wsRole middleware.WorkspaceRole, in *ListTasksInput) error {
	if in.ParentTaskID != "" && in.TopLevel {
		return httpErr(apierrors.ValidationQueryFieldInvalid)
	}
	if in.ParentTaskID == "" {
		return nil
	}
	parentPub, err := types.Parse(in.ParentTaskID)
	if err != nil {
		return httpErr(apierrors.WsTaskNotFound)
	}
	where := "v.workspace_id = ? AND v.public_id = ?"
	args := []any{workspaceID, parentPub}
	if visFrag, visArgs := middleware.TaskVisibilityFilter(actorID, wsRole); visFrag != "" {
		where += " AND " + visFrag
		args = append(args, visArgs...)
	}
	var one int
	//#nosec G201 -- the fragments are static literals; user values are bound.
	q := fmt.Sprintf("SELECT 1 FROM v_task_list v WHERE %s LIMIT 1", where)
	if err := db.QueryRowContext(ctx, q, args...).Scan(&one); err != nil {
		return httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
	}
	return nil
}

func parseTaskDueRange(from, through string) (sql.NullTime, sql.NullTime, error) {
	dueFrom, err := parseDateOrNullTime(from)
	if err != nil {
		return sql.NullTime{}, sql.NullTime{}, err
	}
	dueThrough, err := parseDateOrNullTime(through)
	if err != nil {
		return sql.NullTime{}, sql.NullTime{}, err
	}
	if dueFrom.Valid && dueThrough.Valid && dueFrom.Time.After(dueThrough.Time) {
		return sql.NullTime{}, sql.NullTime{}, errors.New("dueFrom must be on or before dueThrough")
	}
	return dueFrom, dueThrough, nil
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
	page := listPage(deps)
	return func(ctx context.Context, in *ListTasksInput) (*ListTasksOutput, error) {
		out, wsID, err := page(ctx, in)
		if err != nil {
			return nil, err
		}
		if err := attachAssignees(ctx, deps.Queries, wsID, out.Body.Tasks); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return out, nil
	}
}

// listPage runs the list query for every pagination path; List decorates
// its result with assignees.
func listPage(deps Deps) func(context.Context, *ListTasksInput) (*ListTasksOutput, uint32, error) {
	return func(ctx context.Context, in *ListTasksInput) (*ListTasksOutput, uint32, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, 0, httpErr(apierrors.WsTaskAccessDenied)
		}
		dueFrom, dueThrough, err := parseTaskDueRange(in.DueFrom, in.DueThrough)
		if err != nil {
			return nil, 0, httpErr(apierrors.ValidationQueryFieldInvalid)
		}
		if in.Cursor != "" && (in.DueFrom != "" || in.DueThrough != "") {
			return nil, 0, httpErr(apierrors.ValidationQueryFieldInvalid)
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
				return nil, 0, httpErr(apierrors.WsProjectNotFound)
			}
			prj, err := deps.Queries.FindProjectByPublicIdGlobal(ctx, prjPub)
			if err != nil {
				return nil, 0, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
			}
			wsRoleStr, err := handlerutil.WorkspaceMemberRole(ctx, deps.DB, prj.WorkspaceID, actorID)
			if err != nil {
				return nil, 0, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectAccessDenied, apierrors.InternalUnexpected))
			}
			wsRole := middleware.WorkspaceRole(wsRoleStr)
			if err := checkParentFilter(ctx, deps.DB, prj.WorkspaceID, actorID, wsRole, in); err != nil {
				return nil, 0, err
			}
			prjVis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(wsRole))
			pubBytes := prjPub.UUID()
			if needsDynamicQuery(in, wsRole) {
				in.Limit = limit
				frows, total, ferr := listTasksFiltered(ctx, deps.DB, prj.WorkspaceID, pubBytes[:], actorID, wsRole, in, dueFrom, dueThrough)
				if ferr != nil {
					if errors.Is(ferr, errInvalidAssignee) {
						return out, prj.WorkspaceID, nil
					}
					return nil, 0, httpErr(apierrors.InternalUnexpected)
				}
				for _, r := range frows {
					out.Body.Tasks = append(out.Body.Tasks, rowToTaskListItemFromWorkspace(r))
				}
				out.Body.Total = total
				return out, prj.WorkspaceID, nil
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
					return nil, 0, httpErr(apierrors.ValidationQueryFieldInvalid)
				}
				rows, qerr := deps.Queries.ListTasksForProjectKeyset(ctx, generated.ListTasksForProjectKeysetParams{
					WorkspaceID:     prj.WorkspaceID,
					ProjectPublicID: pubBytes[:],
					IsElevated:      prjVis.IsElevated,
					ActorUserID:     prjVis.ActorUserID,
					CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
					CursorPublicID:  cursorPID,
					Limit:           limit + 1,
				})
				if qerr != nil {
					return nil, 0, httpErr(apierrors.InternalUnexpected)
				}
				hasMore := int32(len(rows)) > limit //#nosec G115 -- rows length capped at limit+1 with limit validated to maximum:200
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
				return out, prj.WorkspaceID, nil
			}
			rows, err := deps.Queries.ListTasksForProject(ctx, generated.ListTasksForProjectParams{
				WorkspaceID:     prj.WorkspaceID,
				ProjectPublicID: pubBytes[:],
				IsElevated:      prjVis.IsElevated,
				ActorUserID:     prjVis.ActorUserID,
				Limit:           limit,
				Offset:          in.Offset,
			})
			if err != nil {
				return nil, 0, httpErr(apierrors.InternalUnexpected)
			}
			for _, r := range rows {
				out.Body.Tasks = append(out.Body.Tasks, rowToTaskListItemFromProject(r))
			}
			if len(rows) > 0 {
				total, terr := listTotal(in.Offset, limit, len(rows), func() (int64, error) {
					return deps.Queries.CountTasksForProject(ctx, generated.CountTasksForProjectParams{
						WorkspaceID:     prj.WorkspaceID,
						ProjectPublicID: pubBytes[:],
						IsElevated:      prjVis.IsElevated,
						ActorUserID:     prjVis.ActorUserID,
					})
				})
				if terr != nil {
					return nil, 0, httpErr(apierrors.InternalUnexpected)
				}
				out.Body.Total = total
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
			return out, prj.WorkspaceID, nil
		}

		if in.WorkspaceID == "" {
			return nil, 0, httpErr(apierrors.WsWorkspaceNotFound)
		}
		wsPub, err := types.Parse(in.WorkspaceID)
		if err != nil {
			return nil, 0, httpErr(apierrors.WsWorkspaceNotFound)
		}
		wsInternal, err := deps.Queries.GetWorkspaceIdByPublicId(ctx, wsPub)
		if err != nil {
			return nil, 0, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceNotFound, apierrors.InternalUnexpected))
		}
		wsRoleStr2, err := handlerutil.WorkspaceMemberRole(ctx, deps.DB, wsInternal, actorID)
		if err != nil {
			return nil, 0, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceAccessDenied, apierrors.InternalUnexpected))
		}
		wsRole2 := middleware.WorkspaceRole(wsRoleStr2)
		if err := checkParentFilter(ctx, deps.DB, wsInternal, actorID, wsRole2, in); err != nil {
			return nil, 0, err
		}
		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(wsRole2))
		if needsDynamicQuery(in, wsRole2) {
			in.Limit = limit
			frows, total, ferr := listTasksFiltered(ctx, deps.DB, wsInternal, nil, actorID, wsRole2, in, dueFrom, dueThrough)
			if ferr != nil {
				if errors.Is(ferr, errInvalidAssignee) {
					return out, wsInternal, nil
				}
				return nil, 0, httpErr(apierrors.InternalUnexpected)
			}
			for _, r := range frows {
				out.Body.Tasks = append(out.Body.Tasks, rowToTaskListItemFromWorkspace(r))
			}
			out.Body.Total = total
			return out, wsInternal, nil
		}
		// Keyset path — workspace scope, opted in via non-empty cursor.
		// limit+1 fetch lets us emit a nil nextCursor on the terminal
		// page without forcing an extra empty round-trip (see the
		// project-scope branch above for the full rationale).
		if in.Cursor != "" {
			cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
			if derr != nil {
				return nil, 0, httpErr(apierrors.ValidationQueryFieldInvalid)
			}
			rows, qerr := deps.Queries.ListTasksForWorkspaceKeyset(ctx, generated.ListTasksForWorkspaceKeysetParams{
				WorkspaceID:     wsInternal,
				StateFilter:     "", // empty string skips the filter (see SQL comment)
				IsElevated:      vis.IsElevated,
				ActorUserID:     vis.ActorUserID,
				CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
				CursorPublicID:  cursorPID,
				Limit:           limit + 1,
			})
			if qerr != nil {
				return nil, 0, httpErr(apierrors.InternalUnexpected)
			}
			hasMore := int32(len(rows)) > limit //#nosec G115 -- rows length capped at limit+1 with limit validated to maximum:200
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
			return out, wsInternal, nil
		}

		rows, err := deps.Queries.ListTasksForWorkspace(ctx, generated.ListTasksForWorkspaceParams{
			WorkspaceID: wsInternal,
			IsElevated:  vis.IsElevated,
			ActorUserID: vis.ActorUserID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, 0, httpErr(apierrors.InternalUnexpected)
		}
		for _, r := range rows {
			out.Body.Tasks = append(out.Body.Tasks, rowToTaskListItemFromWorkspace(r))
		}
		if len(rows) > 0 {
			total, terr := listTotal(in.Offset, limit, len(rows), func() (int64, error) {
				return deps.Queries.CountTasksForWorkspace(ctx, generated.CountTasksForWorkspaceParams{
					WorkspaceID: wsInternal,
					IsElevated:  vis.IsElevated,
					ActorUserID: vis.ActorUserID,
				})
			})
			if terr != nil {
				return nil, 0, httpErr(apierrors.InternalUnexpected)
			}
			out.Body.Total = total
			if int64(in.Offset+limit) < out.Body.Total {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
		}
		return out, wsInternal, nil
	}
}
