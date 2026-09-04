package ai

import (
	"context"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// stubEmbedProvider answers every text with the same unit vector and
// counts the calls, which is what says whether the budget guard ran
// before or after the tokens were spent.
type stubEmbedProvider struct {
	calls int
}

func (p *stubEmbedProvider) Model() string        { return "stub-embed" }
func (p *stubEmbedProvider) ProviderName() string { return "stub" }
func (p *stubEmbedProvider) Embed(context.Context, string) ([]float32, error) {
	p.calls++
	return []float32{1, 0, 0}, nil
}

// stubEmbedStore satisfies embed.Store. A query embedding writes no row,
// so a call reaching either method is itself the failure.
type stubEmbedStore struct{}

func (stubEmbedStore) GetTaskEmbedding(context.Context, generated.GetTaskEmbeddingParams) (generated.GetTaskEmbeddingRow, error) {
	return generated.GetTaskEmbeddingRow{}, nil
}

func (stubEmbedStore) UpsertTaskEmbedding(context.Context, generated.UpsertTaskEmbeddingParams) error {
	return nil
}

// emptySmartCreateReader has no history to offer, which is enough: what
// is asserted here is the embedding call, not the ranking built from it.
type emptySmartCreateReader struct{}

func (emptySmartCreateReader) ListCandidateTaskEmbeddings(context.Context, generated.ListCandidateTaskEmbeddingsParams) ([]generated.ListCandidateTaskEmbeddingsRow, error) {
	return nil, nil
}

func (emptySmartCreateReader) ListTasksWithAssigneesForSmartCreate(context.Context, generated.ListTasksWithAssigneesForSmartCreateParams) ([]generated.ListTasksWithAssigneesForSmartCreateRow, error) {
	return nil, nil
}

func (emptySmartCreateReader) ListWorkspaceMembersForSmartCreate(context.Context, uint32) ([]generated.ListWorkspaceMembersForSmartCreateRow, error) {
	return nil, nil
}

// meteredEmbedClient builds the embed client the proposal methods take,
// with its metering wired to the collectors the caller inspects.
func meteredEmbedClient(prov *stubEmbedProvider, logged *[]embed.InvocationRecord, samples *[]sample) *embed.Client {
	return embed.New(prov, stubEmbedStore{}).WithMetering(
		nil,
		func(_ context.Context, rec embed.InvocationRecord) { *logged = append(*logged, rec) },
		func(provider, model string, inTok, outTok int, cost int64, elapsed time.Duration, err error) {
			*samples = append(*samples, sample{provider, model, inTok, outTok, cost, elapsed, err})
		},
		nil,
	)
}

// TestProposalEmbeddingsAreMetered pins the accounting for the embedding
// each proposal method makes before it calls the LLM. The vector is used
// once and discarded, so the invocation hook and the ai_invocations row
// are the only evidence the workspace was billed for it, and the daily
// budget is only enforced on the path that produces them.
func TestProposalEmbeddingsAreMetered(t *testing.T) {
	t.Parallel()

	const workspaceID uint32 = 42

	cases := []struct {
		name     string
		response string
		call     func(o *Orchestrator, ec EmbedClient) error
	}{
		{
			name:     "smart create",
			response: `{"suggestedAssignees":[],"subtasks":[{"title":"t","description":"d","priority":"low"}]}`,
			call: func(o *Orchestrator, ec EmbedClient) error {
				_, err := o.ProposeSmartCreate(context.Background(), workspaceID,
					"ship the thing", "with a description",
					ec, emptySmartCreateReader{}, acl.VisibilityArgs{})
				return err
			},
		},
		{
			name:     "propose steps",
			response: `[{"title":"t","description":"d","priority":"low"}]`,
			call: func(o *Orchestrator, ec EmbedClient) error {
				_, err := o.ProposeSteps(context.Background(), workspaceID,
					"ship the thing", "with a description",
					GranularityStandard, nil,
					ec, emptySmartCreateReader{}, acl.VisibilityArgs{})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			embedProv := &stubEmbedProvider{}
			var logged []embed.InvocationRecord
			var samples []sample
			client := meteredEmbedClient(embedProv, &logged, &samples)

			llm := &labelProvider{
				model: "claude-sonnet-4-6",
				resp:  &providers.Response{Model: "claude-sonnet-4-6", Text: tc.response},
			}
			o := &Orchestrator{
				Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
					return llm, nil
				}),
			}

			if err := tc.call(o, client); err != nil {
				t.Fatalf("proposal: %v", err)
			}
			if embedProv.calls != 1 {
				t.Fatalf("embedding provider calls = %d, want 1", embedProv.calls)
			}
			if len(samples) != 1 {
				t.Fatalf("embedding metrics samples = %d, want 1: the call is invisible to the metric", len(samples))
			}
			if samples[0].err != nil {
				t.Errorf("a successful embedding reported err = %v", samples[0].err)
			}
			if len(logged) != 1 {
				t.Fatalf("ai_invocations rows = %d, want 1: the spend leaves no trace", len(logged))
			}
			if logged[0].WorkspaceID != workspaceID || logged[0].Purpose != "embed_query" {
				t.Errorf("unexpected invocation record: %+v", logged[0])
			}
			// The workspace this spend belongs to is carried by the
			// ai_invocations row above; what the metric has to carry is the
			// size of the call, counted the same way the row counts it.
			if samples[0].inTok != logged[0].TokensInput {
				t.Errorf("metric input tokens = %d, want the logged row's %d",
					samples[0].inTok, logged[0].TokensInput)
			}
		})
	}
}

// TestProposalEmbeddingsRespectTheBudget pins that the proposal methods
// go through the guarded path: a workspace already past its daily cap
// must not reach the embedding provider at all.
//
// Only smart create refuses outright. Similar-task context is optional
// for propose-steps, which answers without it when the embedding does
// not come back — but either way the tokens must not be spent.
func TestProposalEmbeddingsRespectTheBudget(t *testing.T) {
	t.Parallel()

	const workspaceID uint32 = 42

	cases := []struct {
		name     string
		response string
		wantErr  bool
		call     func(o *Orchestrator, ec EmbedClient) error
	}{
		{
			name:     "smart create",
			response: `{"suggestedAssignees":[],"subtasks":[]}`,
			wantErr:  true,
			call: func(o *Orchestrator, ec EmbedClient) error {
				_, err := o.ProposeSmartCreate(context.Background(), workspaceID,
					"ship the thing", "with a description",
					ec, emptySmartCreateReader{}, acl.VisibilityArgs{})
				return err
			},
		},
		{
			name:     "propose steps",
			response: `[{"title":"t","description":"d","priority":"low"}]`,
			call: func(o *Orchestrator, ec EmbedClient) error {
				_, err := o.ProposeSteps(context.Background(), workspaceID,
					"ship the thing", "with a description",
					GranularityStandard, nil,
					ec, emptySmartCreateReader{}, acl.VisibilityArgs{})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			embedProv := &stubEmbedProvider{}
			spent := BudgetReaderFunc(func(context.Context, uint32) (int64, error) {
				return DefaultDailyBudgetCents, nil
			})
			client := embed.New(embedProv, stubEmbedStore{}).
				WithMetering(NewCostGuard(spent, 0), nil, nil, nil)

			llm := &labelProvider{
				model: "claude-sonnet-4-6",
				resp:  &providers.Response{Model: "claude-sonnet-4-6", Text: tc.response},
			}
			o := &Orchestrator{
				Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
					return llm, nil
				}),
			}

			err := tc.call(o, client)
			if tc.wantErr && err == nil {
				t.Fatal("a workspace over its daily budget must not get a proposal")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("proposal: %v", err)
			}
			if embedProv.calls != 0 {
				t.Fatalf("embedding provider calls = %d, want 0: the workspace spent past its cap", embedProv.calls)
			}
		})
	}
}
