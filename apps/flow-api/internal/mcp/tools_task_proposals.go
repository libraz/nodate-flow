package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/errormodel"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
)

// runProposeTasksFrom asks the workspace's LLM provider to turn a free-text
// source (meeting notes, email, etc.) into a list of candidate tasks. The
// orchestrator handles cost guard, provider resolution, redaction, and
// invocation logging.
func runProposeTasksFrom(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Source string `json:"source"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Source == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	if deps.AI == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	tasks, err := deps.AI.ProposeTasksFrom(ctx, s.workspaceID, in.Source)
	if err != nil {
		return nil, aiFailure(err)
	}
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, map[string]any{
			"title":       t.Title,
			"description": t.Description,
			"priority":    t.Priority,
		})
	}
	return map[string]any{"tasks": out}, nil
}

// runProposePriority asks the workspace LLM to suggest a priority for an
// existing task identified by public id.
func runProposePriority(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	taskInternal, row, err := resolveTaskRow(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	if deps.AI == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	summary := row.Title
	if row.Description.Valid && row.Description.String != "" {
		summary = row.Title + "\n\n" + row.Description.String
	}
	// Filed under the task its prompt quotes, as propose_steps is.
	invocationCtx := ai.WithInputResources(
		ai.WithTaskID(ctx, taskInternal),
		ai.TaskInputResource(row.PublicID.String()),
	)
	label, err := deps.AI.ProposePriority(invocationCtx, s.workspaceID, summary)
	if err != nil {
		return nil, aiFailure(err)
	}
	// The model answers with a word; the result speaks the 0-4 scale
	// update_task and create_task take.
	return map[string]any{"priority": proposedPriority(ctx, label, "mcp.propose_priority")}, nil
}

// proposedPriority maps a model's priority word onto the 0-4 task scale
// through the mapping REST uses. A word outside the vocabulary still yields
// a value, but the substitution is logged: otherwise a model drifting off
// the vocabulary is indistinguishable from one choosing "none".
func proposedPriority(ctx context.Context, label, callSite string) int32 {
	priority, known := handlerutil.PriorityFromLabel(label)
	if !known {
		slog.WarnContext(ctx, "mcp: LLM returned a priority outside the vocabulary",
			slog.String("call_site", callSite),
			slog.String("priority_label", label),
		)
	}
	return priority
}

// similarResultLimit caps the candidates one similarity tool returns, the
// same cap the REST duplicate listing applies.
const similarResultLimit = 20

// rankSimilarTasks is the similarity search behind propose_duplicates and
// propose_relations, over the caller's readable set. A source task with no
// usable embedding yields no candidates rather than an error.
func rankSimilarTasks(ctx context.Context, deps Deps, s *session, wsRole acl.WorkspaceRole, taskInternal uint32, title string, description sql.NullString) (string, []embed.SimilarTask, error) {
	vis := acl.ListVisibilityArgs(s.userID, wsRole)
	ranked, err := deps.Embedder.RankSimilarTasks(ctx, deps.Queries, embed.SimilarQuery{
		WorkspaceID: s.workspaceID,
		TaskID:      taskInternal,
		SourceText: func(context.Context) (string, string, error) {
			return title, description.String, nil
		},
		IsElevated:  vis.IsElevated,
		ActorUserID: vis.ActorUserID,
		ResultLimit: similarResultLimit,
	})
	if stderrors.Is(err, embed.ErrNoSourceVector) {
		return deps.Embedder.Model(), nil, nil
	}
	if stderrors.Is(err, embed.ErrWorkspaceDisabled) {
		return deps.Embedder.Model(), nil, apierrors.New(apierrors.AiEmbeddingDisabled)
	}
	if stderrors.Is(err, embed.ErrWorkspaceNotConfigured) {
		return deps.Embedder.Model(), nil, apierrors.New(apierrors.AiEmbeddingNotConfigured)
	}
	if err != nil {
		return deps.Embedder.Model(), nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return deps.Embedder.Model(), ranked, nil
}

// similarTaskCandidates shapes ranked candidates for a tool result, naming
// the duplicate-or-related verdict under kindKey.
func similarTaskCandidates(ranked []embed.SimilarTask, kindKey string) []map[string]any {
	out := make([]map[string]any, 0, len(ranked))
	for _, c := range ranked {
		kind := "related"
		if c.Duplicate {
			kind = "duplicate"
		}
		out = append(out, map[string]any{
			"taskId": c.PublicID.String(),
			"title":  c.Title,
			"score":  c.Score,
			kindKey:  kind,
		})
	}
	return out
}

// runProposeDuplicates returns likely duplicates of a task, classified as
// "duplicate" or "related".
func runProposeDuplicates(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	wsRole, wsErr := requireWorkspaceMember(ctx, deps, s)
	if wsErr != nil {
		return nil, wsErr
	}
	if deps.Embedder == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	taskInternal, row, err := resolveTaskRow(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	model, ranked, err := rankSimilarTasks(ctx, deps, s, wsRole, taskInternal, row.Title, row.Description)
	if err != nil {
		return nil, err
	}
	return map[string]any{"candidates": similarTaskCandidates(ranked, "classification"), "model": model}, nil
}

// runProposeSteps asks the workspace LLM to break an existing task into
// concrete execution steps, through the same orchestrator call REST
// propose-steps makes: existing children the caller can see are handed to
// the model so it does not re-propose them, similar tasks come from the
// caller's readable set, and priorities are on the 0-4 scale apply_steps
// takes, so the result can be applied unchanged.
//
// The project-editor floor is the one REST holds the same request to: a
// breakdown is the first half of apply_steps, it spends the workspace's
// model budget, and the decomposition it returns is meant for whoever may
// restructure the task.
func runProposeSteps(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	wsRole, err := requireWorkspaceMember(ctx, deps, s)
	if err != nil {
		return nil, err
	}
	taskInternal, row, err := resolveTaskRowForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	if deps.AI == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	// A child carries its own visibility and its title reaches the prompt,
	// so the children are filtered by the Layer-4 rule in their own right.
	vis := acl.ListVisibilityArgs(s.userID, wsRole)
	var children []ai.ChildTaskSummary
	childRows, err := deps.Queries.ListChildTasksByParentID(ctx, generated.ListChildTasksByParentIDParams{
		WorkspaceID:  s.workspaceID,
		ParentTaskID: sql.NullInt32{Int32: int32(taskInternal), Valid: true}, //#nosec G115 -- parent task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		IsElevated:   vis.IsElevated,
		ActorUserID:  vis.ActorUserID,
	})
	if err == nil {
		for _, c := range childRows {
			children = append(children, ai.ChildTaskSummary{
				PublicID: c.PublicID.String(),
				Title:    c.Title,
				State:    string(c.DerivedState),
			})
		}
	}
	// The interfaces stay unset when no embedder is configured: a nil
	// *embed.Client held in one would be non-nil as an interface.
	var embedClient ai.EmbedClient
	var reader ai.SmartCreateReader
	if deps.Embedder != nil {
		embedClient = deps.Embedder
		reader = deps.Queries
	}
	desc := ""
	if row.Description.Valid {
		desc = row.Description.String
	}
	// The invocation is filed under the task its prompt quotes, so the
	// invocation list can apply the task's visibility to it.
	invocationCtx := ai.WithInvocationAudience(ai.WithTaskID(ctx, taskInternal), ai.InvocationAudienceInitiator)
	invocationCtx = ai.WithInputResources(invocationCtx, ai.TaskInputResource(row.PublicID.String()))
	steps, err := deps.AI.ProposeSteps(invocationCtx, s.workspaceID, row.Title, desc, ai.GranularityStandard, children, embedClient, reader, vis)
	if err != nil {
		return nil, aiFailure(err)
	}
	out := make([]map[string]any, 0, len(steps))
	for _, st := range steps {
		out = append(out, map[string]any{
			"title":       st.Title,
			"description": st.Description,
			"priority":    proposedPriority(ctx, st.Priority, "mcp.propose_steps"),
		})
	}
	return map[string]any{"parentTaskId": row.PublicID.String(), "steps": out}, nil
}

// runApplySteps creates the given steps as child tasks under an
// existing parent task. Each step becomes a new tasks row with
// parent_task_id set to the parent's internal id. Returns the list of
// created child public ids.
func runApplySteps(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ParentTaskID string `json:"parentTaskId"`
		Steps        []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Priority    int32  `json:"priority"`
		} `json:"steps"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if len(in.Steps) == 0 {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	parentInternal, parentPub, err := resolveTaskForWrite(ctx, deps, s, in.ParentTaskID)
	if err != nil {
		return nil, err
	}
	// no-generated-query: the child rows need the parent's internal project_id as
	// an FK value, and no generated read hands it over. The single-task lookup in
	// acl.go projects the project's public id, which would have to be resolved
	// back to an internal one; LockTaskForTransition does return project_id but
	// is FOR UPDATE and belongs inside the transition transaction, so reading
	// through it here would take a row lock this path has no reason to hold.
	// Replacing this takes a plain generated read of tasks.project_id keyed on
	// the internal id. The read is the one REST apply-steps makes.
	//
	// A step is a task derived from the parent, so it carries the parent's
	// visibility rather than the create-path default: a public child of a
	// private parent would put the private task's plan in front of the whole
	// workspace.
	var parentProjectID uint32
	var parentVisibility generated.TasksVisibility
	if err := deps.DB.QueryRowContext(ctx,
		`SELECT project_id, visibility FROM tasks WHERE id = ? AND workspace_id = ? LIMIT 1`,
		parentInternal, s.workspaceID,
	).Scan(&parentProjectID, &parentVisibility); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// Every step title is checked before the transaction opens, so a
	// blank one partway down the list is refused as bad arguments rather
	// than aborting a batch that has already inserted its predecessors.
	stepTitles := make([]taskrules.Title, 0, len(in.Steps))
	for _, st := range in.Steps {
		title, terr := taskrules.NewTitle(st.Title)
		if terr != nil {
			return nil, translateTaskRuleError(terr)
		}
		stepTitles = append(stepTitles, title)
	}

	// All children are created in one transaction. Task-number allocation
	// locks the project row, and it reads MAX(task_number) within the
	// transaction so each step sees its predecessors and the children get
	// distinct numbers. A failure partway through now rolls the whole batch
	// back instead of leaving orphan children behind.
	created := make([]string, 0, len(in.Steps))
	childIDs := make([]int64, 0, len(in.Steps))
	if txErr := dbretry.InTx(ctx, deps.DB, "mcp.apply_steps", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		created = created[:0]
		childIDs = childIDs[:0]
		for i, st := range in.Steps {
			child, err := taskcreate.New(ctx, tx, taskcreate.AuthoredBy(s.userID), taskcreate.Args{
				WorkspaceID:  s.workspaceID,
				ProjectID:    parentProjectID,
				ParentTaskID: sql.NullInt32{Int32: int32(parentInternal), Valid: true}, //#nosec G115 -- parent task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
				Title:        stepTitles[i],
				Description:  sql.NullString{String: st.Description, Valid: st.Description != ""},
				Priority:     st.Priority,
				Visibility:   parentVisibility,
			})
			if err != nil {
				return err
			}
			created = append(created, child.PublicID.String())
			childIDs = append(childIDs, child.ID)
			childID := child.ID
			// The event commits with the child it names, and carries the
			// title the row actually holds: the rule trims before the
			// insert.
			if err := recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
				EventType:    eventbus.TaskCreated,
				AuditAction:  "task.apply_steps",
				ResourceType: "task",
				ResourceID:   created[i],
				TaskID:       &childID,
				Payload: map[string]any{
					"taskId":       created[i],
					"title":        stepTitles[i].String(),
					"parentTaskId": parentPub.String(),
					"via":          "mcp:apply_steps",
				},
				CallSite: "mcp.apply_steps",
			}); err != nil {
				return err
			}
		}
		return nil
	}); txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}

	for i := range in.Steps {
		childID := childIDs[i]
		// Each step is a task in its own right, so it carries its own
		// embedding. Left unrefreshed a step would be absent from semantic
		// search entirely rather than merely stale.
		embed.RefreshTaskAfterCommit(ctx, deps.Embedder, s.workspaceID, uint32(childID), stepTitles[i].String(), in.Steps[i].Description) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	}
	return map[string]any{"created": created}, nil
}

// aiFailure answers an AI-layer failure with the code the shared mapping
// assigns it, so the same failure reads the same on REST and MCP.
func aiFailure(err error) error {
	spec, _ := errormodel.AIFailure(err)
	return apierrors.Wrap(spec, err)
}

// runProposeRelations finds related or duplicate tasks for a given task
// by embedding similarity, naming the verdict suggestedKind. It is the
// same search as propose_duplicates; only the output key differs.
func runProposeRelations(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	wsRole, wsErr := requireWorkspaceMember(ctx, deps, s)
	if wsErr != nil {
		return nil, wsErr
	}
	if deps.Embedder == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	taskInternal, row, err := resolveTaskRow(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	model, ranked, err := rankSimilarTasks(ctx, deps, s, wsRole, taskInternal, row.Title, row.Description)
	if err != nil {
		return nil, err
	}
	return map[string]any{"candidates": similarTaskCandidates(ranked, "suggestedKind"), "model": model}, nil
}

// runProposeLens compiles a prose prompt into a validated Lens via
// the NL query compiler. When no compiler is wired (e.g., no AI mock
// or provider configured) it returns AI.PROVIDER.NOT_CONFIGURED.
func runProposeLens(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Prompt string `json:"prompt"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	if deps.NlQuery == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	lens, err := deps.NlQuery.Compile(ctx, in.Prompt)
	if err != nil {
		return nil, aiFailure(err)
	}
	return map[string]any{"lens": lens}, nil
}
