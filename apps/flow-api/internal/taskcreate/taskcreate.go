// Package taskcreate is the single writer for new rows in tasks.
//
// Callers state what they decided — title, project, parent, visibility.
// Everything the row needs but nobody chooses per call site — the public
// id and the per-project task number — is owned here. That split is the
// point of the package: a caller cannot forget a required column,
// because the columns that used to be forgotten are not part of its
// vocabulary any more.
//
// Two columns were repeatedly missed while every call site hand-built
// generated.CreateTaskParams:
//
//   - task_number defaults to 0 and carries
//     UNIQUE KEY uniq_tasks_project_id_task_number, so the first task
//     created without allocation succeeds and the second one in the same
//     project fails on a duplicate key. Single-row tests never see it.
//   - visibility is an ENUM listed explicitly by the INSERT, so the
//     column DEFAULT never applies and a zero-valued Go field reaches
//     MySQL as ”, which STRICT_TRANS_TABLES rejects outright.
//
// taskcreate does NOT authorize the actor and does NOT append
// task.created; callers own both. Event emission is deliberately left
// out because call sites differ on whether they publish before commit,
// after commit, or from a commit hook.
package taskcreate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/obs"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/tasknumber"
)

// ErrVisibilityInvalid is returned when [Args.Visibility] is non-empty
// and outside the tasks.visibility enum. The REST DTO layer already
// constrains the value with an enum tag; this is the defence for
// transports that have no schema in front of them, such as MCP.
var ErrVisibilityInvalid = errors.New("taskcreate: unknown task visibility")

// DefaultVisibility is the audience a task gets when the caller does not
// choose one. It matches the tasks.visibility column default.
const DefaultVisibility = generated.TasksVisibilityPublic

// Args carries everything a caller must decide about a new task.
//
// Fields this package owns are deliberately absent: public_id and
// task_number are generated here, and derived_state belongs to the event
// bus and must never be set on insert.
type Args struct {
	WorkspaceID uint32
	ProjectID   uint32
	// ParentTaskID is the internal id of the parent task for subtasks and
	// decomposition steps. Zero value means a top-level task.
	ParentTaskID sql.NullInt32
	// ActorUserID is the creating user. Leave it invalid for
	// agent-authored rows, where attribution lives on
	// events.actor_agent_id rather than on the task.
	ActorUserID sql.NullInt32
	Title       string
	Description sql.NullString
	Priority    int32
	DueOn       sql.NullTime
	StartedOn   sql.NullTime
	// Visibility is optional. The zero value means [DefaultVisibility],
	// which is what the column default and the REST create handler both
	// apply when the request omits it.
	Visibility generated.TasksVisibility
}

// Result is what callers need after the insert: the internal id for
// foreign keys and event stamping, and the public id for API responses
// and event payloads.
type Result struct {
	ID         int64
	PublicID   types.PublicID
	TaskNumber uint32
}

// New allocates the next per-project task number and inserts one task.
//
// It takes the transaction rather than a *generated.Queries because
// allocation runs SELECT ... FOR UPDATE on the project row: taking a
// *sql.Tx makes "this must run inside a transaction" a fact the compiler
// checks instead of a comment a caller can miss. Callers that need
// retry-on-deadlock semantics should obtain the tx from dbretry.InTx.
//
// Creating several tasks in one transaction is supported and expected:
// allocation reads MAX(task_number) within the transaction, so each
// successive call sees the rows the previous ones inserted.
func New(ctx context.Context, tx *dbretry.Tx, args Args) (Result, error) {
	visibility, err := resolveVisibility(args.Visibility)
	if err != nil {
		return Result{}, err
	}

	q := generated.New(tx)
	nextNum, err := tasknumber.Allocate(ctx, q, args.WorkspaceID, args.ProjectID)
	if err != nil {
		return Result{}, fmt.Errorf("taskcreate: allocate task number: %w", err)
	}
	taskNumber := uint32(nextNum) //#nosec G115 -- per-project sequence, fits uint32

	pub := types.New()
	id, err := q.CreateTask(ctx, generated.CreateTaskParams{
		PublicID:        pub,
		WorkspaceID:     args.WorkspaceID,
		ProjectID:       args.ProjectID,
		ParentTaskID:    args.ParentTaskID,
		CreatedByUserID: args.ActorUserID,
		UpdatedByUserID: args.ActorUserID,
		TaskNumber:      taskNumber,
		Title:           args.Title,
		Description:     args.Description,
		Priority:        args.Priority,
		DueOn:           args.DueOn,
		StartedOn:       args.StartedOn,
		Visibility:      visibility,
	})
	if err != nil {
		return Result{}, fmt.Errorf("taskcreate: insert task: %w", err)
	}
	// Every transport reaches the tasks table through this one call, so
	// the counter needs no other instrumentation point. The caller owns
	// the commit, so a transaction that rolls back after this point
	// leaves the counter ahead of the table by that one row.
	obs.IncTaskCreated()
	return Result{ID: id, PublicID: pub, TaskNumber: taskNumber}, nil
}

// resolveVisibility maps the caller's optional choice onto the enum,
// substituting the default for the zero value and rejecting anything
// outside the closed set. An unknown value must not reach MySQL: the
// INSERT names the column explicitly, so the column default cannot
// rescue it and STRICT_TRANS_TABLES turns it into a truncation error
// far from its cause.
func resolveVisibility(v generated.TasksVisibility) (generated.TasksVisibility, error) {
	switch v {
	case "":
		return DefaultVisibility, nil
	case generated.TasksVisibilityPublic,
		generated.TasksVisibilityProject,
		generated.TasksVisibilityPrivate:
		return v, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrVisibilityInvalid, string(v))
	}
}
