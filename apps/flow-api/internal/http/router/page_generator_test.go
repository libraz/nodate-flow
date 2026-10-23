package router

import "testing"

// TestPageGenerationIsWiredToTheAIOrchestrator holds the pages routes to
// the orchestrator the other AI surfaces run on. With the generator left
// unset, page generation answered AI.PROVIDER.NOT_CONFIGURED in every
// deployment, configured or not.
func TestPageGenerationIsWiredToTheAIOrchestrator(t *testing.T) {
	t.Parallel()

	deps := stubDeps(t)
	deps.AiMock = true
	shared := buildSharedDeps(deps)
	if shared.aiOrch == nil {
		t.Fatal("fixture: NF_AI_MOCK must yield an orchestrator for the check to mean anything")
	}
	if newPageDeps(deps, shared).Generator == nil {
		t.Fatal("page generation has no generator although an AI orchestrator is configured")
	}

	none := buildSharedDeps(stubDeps(t))
	if none.aiOrch == nil && newPageDeps(stubDeps(t), none).Generator != nil {
		t.Fatal("with no orchestrator the generator must be a nil interface, so the handler answers NOT_CONFIGURED")
	}
}
