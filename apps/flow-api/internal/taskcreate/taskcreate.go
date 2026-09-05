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
// The first version of the task's description is written here too, for
// the same reason: it is the only copy of the body the task started with,
// and every creating path passes through this function.
//
// Who the task belongs to travels separately, as the [Attribution]
// argument: it is the one answer a caller must give in words rather than
// by omission.
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
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskdesc"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/tasknumber"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
)

// ErrVisibilityInvalid is returned when [Args.Visibility] is non-empty
// and outside the tasks.visibility enum. The REST DTO layer already
// constrains the value with an enum tag; this is the defence for
// transports that have no schema in front of them, such as MCP.
var ErrVisibilityInvalid = errors.New("taskcreate: unknown task visibility")

// DefaultVisibility is the audience a task gets when the caller does not
// choose one. It matches the tasks.visibility column default.
const DefaultVisibility = generated.TasksVisibilityPublic

// ErrActorRoleInvalid is returned when an [Actor] carries a role outside
// the task_actors.role enum. Same defence as [ErrVisibilityInvalid], for
// the same reason: the INSERT names the column, so an unknown value
// reaches MySQL rather than falling back to the default.
var ErrActorRoleInvalid = errors.New("taskcreate: unknown task actor role")

// DefaultActorRole is the role an [Actor] gets when the caller does not
// choose one. It matches the task_actors.role column default.
const DefaultActorRole = generated.TaskActorsRoleAssignee

// Actor is one task_actors row the new task starts with, already
// resolved to an internal user id by the caller. Resolution stays with
// the caller because it is the caller that knows which failure a user id
// nobody can find should be reported as.
type Actor struct {
	UserID uint32
	// Role is optional. The zero value means [DefaultActorRole], which is
	// what the column default applies — the INSERT names the column, so
	// the default cannot rescue a zero-valued field on its own.
	Role generated.TaskActorsRole
}

// Attribution answers who a new task belongs to: the user its row records
// as creator, and the actors it starts with.
//
// It is a positional argument to [New] rather than another field on
// [Args] because both halves of the answer are the kind a caller gives by
// omission. A creator left unset compiles and stores a task nobody is
// recorded as having made; an assignee list depends on nothing but
// whether somebody wrote the insert after the create. Requiring the
// answer by position means a creating path cannot exist without stating
// it, including when the honest answer is [Unattributed].
type Attribution struct {
	createdBy sql.NullInt32
	actors    []Actor
}

// SelfAssigned attributes the task to userID and makes them its sole
// assignee.
func SelfAssigned(userID uint32) Attribution {
	return Attribution{
		createdBy: userRef(userID),
		actors:    []Actor{{UserID: userID, Role: generated.TaskActorsRoleAssignee}},
	}
}

// AuthoredBy records userID as the creator and gives the task no actors.
// Use it where the person who asked for the task is not thereby the
// person the task is waiting on.
func AuthoredBy(userID uint32) Attribution {
	return Attribution{createdBy: userRef(userID)}
}

// Unattributed records no creator and no actors, for a row a process
// wrote rather than a person. An agent-drafted task is attributed on
// events.actor_agent_id instead, which is a fact about the event and not
// about the task.
func Unattributed() Attribution {
	return Attribution{}
}

// WithActors replaces the attribution's actors with an explicit list.
// The recorded creator is unaffected, so a caller can name somebody else
// as the assignee without losing who made the task.
func (a Attribution) WithActors(actors ...Actor) Attribution {
	a.actors = actors
	return a
}

// userRef narrows an internal user id onto the nullable column shape the
// generated insert takes.
func userRef(userID uint32) sql.NullInt32 {
	return sql.NullInt32{Int32: int32(userID), Valid: true} //#nosec G115 -- users.id (INT UNSIGNED), fits int32 within realistic deployments
}

// Args carries everything a caller must decide about a new task.
//
// Fields this package owns are deliberately absent: public_id and
// task_number are generated here, and derived_state belongs to the event
// bus and must never be set on insert. Who the task belongs to is absent
// for a different reason — it is [Attribution], a positional argument, so
// that it cannot be skipped.
type Args struct {
	WorkspaceID uint32
	ProjectID   uint32
	// ParentTaskID is the internal id of the parent task for subtasks and
	// decomposition steps. Zero value means a top-level task.
	ParentTaskID sql.NullInt32
	// Title carries the title rule with it: the only way to hold one is
	// taskrules.NewTitle, so no call site can reach this insert with a
	// title nobody checked.
	Title       taskrules.Title
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
//
// A transaction that reads anything before its first call to New must
// call [LockProject] first. See that function for what goes wrong
// otherwise.
//
// The attribution's actor rows are inserted here, in the same
// transaction, so a task and the people it starts with either both exist
// or neither does.
func New(ctx context.Context, tx *dbretry.Tx, attr Attribution, args Args) (Result, error) {
	// The zero Title is the one value the type cannot rule out: a caller
	// that omits the field entirely still compiles. Refusing it here
	// keeps "every stored title passed the rule" true rather than nearly
	// true.
	if args.Title.String() == "" {
		return Result{}, taskrules.ErrTitleEmpty
	}
	// The date rule is not carried by a type, so it is applied here rather
	// than at each caller. Both dates are plain fields a caller can fill
	// without passing anything, and most creating paths write neither, so
	// the ones that do would each have to remember. Applying it at the
	// insert makes "no stored task starts after it is due" hold for every
	// path that reaches this function, including those that take their
	// dates from a file rather than from a request.
	if err := taskrules.DateOrder(args.DueOn, args.StartedOn); err != nil {
		return Result{}, err
	}
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
		CreatedByUserID: attr.createdBy,
		UpdatedByUserID: attr.createdBy,
		TaskNumber:      taskNumber,
		Title:           args.Title.String(),
		Description:     args.Description,
		Priority:        args.Priority,
		DueOn:           args.DueOn,
		StartedOn:       args.StartedOn,
		Visibility:      visibility,
	})
	if err != nil {
		return Result{}, fmt.Errorf("taskcreate: insert task: %w", err)
	}
	// The body a task is created with is the first version of its
	// description, and it is the one a restore has to be able to return
	// to: once the description is edited, nothing else holds the original.
	// Written here rather than at each caller for the same reason the task
	// number is — every creating path reaches this function, and a path
	// that skipped the snapshot would produce a task whose history is
	// missing its own starting point with nothing to signal it.
	if _, err := taskdesc.Snapshot(ctx, q, args.WorkspaceID, uint32(id), attr.createdBy, args.Description.String); err != nil { //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		return Result{}, err
	}
	// The actors are part of the same answer as the recorded creator, so
	// they are written where that answer arrives. A caller that inserted
	// them itself could forget to, and a task nobody is attached to is
	// indistinguishable from one deliberately left unassigned.
	for _, a := range attr.actors {
		role, rerr := resolveActorRole(a.Role)
		if rerr != nil {
			return Result{}, rerr
		}
		if _, err := q.AddActor(ctx, generated.AddActorParams{
			PublicID:    types.New(),
			WorkspaceID: args.WorkspaceID,
			TaskID:      uint32(id), //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
			UserID:      userRef(a.UserID),
			Role:        role,
		}); err != nil {
			return Result{}, fmt.Errorf("taskcreate: attach actor: %w", err)
		}
	}
	// Every transport reaches the tasks table through this one call, so
	// the counter needs no other instrumentation point. The caller owns
	// the commit, so a transaction that rolls back after this point
	// leaves the counter ahead of the table by that one row.
	obs.IncTaskCreated()
	return Result{ID: id, PublicID: pub, TaskNumber: taskNumber}, nil
}

// LockProject takes the per-project lock that task-number allocation runs
// under. [New] takes it itself, so a transaction whose first statement is
// New needs nothing else.
//
// A transaction that must read something before it creates — resolving an
// actor's public id to an internal one, say — has to call this first.
// Under REPEATABLE READ the transaction's snapshot is fixed by its first
// ordinary read, and the task number is allocated with
// SELECT MAX(task_number): a read taken before the lock pins the snapshot
// to a moment before the previous holder committed, so the allocation
// hands back a number the project already uses and the insert dies on
// uniq_tasks_project_id_task_number. Taking the lock first puts the
// snapshot after the wait, where the allocation can see everything the
// previous holder wrote.
//
// A locking read in the allocation would carry this without the caller's
// help, and does not work: the aggregate form locks every task row of the
// project, and the narrow form's gap lock reaches into the neighbouring
// project, so parallel creators deadlock. The SQL comment on
// AssignTaskNumber records what would.
func LockProject(ctx context.Context, tx *dbretry.Tx, workspaceID, projectID uint32) error {
	if _, err := generated.New(tx).LockProjectForTaskNumber(ctx, generated.LockProjectForTaskNumberParams{
		WorkspaceID: workspaceID,
		ID:          projectID,
	}); err != nil {
		return fmt.Errorf("taskcreate: lock project: %w", err)
	}
	return nil
}

// resolveActorRole maps an actor's optional role onto the enum, on the
// same terms as resolveVisibility: the zero value becomes the column
// default and anything outside the closed set is refused before it can
// reach MySQL, where STRICT_TRANS_TABLES would report it as a truncated
// value far from the call site that chose it.
func resolveActorRole(r generated.TaskActorsRole) (generated.TaskActorsRole, error) {
	switch r {
	case "":
		return DefaultActorRole, nil
	case generated.TaskActorsRoleAssignee,
		generated.TaskActorsRoleReviewer,
		generated.TaskActorsRoleWatcher,
		generated.TaskActorsRoleApprover:
		return r, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrActorRoleInvalid, string(r))
	}
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
