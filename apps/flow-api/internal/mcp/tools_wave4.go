package mcp

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

func runListImportJobs(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		Status string `json:"status"`
		Limit  int32  `json:"limit"`
		Offset int32  `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	rows, err := deps.Queries.ListImportJobsForWorkspace(ctx, generated.ListImportJobsForWorkspaceParams{
		WorkspaceID: s.workspaceID,
		Limit:       in.Limit,
		Offset:      in.Offset,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	type jobOut struct {
		ID             string `json:"id"`
		Source         string `json:"source"`
		Status         string `json:"status"`
		TotalItems     int    `json:"totalItems"`
		ProcessedItems int    `json:"processedItems"`
		FailedItems    int    `json:"failedItems"`
		StartedAt      *int64 `json:"startedAt,omitempty"`
		CompletedAt    *int64 `json:"completedAt,omitempty"`
		CreatedAt      int64  `json:"createdAt"`
	}
	out := make([]jobOut, 0, len(rows))
	for _, r := range rows {
		// Apply optional client-side status filter since the query
		// does not accept a status parameter.
		if in.Status != "" && string(r.Status) != in.Status {
			continue
		}
		j := jobOut{
			ID:             r.PublicID.String(),
			Source:         string(r.Source),
			Status:         string(r.Status),
			TotalItems:     int(r.TotalItems),
			ProcessedItems: int(r.ProcessedItems),
			FailedItems:    int(r.FailedItems),
			CreatedAt:      r.CreatedAt.Unix(),
		}
		if r.StartedAt.Valid {
			v := r.StartedAt.Time.Unix()
			j.StartedAt = &v
		}
		if r.CompletedAt.Valid {
			v := r.CompletedAt.Time.Unix()
			j.CompletedAt = &v
		}
		out = append(out, j)
	}
	return map[string]any{"jobs": out}, nil
}

func runCreateImportJob(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		Source     string `json:"source"`
		ProjectID  string `json:"projectId"`
		ConfigJSON string `json:"configJson"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Source == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	// Validate source value.
	switch in.Source {
	case "github", "jira", "linear", "csv":
		// ok
	default:
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}

	var projectID sql.NullInt32
	if in.ProjectID != "" {
		prjPub, err := types.Parse(in.ProjectID)
		if err != nil {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		prj, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
			WorkspaceID: s.workspaceID,
			PublicID:    prjPub,
		})
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, apierrors.New(apierrors.WsProjectNotFound)
			}
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		projectID = sql.NullInt32{Int32: int32(prj.ID), Valid: true} //#nosec G115 -- project id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}

	configJSON := json.RawMessage("{}")
	if in.ConfigJSON != "" {
		// Validate it is valid JSON.
		if !json.Valid([]byte(in.ConfigJSON)) {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		configJSON = json.RawMessage(in.ConfigJSON)
	}

	pub := newPublicID()
	_, err := deps.Queries.CreateImportJob(ctx, generated.CreateImportJobParams{
		PublicID:          pub,
		WorkspaceID:       s.workspaceID,
		ProjectID:         projectID,
		InitiatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		Source:            generated.ImportJobsSource(in.Source),
		ConfigJson:        configJSON,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	return map[string]any{"ok": true, "importJobId": pub.String()}, nil
}
