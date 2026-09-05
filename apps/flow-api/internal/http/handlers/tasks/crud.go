package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskdesc"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
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

// maxTaskPriority is the inclusive ceiling of tasks.priority as the
// OpenAPI schema declares it on both the create body and the list
// filter. Values outside the range cannot match a stored row, so the
// list filter drops them instead of binding them into the IN list.
const maxTaskPriority = 4

// errCreateValidation is the sentinel returned from the dbretry.InTx
// callback inside Create when a validation failure (unparseable id,
// unknown member, invalid role) is encountered. It is non-transient so
// dbretry skips the retry loop; the outer handler then dispatches the
// captured validation closure to translate the failure into the right
// problem+json envelope.
var errCreateValidation = errors.New("tasks.Create: validation failed")

// translateTaskRuleError renders a [taskrules] refusal as the
// problem+json envelope the REST contract answers for it. The rules
// package states what was violated and stops there, because MCP answers
// the same violations differently; this is the REST half of that split.
func translateTaskRuleError(err error) error {
	switch taskrules.Classify(err) {
	case taskrules.ViolationNone:
		return nil
	case taskrules.ViolationTitleEmpty:
		return httpErr(apierrors.ValidationBodyFieldInvalid)
	case taskrules.ViolationDueBeforeStart:
		return httpErr(apierrors.ValidationBodyDueBeforeStart)
	}
	return httpErr(apierrors.InternalUnexpected)
}

// translateActorRoleError converts an apierror returned by
// [parseActorRole] into the canonical problem+json envelope so the
// `received_role` detail surfaces through ProblemDetails extensions.
// Falls back to a generic WS.TASK.ACTOR_ROLE_INVALID envelope when the
// caller hands in something other than an *apierr.APIError, which
// shouldn't happen in practice but keeps the helper total.
func translateActorRoleError(err error) error {
	var apiErr *apierr.APIError
	if errors.As(err, &apiErr) {
		return handlerutil.HTTPErrFromAPIError(apiErr)
	}
	return httpErr(apierrors.WsTaskActorRoleInvalid)
}

// parseActorRole validates the `role` field on a task-actor body and
// converts it to the sqlc-generated enum type. The OpenAPI schema
// already restricts the field to the catalog enum, but the Huma
// validator only runs on declared inputs — older clients or
// hand-rolled JSON could still slip an unknown role through to the
// SQL boundary, where MySQL would emit a 1265 "Data truncated" error
// and the handler would surface it as a generic 500.
//
// Returning a typed apierror means the same payload from any code
// path (CRUD create, AddActor, AddAgentActor) lands on
// WS.TASK.ACTOR_ROLE_INVALID with HTTP 422 and the offending value
// echoed back in `received_role`.
func parseActorRole(s string) (generated.TaskActorsRole, error) {
	switch generated.TaskActorsRole(s) {
	case generated.TaskActorsRoleAssignee,
		generated.TaskActorsRoleReviewer,
		generated.TaskActorsRoleWatcher,
		generated.TaskActorsRoleApprover:
		return generated.TaskActorsRole(s), nil
	default:
		return "", apierr.New(apierrors.WsTaskActorRoleInvalid).WithDetail("received_role", s)
	}
}

// hasListFilters reports whether any optional filter is set on the list
// query input; it is used to choose between the sqlc fast path and the
// dynamic SQL path.
func hasListFilters(in *ListTasksInput) bool {
	return in.Q != "" || len(in.State) > 0 || in.Assignee != "" || len(in.Priority) > 0
}

// needsDynamicQuery reports whether GET /tasks must go through the
// dynamic SQL path instead of the sqlc-generated fast path.
//
// The dynamic path is required for two non-overlapping reasons:
//   - the caller passed at least one of the user-facing filters
//     (q / state / assignee), which sqlc cannot express;
//   - the caller is not a workspace admin or owner, so the list is
//     routed through the runtime-spliced form of the Layer-4 filter.
//
// The static sqlc queries carry the same predicate, so the second
// condition is a routing choice rather than the thing that keeps a
// task the caller may not see off the wire.
//
// The helper is kept (and not inlined into both call sites) because
// the combined predicate covers two filter sources and four boolean
// sub-clauses; inlining at both call sites would duplicate the
// rationale comment and invite drift between the project-scope and
// workspace-scope branches.
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
// optional q / state / assignee / priority filters. It bypasses sqlc
// because sqlc cannot express dynamic WHERE fragments. The shape of the
// returned rows matches the sqlc ListTasksForWorkspace projection so the
// existing mapper can reuse them.
//
// The total comes from a second statement rather than COUNT(*) OVER(),
// and only when the page came back full. The window function had to
// consume every matching row, and each of those rows pulls
// v_task_list's per-row label and assignee subqueries along with it, so
// a 50-row page of a large project paid for them thousands of times
// over. A short page needs no statement at all: it is the last one, so
// the offset plus what came back is the exact total.
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
	if len(in.Priority) > 0 {
		placeholders := make([]string, 0, len(in.Priority))
		for _, p := range in.Priority {
			if p < 0 || p > maxTaskPriority {
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
	if int32(len(out)) < in.Limit { //#nosec G115 -- page length is capped by the LIMIT bind, which the schema caps at 200
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
		// Checked before the project lookup: a blank title with an
		// unknown project id is answered as a validation error, and
		// moving the check past the lookup would answer not-found.
		title, err := taskrules.NewTitle(in.Body.Title)
		if err != nil {
			return nil, translateTaskRuleError(err)
		}
		prjPub, err := types.Parse(in.Body.ProjectID)
		if err != nil {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		prj, err := deps.Queries.FindProjectByPublicIdGlobal(ctx, prjPub)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
		}
		// Project editor check (handler-level since /tasks has no workspace
		// path parameter to attach RequireProjectRole to).
		if spec := requireProjectEditor(ctx, deps.DB, prj.WorkspaceID, prj.ID, actorID); spec != nil {
			return nil, httpErr(spec)
		}

		due, err := parseDateOrNullTime(in.Body.DueOn)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyDateFormatInvalid)
		}
		start, err := parseDateOrNullTime(in.Body.StartOn)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyDateFormatInvalid)
		}
		if err := taskrules.DateOrder(due, start); err != nil {
			return nil, translateTaskRuleError(err)
		}

		var pub types.PublicID
		desc := sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""}

		// Run the create transaction inside a deadlock-aware retry
		// wrapper. The tx body acquires FK record locks against
		// workspaces, projects, users, and the project-row lock used by
		// task-number allocation, so it can deadlock with concurrent
		// transitions and fan-out under heavy parallel test load.
		// Restarting the whole tx on ER_LOCK_DEADLOCK is the standard
		// MySQL recipe.
		var (
			taskID       int64
			validationFn func() error
		)
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.Create", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			validationFn = nil
			qtx := deps.Queries.WithTx(tx.RawTx())

			created, err := taskcreate.New(ctx, tx, taskcreate.Args{
				WorkspaceID: prj.WorkspaceID,
				ProjectID:   prj.ID,
				ActorUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
				Title:       title,
				Description: desc,
				Priority:    in.Body.Priority,
				DueOn:       due,
				StartedOn:   start,
				// Empty body visibility means "workspace default"; taskcreate
				// substitutes it so the default lives in one place.
				Visibility: generated.TasksVisibility(in.Body.Visibility),
			})
			if err != nil {
				if errors.Is(err, taskcreate.ErrVisibilityInvalid) {
					validationFn = func() error { return httpErr(apierrors.ValidationBodyFieldInvalid) }
					return errCreateValidation
				}
				return err
			}
			tID := created.ID
			// The public id is minted per attempt, so a retried transaction
			// never commits an id a previous attempt already published.
			pub = created.PublicID
			taskID = tID

			// Attach actors. When the caller passed no explicit actor
			// list we auto-attach them as the sole `assignee` so the
			// task shows up on their /me/tasks feeds. An explicit
			// non-empty list is treated as authoritative.
			if len(in.Body.Actors) == 0 {
				actorPub := types.New()
				if _, err := qtx.AddActor(ctx, generated.AddActorParams{
					PublicID:    actorPub,
					WorkspaceID: prj.WorkspaceID,
					TaskID:      uint32(tID),                                       //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
					UserID:      sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
					Role:        generated.TaskActorsRoleAssignee,
				}); err != nil {
					return err
				}
			}
			for _, a := range in.Body.Actors {
				userPub, perr := types.Parse(a.UserID)
				if perr != nil {
					validationFn = func() error { return httpErr(apierrors.WsMemberNotFound) }
					return errCreateValidation
				}
				// Resolve scoped to the task's workspace so an explicit actor
				// list cannot attach a user from another tenant.
				uid, lerr := qtx.FindWorkspaceMemberUserInternalIdByPublicId(ctx, generated.FindWorkspaceMemberUserInternalIdByPublicIdParams{
					WorkspaceID: prj.WorkspaceID,
					PublicID:    userPub,
				})
				if lerr != nil {
					if errors.Is(lerr, sql.ErrNoRows) {
						validationFn = func() error { return httpErr(apierrors.WsMemberNotFound) }
						return errCreateValidation
					}
					return lerr
				}
				role := generated.TaskActorsRoleAssignee
				if a.Role != "" {
					parsed, perr := parseActorRole(a.Role)
					if perr != nil {
						capturedErr := perr
						validationFn = func() error { return translateActorRoleError(capturedErr) }
						return errCreateValidation
					}
					role = parsed
				}
				actorPub := types.New()
				if _, aerr := qtx.AddActor(ctx, generated.AddActorParams{
					PublicID:    actorPub,
					WorkspaceID: prj.WorkspaceID,
					TaskID:      uint32(tID),                                   //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
					UserID:      sql.NullInt32{Int32: int32(uid), Valid: true}, //#nosec G115 -- user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
					Role:        role,
				}); aerr != nil {
					return aerr
				}
			}
			// Append the lifecycle event inside the same tx so a crash
			// between commit and a post-commit append cannot lose the
			// timeline/audit row. On a deadlock retry the whole tx
			// (including this append) rolls back and re-runs, so no
			// duplicate row is committed.
			if err := eventbus.Append(ctx, tx, eventbus.Event{
				Type:        eventbus.TaskCreated,
				WorkspaceID: prj.WorkspaceID,
				ActorUserID: actorPtr(ctx),
				TaskID:      &taskID,
				Payload: map[string]any{
					"taskId":    pub.String(),
					"projectId": prjPub.String(),
					"title":     title.String(),
				},
			}); err != nil {
				return err
			}
			return nil
		})
		if validationFn != nil {
			return nil, validationFn()
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "task.create",
			ActorID:      actorID,
			WorkspaceID:  prj.WorkspaceID,
			ResourceType: "task",
			ResourceID:   pub.String(),
			Metadata:     map[string]any{"title": title.String(), "projectId": in.Body.ProjectID},
		})
		embed.RefreshTaskAfterCommit(ctx, deps.Embedder, prj.WorkspaceID, uint32(taskID), title.String(), in.Body.Description) //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments

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
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
			}
			wsRoleStr, err := handlerutil.WorkspaceMemberRole(ctx, deps.DB, prj.WorkspaceID, actorID)
			if err != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectAccessDenied, apierrors.InternalUnexpected))
			}
			wsRole := middleware.WorkspaceRole(wsRoleStr)
			prjVis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(wsRole))
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
					IsElevated:      prjVis.IsElevated,
					ActorUserID:     prjVis.ActorUserID,
					ActorUserID_2:   prjVis.ActorUserID,
					ActorUserID_3:   prjVis.ActorUserID,
					CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
					CursorPublicID:  cursorPID,
					Limit:           limit + 1,
				})
				if qerr != nil {
					return nil, httpErr(apierrors.InternalUnexpected)
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
				return out, nil
			}
			rows, err := deps.Queries.ListTasksForProject(ctx, generated.ListTasksForProjectParams{
				WorkspaceID:     prj.WorkspaceID,
				ProjectPublicID: pubBytes[:],
				IsElevated:      prjVis.IsElevated,
				ActorUserID:     prjVis.ActorUserID,
				ActorUserID_2:   prjVis.ActorUserID,
				ActorUserID_3:   prjVis.ActorUserID,
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
				total, terr := listTotal(in.Offset, limit, len(rows), func() (int64, error) {
					return deps.Queries.CountTasksForProject(ctx, generated.CountTasksForProjectParams{
						WorkspaceID:     prj.WorkspaceID,
						ProjectPublicID: pubBytes[:],
						IsElevated:      prjVis.IsElevated,
						ActorUserID:     prjVis.ActorUserID,
						ActorUserID_2:   prjVis.ActorUserID,
						ActorUserID_3:   prjVis.ActorUserID,
					})
				})
				if terr != nil {
					return nil, httpErr(apierrors.InternalUnexpected)
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceNotFound, apierrors.InternalUnexpected))
		}
		wsRoleStr2, err := handlerutil.WorkspaceMemberRole(ctx, deps.DB, wsInternal, actorID)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceAccessDenied, apierrors.InternalUnexpected))
		}
		wsRole2 := middleware.WorkspaceRole(wsRoleStr2)
		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(wsRole2))
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
				IsElevated:      vis.IsElevated,
				ActorUserID:     vis.ActorUserID,
				ActorUserID_2:   vis.ActorUserID,
				ActorUserID_3:   vis.ActorUserID,
				CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
				CursorPublicID:  cursorPID,
				Limit:           limit + 1,
			})
			if qerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
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
			return out, nil
		}

		rows, err := deps.Queries.ListTasksForWorkspace(ctx, generated.ListTasksForWorkspaceParams{
			WorkspaceID:   wsInternal,
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
			out.Body.Tasks = append(out.Body.Tasks, rowToTaskListItemFromWorkspace(r))
		}
		if len(rows) > 0 {
			total, terr := listTotal(in.Offset, limit, len(rows), func() (int64, error) {
				return deps.Queries.CountTasksForWorkspace(ctx, generated.CountTasksForWorkspaceParams{
					WorkspaceID:   wsInternal,
					IsElevated:    vis.IsElevated,
					ActorUserID:   vis.ActorUserID,
					ActorUserID_2: vis.ActorUserID,
					ActorUserID_3: vis.ActorUserID,
				})
			})
			if terr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			out.Body.Total = total
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
	return func(ctx context.Context, _ *GetTaskInput) (*GetTaskOutput, error) {
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
		}

		newTitle, err := taskrules.PatchTitle(current.Title, in.Body.Title)
		if err != nil {
			return nil, translateTaskRuleError(err)
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
		// The merged pair, not the body: a patch that moves only one
		// side still has to face the other side's stored value.
		if err := taskrules.DateOrder(newDue, newStart); err != nil {
			return nil, translateTaskRuleError(err)
		}
		newSortWeight := current.SortWeight
		if in.Body.SortWeight != nil {
			newSortWeight = *in.Body.SortWeight
		}
		newVisibility := current.Visibility
		if in.Body.Visibility != nil {
			newVisibility = generated.TasksVisibility(*in.Body.Visibility)
		}

		titleChanged := in.Body.Title != nil && newTitle.String() != current.Title
		dueOnChanged := in.Body.DueOn != nil && newDue != current.DueOn
		// Against the stored value, not the presence of the field: a client
		// that round-trips the whole task re-sends a description it never
		// edited, and a version is a body the task has held rather than a
		// record of who sent what.
		descChanged := in.Body.Description != nil && newDesc != current.Description

		needsItemkit := false
		if titleChanged || dueOnChanged {
			linkedCount, err := deps.Queries.CountActiveCalendarEventsByTaskId(ctx,
				sql.NullInt32{Int32: int32(task.ID), Valid: true}, //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			needsItemkit = linkedCount > 0
		}

		updateParams := generated.UpdateTaskParams{
			Title:           newTitle.String(),
			Description:     newDesc,
			Priority:        newPriority,
			DueOn:           newDue,
			StartedOn:       newStart,
			SortWeight:      newSortWeight,
			Visibility:      newVisibility,
			UpdatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
			WorkspaceID:     ws.ID,
			PublicID:        types.FromUUID(task.PublicID),
		}

		taskInternal := int64(task.ID)
		updateEvent := eventbus.Event{
			Type:        eventbus.TaskUpdated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId": task.PublicID.String(),
			},
		}

		// Not an existence check on either branch: a PATCH that re-sends
		// the task's current values changes nothing and MySQL counts zero.
		// The task was resolved by the task middleware before this runs.
		if !needsItemkit {
			// Same transaction boundary as the itemkit branch below. The
			// UPDATE and the timeline row have to commit together: this
			// path used to append after the write with no transaction in
			// scope, so a crash in between left a task whose change had
			// landed and whose timeline never recorded it — and the
			// append error was only logged, which made the loss silent.
			if err := dbretry.InTx(ctx, deps.DB, "tasks.Patch", nil, func(ctx context.Context, tx *dbretry.Tx) error {
				qtx := deps.Queries.WithTx(tx.RawTx())
				if _, err := qtx.UpdateTask(ctx, updateParams); err != nil {
					return err
				}
				// Inside the transaction that writes the description: a body
				// that commits without its snapshot is one no restore can
				// return to.
				if descChanged {
					if _, err := taskdesc.Snapshot(ctx, qtx, ws.ID, task.ID, updateParams.UpdatedByUserID, newDesc.String); err != nil {
						return err
					}
				}
				return eventbus.Append(ctx, tx, updateEvent)
			}); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		} else {
			// dbretry.InTx rather than a hand-rolled transaction: the
			// lifecycle event is appended inside it, and the eventbus
			// only has a commit boundary to defer its fan-out to when
			// the transaction came from here.
			var answered error
			txErr := dbretry.InTx(ctx, deps.DB, "tasks.Patch", nil, func(ctx context.Context, tx *dbretry.Tx) error {
				answered = nil
				qtx := deps.Queries.WithTx(tx.RawTx())
				if _, err := qtx.UpdateTask(ctx, updateParams); err != nil {
					return err
				}
				// Same reason as the branch above: the snapshot shares the
				// transaction that writes the description.
				if descChanged {
					if _, err := taskdesc.Snapshot(ctx, qtx, ws.ID, task.ID, updateParams.UpdatedByUserID, newDesc.String); err != nil {
						return err
					}
				}
				if titleChanged {
					if err := itemkit.RenameItem(ctx, tx, itemkit.RenameItemArgs{
						WorkspaceID: ws.ID,
						ActorUserID: actorID,
						TaskID:      task.ID,
						NewTitle:    newTitle,
					}); err != nil {
						answered = translateItemkitTaskError(err)
						return err
					}
				}
				if dueOnChanged {
					snap, err := itemkit.ResolveSnapConfig(ctx, tx, ws.ID, actorID)
					if err != nil {
						return err
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
						answered = translateItemkitTaskError(err)
						return err
					}
				}
				// Append the lifecycle event inside the tx so a crash between
				// commit and a post-commit append cannot lose the timeline row.
				return eventbus.Append(ctx, tx, updateEvent)
			})
			if answered != nil {
				return nil, answered
			}
			if txErr != nil {
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
		// Only text that actually moved invalidates the vector: a refresh
		// costs a provider call the workspace pays for.
		textChanged := titleChanged || descChanged
		if textChanged {
			embed.RefreshTaskAfterCommit(ctx, deps.Embedder, ws.ID, task.ID, newTitle.String(), nullStr(newDesc))
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
	return func(ctx context.Context, _ *DisableTaskInput) (*DisableTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, _ := middleware.ActorFromContext(ctx)
		// Wrap the delete in dbretry.InTx: itemkit.DeleteTask appends
		// item.deleted (and a legacy task.disabled) event row inside
		// the same tx, which competes for FK locks with concurrent
		// transitions and fan-out goroutines under heavy parallel
		// load. Restarting the whole transaction on ER_LOCK_DEADLOCK
		// is the canonical MySQL recipe.
		//
		// We pass the raw error through to dbretry so it can detect
		// the transient mysql code; only after the retry budget is
		// exhausted (or the error is non-transient) do we translate
		// the result into a problem+json envelope.
		var rawErr error
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.Disable", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			rawErr = nil
			if err := itemkit.DeleteTask(ctx, tx, ws.ID, task.ID, actorID); err != nil {
				rawErr = err
				return err
			}
			return nil
		})
		if txErr != nil {
			if rawErr != nil {
				return nil, translateItemkitTaskError(rawErr)
			}
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
		// No task.disabled append here: itemkit.DeleteTask already emits
		// item.deleted plus its legacy task.disabled dual-emit inside the
		// tx above (see itemkit.legacyKindFor). A second post-commit append
		// here duplicated the task.disabled timeline row for one delete and
		// risked losing it on a crash between commit and append.
		out := &DisableTaskOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
