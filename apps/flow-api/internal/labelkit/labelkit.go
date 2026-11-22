// Package labelkit owns the writes behind a label's life on a task:
// creating a label, attaching it to a task and detaching it. Each
// function changes its rows and appends the timeline event on the
// caller's transaction; the audit row is the caller's, written with
// RecordTxAudit after the commit.
package labelkit

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// ErrNameTaken is returned when a live label with the same name already
// exists in the target scope.
var ErrNameTaken = errors.New("labelkit: label name already taken in scope")

// ErrAlreadyAttached is returned when the task already carries the label.
var ErrAlreadyAttached = errors.New("labelkit: label already attached")

// ErrNotAttached is returned when the task does not carry the label.
var ErrNotAttached = errors.New("labelkit: label not attached")

// CreateArgs describes one label.
type CreateArgs struct {
	WorkspaceID uint32
	ProjectID   sql.NullInt64
	ParentID    sql.NullInt64
	Name        string
	Color       string
	// Description is stored as NULL when empty.
	Description string
	Actor       mutationlog.Actor
	// Via is recorded in the event payload when non-empty ("mcp").
	Via string
}

// Label identifies a created label.
type Label struct {
	ID       uint32
	PublicID types.PublicID
}

// AttachArgs names a task/label pair.
type AttachArgs struct {
	TaskID        uint32
	LabelID       uint32
	LabelPublicID types.PublicID
	Actor         mutationlog.Actor
	// Via is recorded in the event payload when non-empty ("mcp").
	Via string
}

func actorFields(a mutationlog.Actor) (user, agent *int64) {
	if a.AgentID != 0 {
		v := int64(a.AgentID)
		return nil, &v
	}
	if a.UserID != 0 {
		v := int64(a.UserID)
		return &v, nil
	}
	return nil, nil
}

func nullInt32(n sql.NullInt64) sql.NullInt32 {
	return sql.NullInt32{Int32: int32(n.Int64), Valid: n.Valid} //#nosec G115 -- internal ids, bounded by realistic deployments
}

// Create inserts a label and appends label.created.
func Create(ctx context.Context, tx *dbretry.Tx, args CreateArgs) (Label, error) {
	pub := types.New()
	var createdBy sql.NullInt32
	if args.Actor.UserID != 0 {
		createdBy = sql.NullInt32{Int32: int32(args.Actor.UserID), Valid: true} //#nosec G115 -- actor user id, bounded by realistic deployments
	}
	id, err := generated.New(tx).CreateLabel(ctx, generated.CreateLabelParams{
		PublicID:        pub,
		WorkspaceID:     args.WorkspaceID,
		ProjectID:       nullInt32(args.ProjectID),
		ParentLabelID:   nullInt32(args.ParentID),
		CreatedByUserID: createdBy,
		Name:            args.Name,
		Color:           args.Color,
		Description:     sql.NullString{String: args.Description, Valid: args.Description != ""},
		SortWeight:      0,
	})
	if err != nil {
		if apierr.IsDuplicateEntry(err) {
			return Label{}, ErrNameTaken
		}
		return Label{}, err
	}
	payload := map[string]any{"labelId": pub.String(), "name": args.Name}
	if args.Via != "" {
		payload["via"] = args.Via
	}
	user, agent := actorFields(args.Actor)
	if err := eventbus.Append(ctx, tx, eventbus.Event{
		Type:         eventbus.LabelCreated,
		WorkspaceID:  args.WorkspaceID,
		ActorUserID:  user,
		ActorAgentID: agent,
		Payload:      payload,
	}); err != nil {
		return Label{}, err
	}
	return Label{ID: uint32(id), PublicID: pub}, nil //#nosec G115 -- LastInsertId of a BIGINT UNSIGNED id that fits uint32 here
}

// Attach links a label to a task and appends task.label.added.
func Attach(ctx context.Context, tx *dbretry.Tx, args AttachArgs) error {
	if _, err := generated.New(tx).CreateTaskLabel(ctx, generated.CreateTaskLabelParams{
		PublicID:    types.New(),
		WorkspaceID: args.Actor.WorkspaceID,
		TaskID:      args.TaskID,
		LabelID:     args.LabelID,
		SortWeight:  0,
	}); err != nil {
		if apierr.IsDuplicateEntry(err) {
			return ErrAlreadyAttached
		}
		return err
	}
	return appendTaskLabelEvent(ctx, tx, eventbus.TaskLabelAdded, args)
}

// Detach unlinks a label from a task and appends task.label.removed.
// It returns ErrNotAttached, writing nothing, when the task does not
// carry the label.
func Detach(ctx context.Context, tx *dbretry.Tx, args AttachArgs) error {
	q := generated.New(tx)
	if _, err := q.FindTaskLabelByIds(ctx, generated.FindTaskLabelByIdsParams{
		WorkspaceID: args.Actor.WorkspaceID,
		TaskID:      args.TaskID,
		LabelID:     args.LabelID,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotAttached
		}
		return err
	}
	if err := q.DisableTaskLabel(ctx, generated.DisableTaskLabelParams{
		WorkspaceID: args.Actor.WorkspaceID,
		TaskID:      args.TaskID,
		LabelID:     args.LabelID,
	}); err != nil {
		return err
	}
	return appendTaskLabelEvent(ctx, tx, eventbus.TaskLabelRemoved, args)
}

func appendTaskLabelEvent(ctx context.Context, tx *dbretry.Tx, kind eventbus.Kind, args AttachArgs) error {
	payload := map[string]any{"labelId": args.LabelPublicID.String()}
	if args.Via != "" {
		payload["via"] = args.Via
	}
	taskID := int64(args.TaskID)
	user, agent := actorFields(args.Actor)
	return eventbus.Append(ctx, tx, eventbus.Event{
		Type:         kind,
		WorkspaceID:  args.Actor.WorkspaceID,
		ActorUserID:  user,
		ActorAgentID: agent,
		TaskID:       &taskID,
		Payload:      payload,
	})
}
