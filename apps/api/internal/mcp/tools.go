// Package mcp tool registry and Phase 1 tool implementations.
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
)

// tool is the internal descriptor for a registered MCP tool.
type tool struct {
	name          string
	description   string
	requiredScope string
	inputSchema   map[string]any
	run           func(ctx context.Context, deps Deps, s *session, args json.RawMessage) (any, error)
}

// toolDescriptor is the public shape returned by tools/list.
type toolDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (h *Handler) listTools() []toolDescriptor {
	out := make([]toolDescriptor, 0, len(h.tools))
	for _, t := range h.tools {
		out = append(out, toolDescriptor{
			Name:        t.name,
			Description: t.description,
			InputSchema: t.inputSchema,
		})
	}
	return out
}

func (h *Handler) register(t tool) { h.tools[t.name] = t }

func registerTools(h *Handler) {
	h.register(tool{
		name:          "list_projects",
		description:   "List projects in the caller's workspace.",
		requiredScope: "read:workspace",
		inputSchema:   objectSchema(nil, nil),
		run:           runListProjects,
	})
	h.register(tool{
		name:          "list_tasks",
		description:   "List tasks, optionally scoped to a project.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId": stringSchema("Project public id (UUID v7). Optional."),
			"limit":     intSchema("Max number of rows (1..200)."),
			"offset":    intSchema("Row offset."),
		}, nil),
		run: runListTasks,
	})
	h.register(tool{
		name:          "get_task",
		description:   "Fetch a single task by public id.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
		}, []string{"taskId"}),
		run: runGetTask,
	})
	h.register(tool{
		name:          "create_task",
		description:   "Create a new task in a project.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId":   stringSchema("Project public id (UUID v7)."),
			"title":       stringSchema("Task title."),
			"description": stringSchema("Optional description."),
			"priority":    intSchema("Priority 0..4."),
			"dueOn":       stringSchema("YYYY-MM-DD."),
			"startOn":     stringSchema("YYYY-MM-DD."),
		}, []string{"projectId", "title"}),
		run: runCreateTask,
	})
	h.register(tool{
		name:          "update_task",
		description:   "Update mutable fields of a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":      stringSchema("Task public id (UUID v7)."),
			"title":       stringSchema("New title."),
			"description": stringSchema("New description."),
			"priority":    intSchema("New priority 0..4."),
			"dueOn":       stringSchema("YYYY-MM-DD."),
			"startOn":     stringSchema("YYYY-MM-DD."),
		}, []string{"taskId"}),
		run: runUpdateTask,
	})
	h.register(tool{
		name:          "add_comment",
		description:   "Append a comment to a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
			"body":   stringSchema("Comment body."),
		}, []string{"taskId", "body"}),
		run: runAddComment,
	})
	h.register(tool{
		name:          "search_tasks",
		description:   "Full-text search across tasks. NOT YET IMPLEMENTED in Phase 1.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"query": stringSchema("Search query."),
		}, []string{"query"}),
		run: runSearchTasks,
	})
	h.register(tool{
		name:          "propose_tasks_from",
		description:   "Ask the workspace LLM to propose tasks from free text. AI not wired in Phase 1.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"source": stringSchema("Input text to propose tasks from."),
		}, []string{"source"}),
		run: runProposeTasksFrom,
	})
	h.register(tool{
		name:          "propose_priority",
		description:   "Ask the workspace LLM to propose a priority for a task. AI not wired in Phase 1.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
		}, []string{"taskId"}),
		run: runProposePriority,
	})
}

// ----------------------------------------------------------------------------
// JSON Schema helpers (minimal hand-rolled)
// ----------------------------------------------------------------------------

func objectSchema(props map[string]any, required []string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	out := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func stringSchema(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intSchema(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

// ----------------------------------------------------------------------------
// Tool implementations
// ----------------------------------------------------------------------------

func parseArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return apierrors.Wrap(apierrors.McpToolArgumentsInvalid, err)
	}
	return nil
}

func parseDateOrNull(s string) (sql.NullTime, error) {
	if s == "" {
		return sql.NullTime{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return sql.NullTime{}, err
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

func runListProjects(ctx context.Context, deps Deps, s *session, _ json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	rows, err := deps.Queries.ListProjectsForWorkspace(ctx, generated.ListProjectsForWorkspaceParams{
		WorkspaceID: s.workspaceID,
		Limit:       200,
		Offset:      0,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, map[string]any{
			"id":   r.PublicID.String(),
			"slug": r.Slug,
			"name": r.Name,
		})
	}
	return map[string]any{"projects": items}, nil
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
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	items := []map[string]any{}
	if in.ProjectID != "" {
		prjPub, err := types.Parse(in.ProjectID)
		if err != nil {
			return nil, apierrors.New(apierrors.WsProjectNotFound)
		}
		if _, err := resolveProject(ctx, deps, s, in.ProjectID); err != nil {
			return nil, err
		}
		pb := prjPub.UUID()
		rows, err := deps.Queries.ListTasksForProject(ctx, generated.ListTasksForProjectParams{
			WorkspaceID:     s.workspaceID,
			ProjectPublicID: pb[:],
			Limit:           in.Limit,
			Offset:          in.Offset,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		for _, r := range rows {
			items = append(items, taskListRowToMap(r.PublicID, r.Title, string(r.DerivedState), r.Priority, r.DueOn))
		}
	} else {
		rows, err := deps.Queries.ListTasksForWorkspace(ctx, generated.ListTasksForWorkspaceParams{
			WorkspaceID: s.workspaceID,
			Limit:       in.Limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		for _, r := range rows {
			items = append(items, taskListRowToMap(r.PublicID, r.Title, string(r.DerivedState), r.Priority, r.DueOn))
		}
	}
	return map[string]any{"tasks": items}, nil
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
	pub, err := types.Parse(in.TaskID)
	if err != nil {
		return nil, apierrors.New(apierrors.WsTaskNotFound)
	}
	row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsTaskNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
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
	if in.Title == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	prjID, err := resolveProject(ctx, deps, s, in.ProjectID)
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
	pub := newPublicID()
	desc := sql.NullString{String: in.Description, Valid: in.Description != ""}
	taskID, err := deps.Queries.CreateTask(ctx, generated.CreateTaskParams{
		PublicID:        pub,
		WorkspaceID:     s.workspaceID,
		ProjectID:       prjID,
		ParentTaskID:    sql.NullInt32{},
		CreatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},
		Title:           in.Title,
		Description:     desc,
		Priority:        in.Priority,
		DueOn:           due,
		StartedOn:       start,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        "task.created",
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID,
		Payload: map[string]any{
			"taskId": pub.String(),
			"title":  in.Title,
			"via":    "mcp",
		},
	})
	return map[string]any{"id": pub.String()}, nil
}

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
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	taskInternal, pub, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	current, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	title := current.Title
	if in.Title != nil && *in.Title != "" {
		title = *in.Title
	}
	desc := current.Description
	if in.Description != nil {
		desc = sql.NullString{String: *in.Description, Valid: *in.Description != ""}
	}
	prio := current.Priority
	if in.Priority != nil {
		prio = *in.Priority
	}
	due := current.DueOn
	if in.DueOn != nil {
		p, err := parseDateOrNull(*in.DueOn)
		if err != nil {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		due = p
	}
	start := current.StartedOn
	if in.StartOn != nil {
		p, err := parseDateOrNull(*in.StartOn)
		if err != nil {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		start = p
	}
	if err := deps.Queries.UpdateTask(ctx, generated.UpdateTaskParams{
		Title:       title,
		Description: desc,
		Priority:    prio,
		DueOn:       due,
		StartedOn:   start,
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	taskID64 := int64(taskInternal)
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        "task.updated",
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID64,
		Payload:     map[string]any{"taskId": pub.String(), "via": "mcp"},
	})
	return map[string]any{"id": pub.String()}, nil
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
	taskInternal, pub, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	cpub := newPublicID()
	if _, err := deps.Queries.AddComment(ctx, generated.AddCommentParams{
		PublicID:    cpub,
		WorkspaceID: s.workspaceID,
		TaskID:      taskInternal,
		AuthorID:    s.userID,
		Body:        in.Body,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	taskID64 := int64(taskInternal)
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        "comment.added",
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID64,
		Payload: map[string]any{
			"taskId":    pub.String(),
			"commentId": cpub.String(),
			"via":       "mcp",
		},
	})
	return map[string]any{"id": cpub.String()}, nil
}

// runSearchTasks is deferred to Phase 2: the SearchTasks sqlc query does
// not exist yet and adding it would expand the scope of Phase 1.
func runSearchTasks(_ context.Context, _ Deps, _ *session, _ json.RawMessage) (any, error) {
	return nil, apierrors.Newf(apierrors.McpToolExecutionFailed,
		"search_tasks: not implemented in Phase 1")
}

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
		return nil, mapAiError(err)
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
	pub, err := types.Parse(in.TaskID)
	if err != nil {
		return nil, apierrors.New(apierrors.WsTaskNotFound)
	}
	row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsTaskNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	if deps.AI == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	summary := row.Title
	if row.Description.Valid && row.Description.String != "" {
		summary = row.Title + "\n\n" + row.Description.String
	}
	priority, err := deps.AI.ProposePriority(ctx, s.workspaceID, summary)
	if err != nil {
		return nil, mapAiError(err)
	}
	return map[string]any{
		"priority":  priority,
		"rationale": "",
	}, nil
}

// mapAiError maps ai package sentinel errors to the appropriate API
// error spec for the MCP boundary.
func mapAiError(err error) error {
	switch {
	case stderrors.Is(err, ai.ErrNoProvider):
		return apierrors.New(apierrors.AiProviderNotConfigured)
	case stderrors.Is(err, ai.ErrDailyBudgetExceeded):
		return apierrors.New(apierrors.AiCostGuardExceeded)
	case stderrors.Is(err, ai.ErrParse):
		return apierrors.New(apierrors.AiResponseParseFailed)
	}
	return apierrors.Wrap(apierrors.AiProviderUpstreamCallFailed, err)
}

