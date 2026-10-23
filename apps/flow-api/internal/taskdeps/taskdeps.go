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
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

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

	// FromTaskID / ToTaskID are the edge's endpoints. The owning
	// projects are deliberately absent: an earlier version locked them
	// to serialise the cycle check, and carrying them here would invite
	// a caller to assume that is still the serialization point. It is
	// the workspace edge set now; see [Add].
	FromTaskID uint32
	ToTaskID   uint32

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

// Add rejects an edge that would close a cycle, inserts it, and appends
// the timeline event — all on the supplied transaction.
//
// The lock is what makes the check meaningful. Without it two
// concurrent writers each read an edge set that misses the other's
// insert, both pass the check and both commit, forming the cycle
// neither request could see. The lock has to cover everything the walk
// reads, which is the whole workspace: locking only the endpoint
// projects leaves a cycle drawn across four of them open, since two
// writers holding disjoint project pairs never meet. See
// ListDependencyEdgesForWorkspaceForUpdate for why the lock sits on the
// edge rows rather than on the workspace row.
//
// tx must come from dbretry.InTx: the insert can lose a lock race with
// concurrent transition transactions on the same task rows, and that
// has to be retried as a whole transaction. The event append also needs
// InTx's commit boundary to fan out.
func Add(ctx context.Context, tx *dbretry.Tx, args Args) (types.PublicID, error) {
	q := generated.New(tx)

	// Reading the edge set under the lock is a single step: the locking
	// read establishes both the serialization point and the snapshot the
	// walk runs on, so no other writer can add an edge between the two.
	locked, err := q.ListDependencyEdgesForWorkspaceForUpdate(ctx, args.WorkspaceID)
	if err != nil {
		return types.PublicID{}, err
	}
	edges := make([]Edge, len(locked))
	for i, e := range locked {
		edges[i] = Edge{From: e.FromTaskID, To: e.ToTaskID, Kind: string(e.Kind)}
	}
	if ClosesCycle(edges, args.FromTaskID, args.ToTaskID, string(args.Kind)) {
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
		if apierr.IsDuplicateEntry(err) {
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

// Edge is one live dependency edge as the cycle walk sees it.
type Edge struct {
	From, To uint32
	Kind     string
}

// ClosesCycle reports whether adding from -> to of the given kind would
// close a cycle among blocking edges. Informational kinds never gate
// anything, so they neither take part in the walk nor are refused by it.
// It is exported so callers that need to answer "would this be rejected"
// without writing can reuse the exact walk [Add] applies.
func ClosesCycle(edges []Edge, from, to uint32, kind string) bool {
	if !taskrules.IsBlockingDependencyKind(kind) {
		return false
	}
	if from == to {
		return true
	}
	adj := make(map[uint32][]uint32, len(edges))
	for _, e := range edges {
		if !taskrules.IsBlockingDependencyKind(e.Kind) {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
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
