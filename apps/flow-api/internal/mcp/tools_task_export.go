package mcp

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

// runExportTasks exports tasks as JSON for MCP consumers. When
// projectId is provided it scopes to that project using
// ExportTasksForLens; otherwise it exports workspace-wide.
func runExportTasks(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ProjectID string `json:"projectId"`
		Limit     int32  `json:"limit"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	wsRole, err := requireWorkspaceMember(ctx, deps, s)
	if err != nil {
		return nil, err
	}
	visibility := acl.ListVisibilityArgs(s.userID, wsRole)
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 200
	}
	type exportRow struct {
		PublicID            types.PublicID
		Title               string
		Description         sql.NullString
		DerivedState        string
		Priority            int32
		DueOn               sql.NullTime
		StartedOn           sql.NullTime
		ProjectName         string
		AssigneeDisplayName sql.NullString
	}
	var rows []exportRow
	if in.ProjectID != "" {
		prjID, err := resolveProject(ctx, deps, s, in.ProjectID)
		if err != nil {
			return nil, err
		}
		dbRows, err := deps.Queries.ExportTasksForLens(ctx, generated.ExportTasksForLensParams{
			WorkspaceID: s.workspaceID,
			ProjectID:   prjID,
			IsElevated:  visibility.IsElevated,
			ActorUserID: visibility.ActorUserID,
			Limit:       in.Limit,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		for _, r := range dbRows {
			rows = append(rows, exportRow{
				PublicID:            r.PublicID,
				Title:               r.Title,
				Description:         r.Description,
				DerivedState:        string(r.DerivedState),
				Priority:            r.Priority,
				DueOn:               r.DueOn,
				StartedOn:           r.StartedOn,
				ProjectName:         r.ProjectName,
				AssigneeDisplayName: r.AssigneeDisplayName,
			})
		}
	} else {
		dbRows, err := deps.Queries.ExportTasksForWorkspace(ctx, generated.ExportTasksForWorkspaceParams{
			WorkspaceID: s.workspaceID,
			IsElevated:  visibility.IsElevated,
			ActorUserID: visibility.ActorUserID,
			Limit:       in.Limit,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		for _, r := range dbRows {
			rows = append(rows, exportRow{
				PublicID:            r.PublicID,
				Title:               r.Title,
				Description:         r.Description,
				DerivedState:        string(r.DerivedState),
				Priority:            r.Priority,
				DueOn:               r.DueOn,
				StartedOn:           r.StartedOn,
				ProjectName:         r.ProjectName,
				AssigneeDisplayName: r.AssigneeDisplayName,
			})
		}
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		m := map[string]any{
			"id":          r.PublicID.String(),
			"title":       r.Title,
			"state":       r.DerivedState,
			"priority":    r.Priority,
			"projectName": r.ProjectName,
		}
		if r.Description.Valid {
			m["description"] = r.Description.String
		}
		if r.DueOn.Valid {
			m["dueOn"] = r.DueOn.Time.UTC().Format("2006-01-02")
		}
		if r.AssigneeDisplayName.Valid {
			m["assignee"] = r.AssigneeDisplayName.String
		}
		items = append(items, m)
	}
	// An export is a read, but it is also the moment a workspace's task
	// data leaves it in bulk. REST records both halves for exactly that
	// reason; leaving MCP out meant an administrator investigating a leak
	// saw only the exports made by people using a browser, and none of
	// the ones made by the automated callers that use this surface most.
	// Nothing was written, so there is no transaction for the event to join.
	recorder(deps).Record(ctx, mutationActor(s), mutationlog.Mutation{
		EventType:    eventbus.ExportRequested,
		AuditAction:  "export.create",
		ResourceType: "export",
		Payload: map[string]any{
			"format": "json",
			"count":  len(items),
			"via":    "mcp",
		},
		CallSite: "mcp.export_tasks",
	})
	return map[string]any{"tasks": items}, nil
}
