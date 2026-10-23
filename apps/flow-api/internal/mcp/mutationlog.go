package mcp

import (
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

// MCP tools record their changes through the same recorder the REST
// handlers use, so an event row commits in the same transaction as the
// change it describes and the audit row is written after commit — on
// both transports, by one implementation. mutation_static_test.go holds
// every write-scoped tool to reaching a recorder entry point and keeps
// eventbus.Append* and the audit recorder out of this package.

// recorder returns the shared mutation recorder over this handler's
// handles. It is stateless, so building one per call keeps the handler's
// dependency bundle — and the router wiring that fills it — unchanged.
func recorder(deps Deps) *mutationlog.Recorder {
	return mutationlog.New(deps.DB, deps.Queries)
}

// mutationActor is the actor a tool's change is recorded under: the
// token's user in the token's workspace, as the event and audit rows
// have always named it.
func mutationActor(s *session) mutationlog.Actor {
	return mutationlog.Actor{UserID: s.userID, WorkspaceID: s.workspaceID}
}
