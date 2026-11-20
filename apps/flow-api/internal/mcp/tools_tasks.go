package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint/engine"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskdesc"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
)

func runListProjects(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Limit  int32 `json:"limit"`
		Offset int32 `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	wsRole, err := requireWorkspaceMember(ctx, deps, s)
	if err != nil {
		return nil, err
	}
	rows, err := deps.Queries.ListProjectsForWorkspace(ctx, generated.ListProjectsForWorkspaceParams{
		WorkspaceID: s.workspaceID,
		Limit:       in.Limit,
		Offset:      in.Offset,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// The REST listing's rule: owners and admins see every project, anyone
	// else only the projects they hold an enabled project_members row on.
	var allowed map[types.PublicID]bool
	if !wsRole.AtLeast(acl.WorkspaceRoleAdmin) {
		ids, err := deps.Queries.ListProjectPublicIdsForUserInWorkspace(ctx, generated.ListProjectPublicIdsForUserInWorkspaceParams{
			WorkspaceID: s.workspaceID,
			UserID:      s.userID,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		allowed = make(map[types.PublicID]bool, len(ids))
		for _, id := range ids {
			allowed[id] = true
		}
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		if allowed != nil && !allowed[r.PublicID] {
			continue
		}
		items = append(items, map[string]any{
			"id":   r.PublicID.String(),
			"slug": r.Slug,
			"name": r.Name,
		})
	}
	// hasMore is the truncation signal: the window is applied before the
	// membership filter, so a short page does not mean the end.
	hasMore := len(rows) > 0 && int64(in.Offset)+int64(len(rows)) < handlerutil.TotalAsInt64(rows[0].Total)
	return map[string]any{"projects": items, "hasMore": hasMore}, nil
}
func runListTasks(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ProjectID string `json:"projectId"`
		Limit     int32  `json:"limit"`
		Offset    int32  `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	wsRole, err := requireWorkspaceMember(ctx, deps, s)
	if err != nil {
		return nil, err
	}
	var projectPublicID []byte
	if in.ProjectID != "" {
		prjPub, err := types.Parse(in.ProjectID)
		if err != nil {
			return nil, apierrors.New(apierrors.WsProjectNotFound)
		}
		if _, err := resolveProject(ctx, deps, s, in.ProjectID); err != nil {
			return nil, err
		}
		pb := prjPub.UUID()
		projectPublicID = pb[:]
	}
	rows, err := listMCPTasks(ctx, deps.DB, s.workspaceID, projectPublicID, s.userID, wsRole, in.Limit, in.Offset)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, taskListRowToMap(r.publicID, r.title, r.derivedState, r.priority, r.dueOn))
	}
	return map[string]any{"tasks": items}, nil
}

type mcpTaskListRow struct {
	publicID     types.PublicID
	title        string
	derivedState string
	priority     int32
	dueOn        sql.NullTime
}

func listMCPTasks(
	ctx context.Context,
	db *sql.DB,
	workspaceID uint32,
	projectPublicID []byte,
	actorID uint32,
	wsRole acl.WorkspaceRole,
	limit int32,
	offset int32,
) ([]mcpTaskListRow, error) {
	where := []string{"v.workspace_id = ?"}
	args := []any{workspaceID}
	if len(projectPublicID) > 0 {
		where = append(where, "v.project_public_id = ?")
		args = append(args, projectPublicID)
	}
	if visFrag, visArgs := acl.TaskVisibilityFilter(actorID, wsRole); visFrag != "" {
		where = append(where, visFrag)
		args = append(args, visArgs...)
	}

	//#nosec G201 -- WHERE fragments are static literals; user values are bound.
	query := fmt.Sprintf(`SELECT
  v.public_id,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on
FROM v_task_list v
WHERE %s
ORDER BY v.sort_weight ASC, v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?`, strings.Join(where, " AND "))
	args = append(args, limit, offset)

	// no-generated-query: the WHERE clause is not fixed. acl.TaskVisibilityFilter
	// returns a different fragment, with a different number of binds, depending on
	// the caller's workspace role, and sqlc compiles one statement per query. A
	// static version would have to spell the visibility predicate out a second
	// time, which is how a listing ends up projecting rows the caller may not see.
	// Replacing this needs a way to compose the filter into a generated statement,
	// not another copy of the predicate.
	rows, err := db.QueryContext(ctx, query, args...) //#nosec G701 -- query is assembled from static WHERE fragments; all user values are bound args.
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []mcpTaskListRow{}
	for rows.Next() {
		var r mcpTaskListRow
		if err := rows.Scan(&r.publicID, &r.title, &r.derivedState, &r.priority, &r.dueOn); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func taskListRowToMap(pub types.PublicID, title, state string, priority int32, due sql.NullTime) map[string]any {
	m := map[string]any{
		"id":           pub.String(),
		"title":        title,
		"derivedState": state,
		"priority":     priority,
	}
	if due.Valid {
		m["dueOn"] = due.Time.UTC().Format("2006-01-02")
	}
	return m
}
func runGetTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	_, row, err := resolveTaskRow(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"id":           row.PublicID.String(),
		"title":        row.Title,
		"derivedState": string(row.DerivedState),
		"priority":     row.Priority,
		"projectName":  row.ProjectName,
	}
	if row.Description.Valid {
		out["description"] = row.Description.String
	}
	if row.DueOn.Valid {
		out["dueOn"] = row.DueOn.Time.UTC().Format("2006-01-02")
	}
	return out, nil
}
func runCreateTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ProjectID   string `json:"projectId"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    int32  `json:"priority"`
		DueOn       string `json:"dueOn"`
		StartOn     string `json:"startOn"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	title, err := taskrules.NewTitle(in.Title)
	if err != nil {
		return nil, translateTaskRuleError(err)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	prjID, err := resolveProjectForWrite(ctx, deps, s, in.ProjectID)
	if err != nil {
		return nil, err
	}
	due, err := parseDateOrNull(in.DueOn)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	start, err := parseDateOrNull(in.StartOn)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if err := taskrules.DateOrder(due, start); err != nil {
		return nil, translateTaskRuleError(err)
	}
	var (
		pub    types.PublicID
		taskID int64
	)
	if txErr := dbretry.InTx(ctx, deps.DB, "mcp.create_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		// Attributed to the person whose token made the call, and to
		// nobody as assignee.
		//
		// Both halves are about what somebody opening the task later
		// sees. The creator line names a colleague they can go and ask
		// what this is for, which is the whole value of the field; the
		// alternative, a task with no creator at all, reads as something
		// the system produced and leaves nobody to ask. And the empty
		// assignee list is the honest answer to "who is doing this": a
		// tool call is a request for the work to exist, not an agreement
		// by anyone to carry it out, so filling that line in would put
		// the caller's name on a queue they never accepted and hide the
		// task from anyone triaging unassigned work.
		created, err := taskcreate.New(ctx, tx, taskcreate.AuthoredBy(s.userID), taskcreate.Args{
			WorkspaceID: s.workspaceID,
			ProjectID:   prjID,
			Title:       title,
			Description: sql.NullString{String: in.Description, Valid: in.Description != ""},
			Priority:    in.Priority,
			DueOn:       due,
			StartedOn:   start,
		})
		if err != nil {
			return err
		}
		pub = created.PublicID
		taskID = created.ID
		// The lifecycle event commits with the task; the audit row waits
		// for the commit.
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.TaskCreated,
			AuditAction:  "task.create",
			ResourceType: "task",
			ResourceID:   pub.String(),
			TaskID:       &taskID,
			Payload: map[string]any{
				"taskId": pub.String(),
				"title":  title.String(),
				"via":    "mcp",
			},
			CallSite: "mcp.create_task",
		})
	}); txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}
	noteInvocationTask(ctx, uint32(taskID)) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	// The embedded text is the stored text. The rule trims the title before
	// the insert, so embedding the submitted string would index a padded
	// string against a row holding the trimmed one.
	embed.RefreshTaskAfterCommit(ctx, deps.Embedder, s.workspaceID, uint32(taskID), title.String(), in.Description) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	return map[string]any{"id": pub.String()}, nil
}

// runUpdateTask changes a task's mutable fields.
//
// The tool takes dates, not a window. When the task carries a projection
// event, itemkit mirrors the new date onto it by re-anchoring the event's
// own duration — the stored end minus the stored start, which the
// database's own chronology CHECK keeps non-negative — to that date, and
// shifts both ends together for a working-day snap. So the pair written
// is ordered by construction and holds nothing the caller sent:
//
// calendar-precondition: chronology not-applicable — this tool sends a date and no window, and the pair it mirrors keeps the stored event's own duration
func runUpdateTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID      string  `json:"taskId"`
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Priority    *int32  `json:"priority"`
		DueOn       *string `json:"dueOn"`
		StartOn     *string `json:"startOn"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	// Dates are parsed before the transaction; everything merged against
	// the stored row happens inside it.
	var argDue, argStart sql.NullTime
	if in.DueOn != nil {
		p, err := parseDateOrNull(*in.DueOn)
		if err != nil {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		argDue = p
	}
	if in.StartOn != nil {
		p, err := parseDateOrNull(*in.StartOn)
		if err != nil {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		argStart = p
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	taskInternal, pub, err := resolveTaskForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}

	taskID64 := int64(taskInternal)
	var (
		title        taskrules.Title
		desc         sql.NullString
		titleChanged bool
		descChanged  bool
		answered     error
	)
	// One transaction for the merge and the write, as REST PATCH does. The
	// row is locked before it is read, so the merge starts from what the
	// previous writer committed and a concurrent edit of another field is
	// kept rather than overwritten with a value read before it landed.
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.update_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		qtx := deps.Queries.WithTx(tx.RawTx())
		if _, err := qtx.LockTaskForTransition(ctx, generated.LockTaskForTransitionParams{
			ID:          taskInternal,
			WorkspaceID: s.workspaceID,
		}); err != nil {
			if stderrors.Is(err, sql.ErrNoRows) {
				answered = apierrors.New(apierrors.WsTaskNotFound)
			}
			return err
		}
		current, err := loadTaskRow(ctx, qtx, s.workspaceID, pub)
		if err != nil {
			if stderrors.Is(err, sql.ErrNoRows) {
				answered = apierrors.New(apierrors.WsTaskNotFound)
			}
			return err
		}
		title, err = taskrules.PatchTitle(current.Title, in.Title)
		if err != nil {
			answered = translateTaskRuleError(err)
			return err
		}
		desc = current.Description
		if in.Description != nil {
			desc = sql.NullString{String: *in.Description, Valid: *in.Description != ""}
		}
		prio := current.Priority
		if in.Priority != nil {
			prio = *in.Priority
		}
		due := current.DueOn
		if in.DueOn != nil {
			due = argDue
		}
		start := current.StartedOn
		if in.StartOn != nil {
			start = argStart
		}
		// The merged pair, not the arguments: a call that moves only one
		// side still has to face the other side's stored value.
		if err := taskrules.DateOrder(due, start); err != nil {
			answered = translateTaskRuleError(err)
			return err
		}

		titleChanged = in.Title != nil && title.String() != current.Title
		dueOnChanged := in.DueOn != nil && due != current.DueOn
		// Whether the stored body moved, not whether the caller named it:
		// the history holds the bodies the task has actually carried.
		descChanged = in.Description != nil && desc != current.Description

		needsItemkit := false
		if titleChanged || dueOnChanged {
			n, err := qtx.CountActiveCalendarEventsByTaskId(ctx, sql.NullInt32{
				Int32: int32(taskInternal), //#nosec G115 -- internal row id, bounded by realistic deployments
				Valid: true,
			})
			if err != nil {
				return err
			}
			needsItemkit = n > 0
		}

		updateParams := generated.UpdateTaskParams{
			Title:           title.String(),
			Description:     desc,
			Priority:        prio,
			DueOn:           due,
			StartedOn:       start,
			SortWeight:      current.SortWeight,
			Visibility:      current.Visibility,
			UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			WorkspaceID:     s.workspaceID,
			PublicID:        current.PublicID,
		}
		// Not an existence check: an update carrying the task's current
		// values changes nothing and MySQL counts zero. The row lock above
		// is what established the task exists.
		if _, err := qtx.UpdateTask(ctx, updateParams); err != nil {
			return err
		}
		// Inside the transaction that writes the description: a body that
		// commits without its snapshot is one no restore can return to.
		if descChanged {
			version, err := taskdesc.Snapshot(ctx, qtx, s.workspaceID, taskInternal, updateParams.UpdatedByUserID, desc.String)
			if err != nil {
				return err
			}
			if err := announceTaskDescriptionVersion(ctx, tx, s, taskInternal, current.PublicID, version); err != nil {
				return err
			}
			// Alongside the snapshot, not inside it: a body edited down to
			// nothing writes no version and still has to stop naming the
			// people it used to.
			if err := syncTaskDescriptionMentions(ctx, tx, s, taskInternal, current.PublicID, desc.String); err != nil {
				return err
			}
		}
		if needsItemkit && titleChanged {
			// The renamed text is discarded here: this tool carries the
			// task's new title and body already and refreshes the embedding
			// from them below.
			if _, err := itemkit.RenameItem(ctx, tx, itemkit.RenameItemArgs{
				WorkspaceID: s.workspaceID,
				ActorUserID: s.userID,
				TaskID:      taskInternal,
				NewTitle:    title,
			}); err != nil {
				answered = translateItemkitMCPError(err, apierrors.WsTaskNotFound)
				return err
			}
		}
		if needsItemkit && dueOnChanged {
			// The snap badges come from the actor's working week and holiday
			// set, resolved from the same workspace and actor REST resolves
			// them from.
			snap, err := itemkit.ResolveSnapConfig(ctx, tx, s.workspaceID, s.userID)
			if err != nil {
				return err
			}
			var t time.Time
			if due.Valid {
				t = due.Time
			}
			if err := itemkit.RescheduleTask(ctx, tx, itemkit.RescheduleTaskArgs{
				WorkspaceID: s.workspaceID,
				TaskID:      taskInternal,
				ActorUserID: s.userID,
				SetDueOn:    true,
				DueOn:       t,
				Snap:        snap,
			}); err != nil {
				answered = translateItemkitMCPError(err, apierrors.WsTaskNotFound)
				return err
			}
		}
		// The lifecycle event commits with the change; the audit row waits
		// for the commit.
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.TaskUpdated,
			AuditAction:  "task.update",
			ResourceType: "task",
			ResourceID:   current.PublicID.String(),
			TaskID:       &taskID64,
			Payload:      map[string]any{"taskId": current.PublicID.String(), "via": "mcp"},
			CallSite:     "mcp.update_task",
		})
	})
	if answered != nil {
		return nil, answered
	}
	if txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}
	// Only text that actually moved invalidates the vector: a refresh costs
	// a provider call the workspace pays for.
	if titleChanged || descChanged {
		embed.RefreshTaskAfterCommit(ctx, deps.Embedder, s.workspaceID, taskInternal, title.String(), desc.String)
	}
	return map[string]any{"id": pub.String()}, nil
}

// translateItemkitMCPError maps an itemkit failure onto the error a
// tool answers with, using the same classifier the REST handlers use so
// the two surfaces cannot disagree about what an itemkit error means.
//
// notFound is the caller's own 404 spec, because itemkit is shared
// between the task and calendar tools and only the caller knows which
// resource went missing. The distinction matters to the caller as well
// as to a reader: an agent retries a server error and does not retry a
// 404, so collapsing a missing row into a tool-execution failure buys a
// retry loop that cannot succeed.
// translateTaskRuleError renders a taskrules refusal as the error a tool
// caller is owed. A rule violated by an agent's arguments is bad
// arguments, not a server failure: the REST handlers answer the same
// violations as a 422 naming the field, and this is the other half of
// that split.
func translateTaskRuleError(err error) error {
	switch taskrules.Classify(err) {
	case taskrules.ViolationNone:
		return nil
	case taskrules.ViolationTitleEmpty, taskrules.ViolationDueBeforeStart:
		return apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	return apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
}
func translateItemkitMCPError(err error, notFound *apierrors.Spec) error {
	if err == nil {
		return nil
	}
	switch handlerutil.ClassifyItemkitError(err) {
	case handlerutil.ItemkitErrorNone:
		return nil
	case handlerutil.ItemkitErrorNotFound:
		return apierrors.New(notFound)
	case handlerutil.ItemkitErrorRecurrenceWithLink:
		return apierrors.New(apierrors.ItemItemkitRecurrenceWithTaskLink)
	case handlerutil.ItemkitErrorInvariant:
		return apierrors.New(apierrors.ItemItemkitInvariantViolation)
	case handlerutil.ItemkitErrorOther:
		return apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// Totality only: a kind added to the classifier and not handled above
	// answers as a server error rather than as a plausible wrong one.
	return apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
}
func runTransitionTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID     string  `json:"taskId"`
		Transition string  `json:"transition"`
		Reason     *string `json:"reason"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if !taskstate.IsKnownTransition(in.Transition) {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	taskInternal, pub, err := resolveTaskForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}

	actor := int64(s.userID)
	reason := ""
	if in.Reason != nil {
		reason = *in.Reason
	}
	// dbretry.InTx, not a hand-rolled transaction: the transition event
	// is appended inside it, and only InTx gives the eventbus a commit
	// boundary to hang the fan-out on. Opening the tx directly here is
	// what stopped webhook deliveries and notifications from being
	// created for agent-driven transitions while the same transition
	// over REST delivered normally.
	var (
		result taskstate.ApplyResult
		spec   *apierrors.Spec
	)
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.transition_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		var applyErr error
		result, spec, applyErr = taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
			WorkspaceID:  s.workspaceID,
			TaskID:       taskInternal,
			PublicID:     pub,
			Transition:   in.Transition,
			ActorUserID:  &actor,
			Reason:       reason,
			Via:          "mcp",
			ExtraPayload: nil,
		})
		if applyErr != nil {
			return applyErr
		}
		if spec != nil {
			// A rejected transition is a decision, not a failure: roll
			// the attempt back and let the caller answer with the spec.
			return errTransitionRejected
		}
		return nil
	})
	if spec != nil {
		return nil, apierrors.New(spec)
	}
	if txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}
	// The task's own constraints and its dependents' dependency.* facts
	// read the state just committed. Best effort, as on REST: the next
	// evaluation recovers from a failure here.
	eng := &engine.Engine{Store: &engine.SqlcStore{WorkspaceID: s.workspaceID, Queries: deps.Queries}}
	_ = eng.EvaluateTaskAndDependents(ctx, taskInternal)
	// taskstate appended the transition event inside the transaction so
	// the event and the state change commit together; only the audit row
	// belongs to the transport.
	recorder(deps).RecordTxAudit(ctx, mutationActor(s), mutationlog.Mutation{
		AuditAction:  "task.transition",
		ResourceType: "task",
		ResourceID:   pub.String(),
		Payload: map[string]any{
			"taskId":     pub.String(),
			"transition": in.Transition,
			"fromState":  string(result.FromState),
			"toState":    string(result.ToState),
			"via":        "mcp",
		},
		CallSite: "mcp.transition_task",
	})
	return map[string]any{
		"id":         pub.String(),
		"fromState":  string(result.FromState),
		"toState":    string(result.ToState),
		"transition": in.Transition,
	}, nil
}
func runAddComment(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
		Body   string `json:"body"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Body == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	taskInternal, pub, err := resolveTaskForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	cpub := newPublicID()
	taskID64 := int64(taskInternal)
	// One commit boundary for the body, the mentions it names and its event:
	// a mention row that lands without its comment, or a comment that lands
	// without its mentions, is a thread that says one thing and notifies
	// another.
	if err := dbretry.InTx(ctx, deps.DB, "mcp.add_comment", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		qtx := deps.Queries.WithTx(tx.RawTx())
		id, e := qtx.AddComment(ctx, generated.AddCommentParams{
			PublicID:    cpub,
			WorkspaceID: s.workspaceID,
			TaskID:      sql.NullInt32{Int32: int32(taskInternal), Valid: true}, //#nosec G115 -- internal row id, bounded by realistic deployments
			AuthorID:    sql.NullInt32{Int32: int32(s.userID), Valid: true},     //#nosec G115 -- verified users.id is an INT UNSIGNED key
			AuthorKind:  generated.CommentsAuthorKindUser,
			Body:        in.Body,
		})
		if e != nil {
			return e
		}
		if err := syncCommentMentions(ctx, tx, s, taskInternal, pub, uint32(id), cpub, in.Body); err != nil { //#nosec G115 -- LastInsertId for comments.id (INT UNSIGNED), fits uint32 within realistic deployments
			return err
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.TaskCommentAdded,
			AuditAction:  "comment.create",
			ResourceType: "comment",
			ResourceID:   cpub.String(),
			TaskID:       &taskID64,
			Payload: map[string]any{
				"taskId":    pub.String(),
				"commentId": cpub.String(),
				"via":       "mcp",
			},
			CallSite: "mcp.add_comment",
		})
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"id": cpub.String()}, nil
}

// runSearchTasks searches tasks by title or description using LIKE.
func runSearchTasks(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Query  string `json:"query"`
		Limit  int32  `json:"limit"`
		Offset int32  `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	wsRole, err := requireWorkspaceMember(ctx, deps, s)
	if err != nil {
		return nil, err
	}
	if in.Query == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}

	// Matched as literal text, as the REST search is: % and _ in the query
	// are characters to find, not wildcards.
	pattern := "%" + stringutil.EscapeLike(in.Query) + "%"
	rows, err := searchMCPTasks(ctx, deps.DB, s.workspaceID, s.userID, wsRole, pattern, in.Limit, in.Offset)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, taskListRowToMap(r.publicID, r.title, r.derivedState, r.priority, r.dueOn))
	}
	return map[string]any{"tasks": items}, nil
}
func searchMCPTasks(
	ctx context.Context,
	db *sql.DB,
	workspaceID uint32,
	actorID uint32,
	wsRole acl.WorkspaceRole,
	pattern string,
	limit int32,
	offset int32,
) ([]mcpTaskListRow, error) {
	where := []string{"v.workspace_id = ?", "(v.title LIKE ? OR t.description LIKE ?)"}
	args := []any{workspaceID, pattern, pattern}
	if visFrag, visArgs := acl.TaskVisibilityFilter(actorID, wsRole); visFrag != "" {
		where = append(where, visFrag)
		args = append(args, visArgs...)
	}

	//#nosec G201 -- WHERE fragments are static literals; user values are bound.
	query := fmt.Sprintf(`SELECT
  v.public_id,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on
FROM v_task_list v
INNER JOIN tasks t
  ON t.public_id = v.public_id AND t.workspace_id = v.workspace_id
WHERE %s
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?`, strings.Join(where, " AND "))
	args = append(args, limit, offset)

	// no-generated-query: same reason as listMCPTasks. The search predicate is
	// fixed, but acl.TaskVisibilityFilter AND-ed into it is not: its fragment and
	// its bind count follow the caller's workspace role. Search is the path where
	// omitting it matters most, since a LIKE over titles and descriptions is
	// exactly how a task nobody may see would surface.
	rows, err := db.QueryContext(ctx, query, args...) //#nosec G701 -- query is assembled from static WHERE fragments; all user values are bound args.
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []mcpTaskListRow{}
	for rows.Next() {
		var r mcpTaskListRow
		if err := rows.Scan(&r.publicID, &r.title, &r.derivedState, &r.priority, &r.dueOn); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// runSmartCreateTask creates a parent task and AI-suggested subtasks with
// assignee recommendations. It delegates to the AI orchestrator's
// ProposeSmartCreate method to get a structured proposal, then persists
// the parent task and each subtask as child tasks. Suggested assignees are
// returned for the caller to confirm and no task actor is added for them, so
// a name the model proposed never becomes an assignment on its own.
func runSmartCreateTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ProjectID   string `json:"projectId"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	parentTitle, err := taskrules.NewTitle(in.Title)
	if err != nil {
		return nil, translateTaskRuleError(err)
	}
	wsRole, wsErr := requireWorkspaceMember(ctx, deps, s)
	if wsErr != nil {
		return nil, wsErr
	}
	vis := acl.ListVisibilityArgs(s.userID, wsRole)
	prjID, err := resolveProjectForWrite(ctx, deps, s, in.ProjectID)
	if err != nil {
		return nil, err
	}
	if deps.AI == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	if deps.Embedder == nil {
		policyErr := embed.CheckWorkspacePolicy(ctx, deps.Queries, s.workspaceID)
		if stderrors.Is(policyErr, embed.ErrWorkspaceDisabled) {
			return nil, apierrors.New(apierrors.AiEmbeddingDisabled)
		}
		if policyErr != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, policyErr)
		}
		return nil, apierrors.New(apierrors.AiEmbeddingNotConfigured)
	}

	// Call the smart create orchestrator. The embed client satisfies
	// ai.EmbedClient and *generated.Queries satisfies SmartCreateReader.
	proposal, err := deps.AI.ProposeSmartCreate(
		ctx, s.workspaceID,
		in.Title, in.Description,
		deps.Embedder, deps.Queries,
		vis,
	)
	if err != nil {
		return nil, aiFailure(err)
	}

	// Bound every title the model produced before any of them reaches a
	// column. Nothing constrains what a model returns, and tasks.title is
	// finite, so an overlong subtask title failed the insert and took the
	// whole batch — parent included — down with it. The caller's own title
	// is bounded by the tool's input schema; this is the same bound applied
	// to the half of the payload no schema covers. The cut is rune-aware:
	// a byte-indexed cut can sever a multi-byte character and hand the
	// utf8mb4 column a fragment it rejects under STRICT_TRANS_TABLES.
	//
	// The priority word each subtask carries is resolved here too, through
	// the mapping every transport applies to a model-proposed priority, so
	// a word means the same number whether the proposal arrived over REST
	// or through a tool. It is resolved before the transaction opens
	// because the resolution reports a model answering outside the
	// vocabulary it was handed, and a retried transaction would report the
	// same drift again.
	subtaskPriorities := make([]int32, len(proposal.Subtasks))
	for i := range proposal.Subtasks {
		proposal.Subtasks[i].Title = stringutil.TruncateRunes(proposal.Subtasks[i].Title, taskTitleLimit)
		subtaskPriorities[i] = proposedPriority(ctx, proposal.Subtasks[i].Priority, "mcp.smart_create_task")
	}

	// Create the parent and every proposed subtask in one transaction, so a
	// failure partway through the batch does not leave a parent with a
	// truncated set of children. Each event commits with the task it names.
	type createdChild struct {
		id    int64
		pub   string
		title string
		desc  string
	}
	var (
		parentPub types.PublicID
		parentID  int64
		// children is indexed by proposal position; a subtask that was not
		// created leaves its slot empty, so the response pairs every
		// proposal with the task made from it and no other.
		children []createdChild
	)
	if txErr := dbretry.InTx(ctx, deps.DB, "mcp.smart_create_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		children = make([]createdChild, len(proposal.Subtasks))
		parent, err := taskcreate.New(ctx, tx, taskcreate.AuthoredBy(s.userID), taskcreate.Args{
			WorkspaceID: s.workspaceID,
			ProjectID:   prjID,
			Title:       parentTitle,
			Description: sql.NullString{String: in.Description, Valid: in.Description != ""},
		})
		if err != nil {
			return err
		}
		parentPub = parent.PublicID
		parentID = parent.ID
		if err := recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.TaskCreated,
			AuditAction:  "task.smart_create",
			ResourceType: "task",
			ResourceID:   parentPub.String(),
			TaskID:       &parentID,
			Payload: map[string]any{
				"taskId": parentPub.String(),
				"title":  parentTitle.String(),
				"via":    "mcp:smart_create_task",
			},
			CallSite: "mcp.smart_create_task",
		}); err != nil {
			return err
		}

		for i, st := range proposal.Subtasks {
			// A subtask the model returned without a usable title is
			// dropped rather than failing the batch: the parent and its
			// siblings are what the caller asked for, and one unnameable
			// child is not worth losing them.
			childTitle, terr := taskrules.NewTitle(st.Title)
			if terr != nil {
				continue
			}
			child, cerr := taskcreate.New(ctx, tx, taskcreate.AuthoredBy(s.userID), taskcreate.Args{
				WorkspaceID:  s.workspaceID,
				ProjectID:    prjID,
				ParentTaskID: sql.NullInt32{Int32: int32(parent.ID), Valid: true}, //#nosec G115 -- parent_task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
				Title:        childTitle,
				Description:  sql.NullString{String: st.Description, Valid: st.Description != ""},
				Priority:     subtaskPriorities[i],
			})
			if cerr != nil {
				return cerr
			}
			childID := child.ID
			children[i] = createdChild{id: childID, pub: child.PublicID.String(), title: childTitle.String(), desc: st.Description}
			if err := recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
				EventType:    eventbus.TaskCreated,
				AuditAction:  "task.create",
				ResourceType: "task",
				ResourceID:   children[i].pub,
				TaskID:       &childID,
				Payload: map[string]any{
					"taskId":       children[i].pub,
					"title":        children[i].title,
					"parentTaskId": parentPub.String(),
					"via":          "mcp:smart_create_task",
				},
				CallSite: "mcp.smart_create_task",
			}); err != nil {
				return err
			}
		}
		return nil
	}); txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}

	noteInvocationTask(ctx, uint32(parentID)) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	// The parent and every child are separate tasks with separate
	// embeddings, and the children hold text the model wrote that nothing
	// else indexes.
	embed.RefreshTaskAfterCommit(ctx, deps.Embedder, s.workspaceID, uint32(parentID), parentTitle.String(), in.Description) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	for _, c := range children {
		if c.pub == "" {
			continue
		}
		embed.RefreshTaskAfterCommit(ctx, deps.Embedder, s.workspaceID, uint32(c.id), c.title, c.desc) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	}

	// Build assignee suggestions for the response (we do not auto-assign
	// because the caller should review and confirm the suggestions).
	assignees := make([]map[string]any, 0, len(proposal.SuggestedAssignees))
	for _, a := range proposal.SuggestedAssignees {
		assignees = append(assignees, map[string]any{
			"userPublicId": a.UserPublicID,
			"displayName":  a.DisplayName,
			"confidence":   a.Confidence,
			"reason":       a.Reason,
		})
	}

	subtaskSuggestions := make([]map[string]any, 0, len(proposal.Subtasks))
	for i, st := range proposal.Subtasks {
		m := map[string]any{
			"title":    st.Title,
			"priority": st.Priority,
		}
		if children[i].pub != "" {
			m["id"] = children[i].pub
		}
		if st.Assignee != nil {
			m["suggestedAssignee"] = map[string]any{
				"userPublicId": st.Assignee.UserPublicID,
				"displayName":  st.Assignee.DisplayName,
				"confidence":   st.Assignee.Confidence,
				"reason":       st.Assignee.Reason,
			}
		}
		subtaskSuggestions = append(subtaskSuggestions, m)
	}

	return map[string]any{
		"id":                 parentPub.String(),
		"subtasks":           subtaskSuggestions,
		"suggestedAssignees": assignees,
	}, nil
}
