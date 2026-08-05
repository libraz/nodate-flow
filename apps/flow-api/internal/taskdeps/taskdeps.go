// Package taskdeps owns the one way a task_dependencies edge is
// written. Every writer goes through [Add] so the guarantees that make
// the dependency graph usable — it stays a DAG, and every edge has a
// timeline row — hold for all of them rather than for whichever call
// site remembered them.
//
// The graph being acyclic is not cosmetic: `dependency.all_done`
// constraints are evaluated by walking it, and the walk is
// cycle-tolerant, so a cycle does not fail loudly. It leaves the
// constraint permanently unsatisfiable while every screen still looks
// healthy.
package taskdeps

import (
	"context"
	"database/sql"
	"errors"

	"github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// mysqlDuplicateEntry is ER_DUP_ENTRY (1062). Checked locally so this
// package does not depend on the HTTP handler helpers.
const mysqlDuplicateEntry uint16 = 1062

func isDuplicateEntry(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == mysqlDuplicateEntry
}

// ErrCycle is returned when the requested edge would close a cycle.
// Callers map it onto their own surface (the REST handler answers
// WS.TASK.DEPENDENCY_CYCLE).
var ErrCycle = errors.New("taskdeps: edge closes a dependency cycle")

// ErrDuplicate is returned when the edge already exists. Whether that
// is an error or a no-op is the caller's decision: accepting a relation
// suggestion for an edge somebody already drew is success, while asking
// for the same edge twice over the API is not.
var ErrDuplicate = errors.New("taskdeps: edge already exists")

// Args describes one edge. Internal ids drive the writes; the public
// ids are only used to build the event payload.
type Args struct {
	WorkspaceID uint32

	FromTaskID    uint32
	FromProjectID uint32
	ToTaskID      uint32
	ToProjectID   uint32

	Kind generated.TaskDependenciesKind

	// ActorUserID is the internal id of the acting user, nil for system
	// writers.
	ActorUserID *int64

	// FromTaskPublicID / ToTaskPublicID appear in the event payload.
	FromTaskPublicID string
	ToTaskPublicID   string

	// Via records how the edge came to be ("api", "relation.accept") in
	// the event payload.
	Via string
}

// Add locks both endpoint projects, rejects an edge that would close a
// cycle, inserts it, and appends the timeline event — all on the
// supplied transaction.
//
// The lock is what makes the check meaningful. Without it two
// concurrent writers (A→B and B→A) each read an edge set that misses
// the other's insert, both pass the check and both commit, forming the
// cycle neither request could see. Locking both endpoint projects in
// ascending id order serialises check-then-insert without letting two
// cross-project writers deadlock on each other.
//
// tx must come from dbretry.InTx: the insert can lose a lock race with
// concurrent transition transactions on the same task rows, and that
// has to be retried as a whole transaction. The event append also needs
// InTx's commit boundary to fan out.
func Add(ctx context.Context, tx *sql.Tx, args Args) (types.PublicID, error) {
	q := generated.New(tx)

	first, second := args.FromProjectID, args.ToProjectID
	if second < first {
		first, second = second, first
	}
	if _, err := q.LockProjectForDependency(ctx, generated.LockProjectForDependencyParams{
		WorkspaceID: args.WorkspaceID,
		ID:          first,
	}); err != nil {
		return types.PublicID{}, err
	}
	if second != first {
		if _, err := q.LockProjectForDependency(ctx, generated.LockProjectForDependencyParams{
			WorkspaceID: args.WorkspaceID,
			ID:          second,
		}); err != nil {
			return types.PublicID{}, err
		}
	}

	// Read the edge set only after the locks are granted: under
	// REPEATABLE READ the consistent snapshot is established at the
	// first plain SELECT, which here runs after any concurrent
	// dependency writer on these projects has committed, so the walk
	// sees that writer's edge.
	edges, err := q.ListDependencyEdgesForWorkspace(ctx, args.WorkspaceID)
	if err != nil {
		return types.PublicID{}, err
	}
	if ClosesCycle(edges, args.FromTaskID, args.ToTaskID) {
		return types.PublicID{}, ErrCycle
	}

	pub := types.New()
	if _, err := q.AddDependency(ctx, generated.AddDependencyParams{
		PublicID:    pub,
		WorkspaceID: args.WorkspaceID,
		FromTaskID:  args.FromTaskID,
		ToTaskID:    args.ToTaskID,
		Kind:        args.Kind,
	}); err != nil {
		if isDuplicateEntry(err) {
			return types.PublicID{}, ErrDuplicate
		}
		return types.PublicID{}, err
	}

	fromTask := int64(args.FromTaskID)
	payload := map[string]any{
		"taskId":       args.FromTaskPublicID,
		"dependencyId": pub.String(),
		"toTaskId":     args.ToTaskPublicID,
		"kind":         string(args.Kind),
	}
	if args.Via != "" {
		payload["via"] = args.Via
	}
	if err := eventbus.Append(ctx, tx, eventbus.Event{
		Type:        eventbus.TaskDependencyAdded,
		WorkspaceID: args.WorkspaceID,
		ActorUserID: args.ActorUserID,
		TaskID:      &fromTask,
		Payload:     payload,
	}); err != nil {
		return types.PublicID{}, err
	}
	return pub, nil
}

// ClosesCycle reports whether adding from -> to would close a cycle in
// the edge set. It is exported so callers that need to answer "would
// this be rejected" without writing can reuse the exact walk [Add]
// applies.
func ClosesCycle(edges []generated.ListDependencyEdgesForWorkspaceRow, from, to uint32) bool {
	if from == to {
		return true
	}
	adj := make(map[uint32][]uint32, len(edges))
	for _, e := range edges {
		adj[e.FromTaskID] = append(adj[e.FromTaskID], e.ToTaskID)
	}
	visited := make(map[uint32]bool)
	stack := []uint32{to}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == from {
			return true
		}
		if visited[n] {
			continue
		}
		visited[n] = true
		stack = append(stack, adj[n]...)
	}
	return false
}
