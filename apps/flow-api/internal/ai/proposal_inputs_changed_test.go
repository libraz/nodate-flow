package ai

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// revocableProposalReader loses workspace membership once revoked is set.
type revocableProposalReader struct {
	emptySmartCreateReader
	revoked *atomic.Bool
}

func (r revocableProposalReader) GetProposalCacheWorkspaceAccess(ctx context.Context, arg generated.GetProposalCacheWorkspaceAccessParams) (interface{}, error) {
	if r.revoked.Load() {
		return nil, sql.ErrNoRows
	}
	return r.emptySmartCreateReader.GetProposalCacheWorkspaceAccess(ctx, arg)
}

// TestProposalInputsChangedInFlightIsItsOwnFailure pins that losing access
// while the provider call runs is reported as changed inputs, not as a
// malformed provider response, both to the caller and in the invocation log.
func TestProposalInputsChangedInFlightIsItsOwnFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		response string
		call     func(o *Orchestrator, reader SmartCreateReader) error
	}{
		{
			name:     "smart create",
			response: `{"suggestedAssignees":[],"subtasks":[{"title":"Task 1","description":"d","priority":"low"},{"title":"Task 2","description":"d","priority":"low"}]}`,
			call: func(o *Orchestrator, reader SmartCreateReader) error {
				_, err := o.ProposeSmartCreate(context.Background(), 42, "title", "description", embed.New(&stubEmbedProvider{}, stubEmbedStore{}), reader, acl.VisibilityArgs{ActorUserID: 7})
				return err
			},
		},
		{
			name:     "propose steps",
			response: `[{"title":"Step 1","description":"d","priority":"low"},{"title":"Step 2","description":"d","priority":"low"},{"title":"Step 3","description":"d","priority":"low"},{"title":"Step 4","description":"d","priority":"low"},{"title":"Step 5","description":"d","priority":"low"}]`,
			call: func(o *Orchestrator, reader SmartCreateReader) error {
				_, err := o.ProposeSteps(context.Background(), 42, "title", "description", GranularityStandard, nil, nil, reader, acl.VisibilityArgs{ActorUserID: 7})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			revoked := &atomic.Bool{}
			prov := providerFunc(func(_ context.Context, req providers.Request) (*providers.Response, error) {
				revoked.Store(true)
				return &providers.Response{Model: req.Model, Text: tc.response, Termination: providers.TerminationComplete, InputTokens: 3, OutputTokens: 4}, nil
			})
			var records []InvocationRecord
			o := &Orchestrator{
				Resolver:  ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) { return prov, nil }),
				LogInvoke: func(_ context.Context, rec InvocationRecord) { records = append(records, rec) },
			}

			err := tc.call(o, revocableProposalReader{revoked: revoked})
			if !errors.Is(err, ErrProposalInputsChanged) {
				t.Fatalf("err = %v, want ErrProposalInputsChanged", err)
			}
			if errors.Is(err, providers.ErrResponseSchemaMismatch) {
				t.Fatalf("err = %v must not read as a provider schema mismatch", err)
			}
			if len(records) != 1 || records[0].ErrorCode != "AI.PROPOSAL.INPUTS_CHANGED" {
				t.Fatalf("invocation records = %+v, want one AI.PROPOSAL.INPUTS_CHANGED", records)
			}
		})
	}
}
