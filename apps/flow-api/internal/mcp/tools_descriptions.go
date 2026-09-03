// MCP tools over a task's description history: listing the stored versions
// and restoring one of them onto the task.

package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

func runListDescriptionVersions(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	taskInternal, _, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	rows, err := deps.Queries.ListDescriptionVersions(ctx, generated.ListDescriptionVersionsParams{
		WorkspaceID: s.workspaceID,
		TaskID:      taskInternal,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	type versionOut struct {
		ID                string `json:"id"`
		VersionNumber     int    `json:"versionNumber"`
		AuthorID          string `json:"authorId,omitempty"`
		AuthorDisplayName string `json:"authorDisplayName,omitempty"`
		BodyLength        int    `json:"bodyLength"`
		CreatedAt         int64  `json:"createdAt"`
	}
	out := make([]versionOut, 0, len(rows))
	for _, r := range rows {
		v := versionOut{
			ID:            r.PublicID.String(),
			VersionNumber: int(r.VersionNumber),
			BodyLength:    int(r.BodyLength),
			CreatedAt:     r.CreatedAt.Unix(),
		}
		var zero types.PublicID
		if r.AuthorPublicID != zero {
			v.AuthorID = r.AuthorPublicID.String()
		}
		if r.AuthorDisplayName.Valid {
			v.AuthorDisplayName = r.AuthorDisplayName.String
		}
		out = append(out, v)
	}
	return map[string]any{"versions": out}, nil
}

func runRestoreDescriptionVersion(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TaskID    string `json:"taskId"`
		VersionID string `json:"versionId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.TaskID == "" || in.VersionID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}

	taskInternal, taskPub, err := resolveTaskForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}

	versionPub, err := types.Parse(in.VersionID)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}

	version, err := deps.Queries.FindDescriptionVersion(ctx, generated.FindDescriptionVersionParams{
		WorkspaceID: s.workspaceID,
		TaskID:      taskInternal,
		PublicID:    versionPub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsDescriptionVersionNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	tx, err := deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	defer tx.Rollback() //nolint:errcheck
	qtx := deps.Queries.WithTx(tx)

	// Get current task state for the UpdateTask call. Access was already
	// authorized by resolveTask above; this transaction-scoped load reads
	// the row for a consistent update.
	taskRow, err := loadTaskRow(ctx, qtx, s.workspaceID, taskPub)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	// Not an existence check: restoring a version whose body already
	// matches the task changes nothing and MySQL counts zero. The task is
	// read into taskRow just above.
	if _, err := qtx.UpdateTask(ctx, generated.UpdateTaskParams{
		Title:           taskRow.Title,
		Description:     sql.NullString{String: version.Body, Valid: version.Body != ""},
		Priority:        taskRow.Priority,
		DueOn:           taskRow.DueOn,
		StartedOn:       taskRow.StartedOn,
		SortWeight:      taskRow.SortWeight,
		Visibility:      taskRow.Visibility,
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		WorkspaceID:     s.workspaceID,
		PublicID:        taskPub,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	nextVer, err := qtx.NextDescriptionVersionNumber(ctx, taskInternal)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	newPub := newPublicID()
	if _, err := qtx.CreateDescriptionVersion(ctx, generated.CreateDescriptionVersionParams{
		PublicID:      newPub,
		WorkspaceID:   s.workspaceID,
		TaskID:        taskInternal,
		AuthorUserID:  sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		VersionNumber: uint32(nextVer),                                    //#nosec G115 -- per-task version sequence, fits uint32
		Body:          version.Body,
		BodyLength:    uint32(len(version.Body)), //#nosec G115 -- description body length capped at 50KB by handler validation, fits uint32
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	taskIDInt64 := int64(taskInternal)
	// The restore committed a new description version row; a retry would
	// add another one on top of it.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.DescriptionVersionRestored,
		AuditAction:  "description_version.restore",
		ResourceType: "task",
		ResourceID:   taskPub.String(),
		TaskID:       &taskIDInt64,
		Payload:      map[string]any{"taskId": in.TaskID, "restoredFrom": in.VersionID, "newVersionId": newPub.String(), "via": "mcp"},
		CallSite:     "mcp.restore_description_version",
	})

	return map[string]any{"ok": true, "newVersionId": newPub.String()}, nil
}
