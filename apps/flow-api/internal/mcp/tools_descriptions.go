// MCP tools over a task's description history: listing the stored versions
// and restoring one of them onto the task.

package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskdesc"
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

// runRestoreDescriptionVersion restores a stored description version onto its task.
//
// task-precondition: date-order not-applicable — the update writes the task's
// stored due and start dates back unchanged, having read both from the row it
// is restoring into, so it cannot put them out of order. Rows predating the
// rule may already hold an inverted pair; checking here would refuse a
// description restore over dates the restore does not touch.
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

	// One commit boundary for the update, the snapshot and the mentions the
	// restored body names. dbretry.InTx rather than a bare transaction: the
	// mention sync takes a commit boundary, which is what lets the event it
	// appends defer its fan-out until the description it describes is
	// observable.
	// The title is what leaves the transaction: the update carries it
	// through unchanged, and the embedding refresh below pairs it with the
	// restored body. The row it is read from stays inside, where the task
	// lookup belongs.
	var taskTitle string
	var restored taskdesc.Version
	if err := dbretry.InTx(ctx, deps.DB, "mcp.restore_description_version", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		qtx := deps.Queries.WithTx(tx.RawTx())

		// Get current task state for the UpdateTask call. Access was already
		// authorized by resolveTask above; this transaction-scoped load reads
		// the row for a consistent update.
		taskRow, rerr := loadTaskRow(ctx, qtx, s.workspaceID, taskPub)
		if rerr != nil {
			return rerr
		}
		taskTitle = taskRow.Title

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
			return err
		}

		// The restored body becomes the newest version rather than rewinding
		// the history to the one it came from.
		restored, rerr = taskdesc.Snapshot(ctx, qtx, s.workspaceID, taskInternal,
			sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			version.Body,
		)
		if rerr != nil {
			return rerr
		}
		// The restored body is the one the task now carries, so it is the
		// body the mentions table has to agree with — including when the
		// version being restored to named nobody.
		return syncTaskDescriptionMentions(ctx, tx, s, taskInternal, taskPub, version.Body)
	}); err != nil {
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
		Payload:      map[string]any{"taskId": in.TaskID, "restoredFrom": in.VersionID, "newVersionId": restored.PublicID.String(), "via": "mcp"},
		CallSite:     "mcp.restore_description_version",
	})
	// A restore replaces the task's description, so the embedding follows it
	// back. The pair passed is what the update wrote: the title it carried
	// through unchanged, and the restored body.
	embed.RefreshTaskAfterCommit(ctx, deps.Embedder, s.workspaceID, taskInternal, taskTitle, version.Body)

	return map[string]any{"ok": true, "newVersionId": restored.PublicID.String()}, nil
}
