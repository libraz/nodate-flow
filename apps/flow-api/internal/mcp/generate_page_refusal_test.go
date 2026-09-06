package mcp_test

import (
	"context"
	"database/sql"
	stderrors "errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// unreachableProvider stands in for a model server that cannot be
// reached, which is the one failure a workspace with a correctly
// configured provider still meets in normal operation.
type unreachableProvider struct{}

func (unreachableProvider) Name() string         { return "unreachable" }
func (unreachableProvider) Kind() providers.Kind { return providers.Kind("openai_compat") }
func (unreachableProvider) Model() string        { return "unreachable-model" }
func (unreachableProvider) Complete(_ context.Context, _ providers.Request) (*providers.Response, error) {
	return nil, stderrors.New("dial tcp: connection refused")
}

// cannedOrchestrator wires a provider that answers every completion with
// a fixed body, so a test decides exactly what the model returns.
func cannedOrchestrator(text string) *ai.Orchestrator {
	return &ai.Orchestrator{
		Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return &cannedProvider{text: text}, nil
		}),
	}
}

// pagesInWorkspace counts every page row a workspace holds, however it
// was made. The fixture creates none, so this is what "nothing was
// written" means for a refused generation.
func pagesInWorkspace(t *testing.T, db *sql.DB, wsID uint32) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM pages WHERE workspace_id = ?`, wsID).Scan(&n))
	return n
}

// TestGeneratePageRefusesAGenerationThatDidNotHappen drives the routes on
// which no draft comes back and pins that each one refuses and writes
// nothing.
//
// The tool used to answer all of them the same way: it stored a page
// whose body was the caller's own brief, flagged it as not
// AI-generated, and reported success. A caller that asked for a drafted
// page therefore got its own question back — filed in the workspace's
// wiki, under a title that says "Generated", with an ok. Nothing in the
// answer distinguished that from a real draft except a boolean the
// caller had to think to read, and by then the page existed.
//
// So each case asserts two things: the refusal names the condition, and
// the workspace still holds no pages.
func TestGeneratePageRefusesAGenerationThatDidNotHappen(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()

	const brief = "Summarise how the release process works."

	cases := []struct {
		name string
		ai   *ai.Orchestrator
		want *apierrors.Spec
		why  string
	}{
		{
			name: "no provider is wired on the deployment",
			ai:   nil,
			want: apierrors.AiProviderNotConfigured,
			why:  "there is nothing to draft with, which is what the REST page-generation route answers too",
		},
		{
			name: "the provider could not be reached",
			ai: &ai.Orchestrator{
				Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
					return unreachableProvider{}, nil
				}),
			},
			want: apierrors.AiProviderUpstreamUnreachable,
			why:  "a call that never completed is retryable, and the caller can only know that if it is told",
		},
		{
			name: "the provider answered with no draft at all",
			ai:   cannedOrchestrator(`[]`),
			want: apierrors.PageGenerationUpstreamUnavailable,
			why:  "an empty answer is a response with no page in it",
		},
		{
			name: "the provider answered with an empty body",
			ai:   cannedOrchestrator(`[{"title":"Release process","description":"","priority":"medium"}]`),
			want: apierrors.PageGenerationUpstreamUnavailable,
			why:  "a page with a title and no body is not the draft that was asked for",
		},
		{
			name: "the provider answered with something that is not JSON",
			ai:   cannedOrchestrator(`I am afraid I cannot help with that.`),
			want: apierrors.AiResponseInvalidJson,
			why:  "an answer that could not be read is not an answer the caller should find stored as a page",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := seedMCPVisibilityFixture(t, db)
			deps := mcp.Deps{DB: db, Queries: generated.New(db), AI: tc.ai}
			caller := mcp.NewTestSession(fx.creatorID, fx.wsID, []string{"write:workspace"})

			require.Equal(t, 0, pagesInWorkspace(t, db, fx.wsID),
				"the fixture has to start with no pages for the assertion below to mean anything")

			_, err := mcp.RunGeneratePage(ctx, deps, caller, mcpVisJSON(t, map[string]any{
				"contextDescription": brief,
			}))
			requireSpec(t, err, tc.want)

			require.Equalf(t, 0, pagesInWorkspace(t, db, fx.wsID),
				"a generation that did not happen must leave nothing behind: %s", tc.why)
		})
	}
}

// TestGeneratePageStoresTheDraftItWasGiven is the control for the
// refusals above.
//
// Without it, "refuses and writes nothing" is also what an
// implementation that refuses everything produces. It additionally pins
// the two properties a caller reads a success for: the stored body is
// the model's, not the brief that was sent, and the row says it was
// drafted.
func TestGeneratePageStoresTheDraftItWasGiven(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPVisibilityFixture(t, db)
	ctx := context.Background()

	const brief = "Summarise how the release process works."
	const drafted = "The release runs from develop, is fast-forwarded onto main, and is tagged last."

	deps := mcp.Deps{
		DB:      db,
		Queries: generated.New(db),
		AI:      cannedOrchestrator(`[{"title":"Release process","description":"` + drafted + `","priority":"medium"}]`),
	}
	caller := mcp.NewTestSession(fx.creatorID, fx.wsID, []string{"write:workspace"})

	out, err := mcp.RunGeneratePage(ctx, deps, caller, mcpVisJSON(t, map[string]any{
		"contextDescription": brief,
	}))
	require.NoError(t, err)

	res, ok := out.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, res["isAiGenerated"])
	pageID, ok := res["id"].(string)
	require.True(t, ok)

	var title, body string
	var isAI bool
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT title, body, is_ai_generated FROM pages WHERE public_id = UUID_TO_BIN(?, 0) AND workspace_id = ?`,
		pageID, fx.wsID).Scan(&title, &body, &isAI))

	require.Equal(t, drafted, body, "the stored body has to be the model's draft")
	require.NotEqual(t, brief, body, "the brief is the question, never the page")
	require.Equal(t, "Release process", title)
	require.True(t, isAI, "a page drafted by a model has to be stored as one")
}
