package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
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

// errCreateValidation is the sentinel returned from the dbretry.InTx
// callback inside Create when a validation failure (unparseable id,
// unknown member, invalid role) is encountered. It is non-transient so
// dbretry skips the retry loop; the outer handler then dispatches the
// captured validation closure to translate the failure into the right
// problem+json envelope.
var errCreateValidation = errors.New("tasks.Create: validation failed")

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
	return in.Q != "" || len(in.State) > 0 || in.Assignee != ""
}

// needsDynamicQuery reports whether GET /tasks must go through the
// dynamic SQL path instead of the sqlc-generated fast path.
//
// The dynamic path is required for two non-overlapping reasons:
//   - the caller passed at least one of the user-facing filters
//     (q / state / assignee), which sqlc cannot express;
//   - the caller is not a workspace admin or owner, so the Layer-4
//     task visibility filter must be appended at runtime — again
//     beyond what the static sqlc queries express.
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
		title := strings.TrimSpace(in.Body.Title)
		if title == "" {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
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
		// Cross-field invariant: when both dueOn and startedOn are
		// provided, dueOn must not be earlier than startedOn. Same-day
		// (equal) is allowed; NULL on either side means "unconstrained".
		if due.Valid && start.Valid && due.Time.Before(start.Time) {
			return nil, httpErr(apierrors.ValidationBodyDueBeforeStart)
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
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.Create", nil, func(ctx context.Context, tx *sql.Tx) error {
			validationFn = nil
			qtx := deps.Queries.WithTx(tx)

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
			// timeline/audit row (L-14). On a deadlock retry the whole tx
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
					"title":     title,
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
			Metadata:     map[string]any{"title": title, "projectId": in.Body.ProjectID},
		})
		if deps.Embedder != nil {
			// Write-time embedding upsert (ADR 0003). Failures are swallowed
			// so the task write still succeeds; the weekly reindex cron
			// picks up any rows that missed.
			_ = deps.Embedder.EmbedTask(ctx, prj.WorkspaceID, uint32(taskID), title, in.Body.Description) //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
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
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
			}
			wsRoleStr, err := handlerutil.WorkspaceMemberRole(ctx, deps.DB, prj.WorkspaceID, actorID)
			if err != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectAccessDenied, apierrors.InternalUnexpected))
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceNotFound, apierrors.InternalUnexpected))
		}
		wsRoleStr2, err := handlerutil.WorkspaceMemberRole(ctx, deps.DB, wsInternal, actorID)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceAccessDenied, apierrors.InternalUnexpected))
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

		newTitle := current.Title
		if in.Body.Title != nil {
			trimmedTitle := strings.TrimSpace(*in.Body.Title)
			if trimmedTitle == "" {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			newTitle = trimmedTitle
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
				sql.NullInt32{Int32: int32(task.ID), Valid: true}, //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
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

		if !needsItemkit {
			if err := deps.Queries.UpdateTask(ctx, updateParams); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			// No tx in scope on this path; append best-effort.
			if err := eventbus.Append(ctx, deps.DB, updateEvent); err != nil {
				nflog.LoggerFromContext(ctx).ErrorContext(ctx, "eventbus.Append failed",
					slog.Any("err", err),
					slog.String("handler", "tasks.Patch"),
					slog.String("event_type", string(eventbus.TaskUpdated)),
					logutil.LogEntity("workspace", ws.PublicID),
					logutil.LogEntity("task", task.PublicID),
				)
			}
		} else {
			// dbretry.InTx rather than a hand-rolled transaction: the
			// lifecycle event is appended inside it, and the eventbus
			// only has a commit boundary to defer its fan-out to when
			// the transaction came from here.
			var answered error
			txErr := dbretry.InTx(ctx, deps.DB, "tasks.Patch", nil, func(ctx context.Context, tx *sql.Tx) error {
				answered = nil
				qtx := deps.Queries.WithTx(tx)
				if err := qtx.UpdateTask(ctx, updateParams); err != nil {
					return err
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
		if deps.Embedder != nil && (in.Body.Title != nil || in.Body.Description != nil) {
			_ = deps.Embedder.EmbedTask(ctx, ws.ID, task.ID, newTitle, nullStr(newDesc))
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
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.Disable", nil, func(ctx context.Context, tx *sql.Tx) error {
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
		// risked losing it on a crash between commit and append (L-14).
		out := &DisableTaskOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
