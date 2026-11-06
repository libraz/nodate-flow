package nlquery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

type outputContractProvider struct {
	response *providers.Response
	calls    int
	request  providers.Request
	callCtx  context.Context
}

func (p *outputContractProvider) Name() string         { return "output-contract-test" }
func (p *outputContractProvider) Kind() providers.Kind { return providers.KindOpenAICompat }
func (p *outputContractProvider) Model() string        { return "test-model" }
func (p *outputContractProvider) Complete(ctx context.Context, req providers.Request) (*providers.Response, error) {
	p.calls++
	p.request = req
	p.callCtx = ctx
	return p.response, nil
}

func TestWorkspaceProviderOutputContract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		response   *providers.Response
		wantErr    error
		wantNLCode bool
		wantStatus string
	}{
		{
			name:       "complete valid Lens",
			response:   &providers.Response{Text: goodLens, Termination: providers.TerminationComplete, InputTokens: 31, OutputTokens: 12, CostMicros: 123456},
			wantStatus: "ok",
		},
		{
			name:       "incomplete valid-looking JSON",
			response:   &providers.Response{Text: goodLens, Termination: providers.TerminationIncomplete, StopReason: "length", InputTokens: 31, OutputTokens: 12, CostMicros: 123456},
			wantErr:    providers.ErrGenerationIncomplete,
			wantStatus: "error",
		},
		{
			name:       "refusal with text",
			response:   &providers.Response{Text: "I cannot help with that request", Termination: providers.TerminationRefused, InputTokens: 31, OutputTokens: 12, CostMicros: 123456},
			wantErr:    providers.ErrModelRefused,
			wantStatus: "blocked",
		},
		{
			name:       "refusal without text",
			response:   &providers.Response{Termination: providers.TerminationRefused, InputTokens: 31, OutputTokens: 12, CostMicros: 123456},
			wantErr:    providers.ErrModelRefused,
			wantStatus: "blocked",
		},
		{
			name:       "unknown stop reason",
			response:   &providers.Response{Text: goodLens, Termination: providers.TerminationUnknown, StopReason: "future_reason", InputTokens: 31, OutputTokens: 12, CostMicros: 123456},
			wantErr:    providers.ErrGenerationIncomplete,
			wantStatus: "error",
		},
		{
			name:       "missing stop evidence",
			response:   &providers.Response{Text: goodLens, InputTokens: 31, OutputTokens: 12, CostMicros: 123456},
			wantErr:    providers.ErrGenerationIncomplete,
			wantStatus: "error",
		},
		{
			name:       "complete but empty output",
			response:   &providers.Response{Text: " \n", Termination: providers.TerminationComplete, InputTokens: 31, OutputTokens: 12, CostMicros: 123456},
			wantErr:    providers.ErrResponseSchemaMismatch,
			wantStatus: "error",
		},
		{
			name:       "schema shape rejected",
			response:   &providers.Response{Text: `{"filter":"blocked"}`, Termination: providers.TerminationComplete, InputTokens: 31, OutputTokens: 12, CostMicros: 123456},
			wantErr:    providers.ErrResponseSchemaMismatch,
			wantNLCode: true,
			wantStatus: "error",
		},
		{
			name:       "Lens domain rejected",
			response:   &providers.Response{Text: `{"filter":{"not_a_lens_field":{"eq":true}}}`, Termination: providers.TerminationComplete, InputTokens: 31, OutputTokens: 12, CostMicros: 123456},
			wantErr:    providers.ErrResponseSchemaMismatch,
			wantNLCode: true,
			wantStatus: "error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			provider := &outputContractProvider{response: tc.response}
			var records []InvocationRecord
			var metricCalls int
			var metricErr error
			var metricInput, metricOutput int
			var metricCost int64
			wp := NewWorkspaceProvider(&meteringResolver{provider: provider}, extractWSFixed(7)).WithMetering(
				fixedGuard{},
				func(_ context.Context, record InvocationRecord) { records = append(records, record) },
				func(_ string, _ string, inputTokens, outputTokens int, costMicros int64, _ time.Duration, err error) {
					metricCalls++
					metricInput, metricOutput, metricCost, metricErr = inputTokens, outputTokens, costMicros, err
				},
			)
			ctx := ai.WithAgentID(context.Background(), 23)
			ctx = ai.WithTaskID(ctx, 41)
			ctx = ai.WithInvocationAudience(ctx, ai.InvocationAudienceTask)
			ctx = ai.WithInputResources(ctx, ai.TaskInputResource("01234567-89ab-cdef-0123-456789abcdef"))

			out, err := wp.CompileLens(ctx, "filter blocked tasks")
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("CompileLens: %v", err)
				}
				if string(out) != goodLens {
					t.Fatalf("output = %q, want validated Lens bytes", out)
				}
			} else {
				if err == nil {
					t.Fatal("expected unusable response to fail")
				}
				if len(out) != 0 {
					t.Fatalf("unusable response returned bytes %q", out)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("error = %v, want errors.Is(_, %v)", err, tc.wantErr)
				}
				if tc.wantNLCode && !errors.Is(err, ErrUnparseable) {
					t.Errorf("domain/schema error %v does not retain ErrUnparseable", err)
				}
			}

			if provider.calls != 1 {
				t.Fatalf("provider calls = %d, want exactly one", provider.calls)
			}
			if provider.request.Output == nil || provider.request.Output.Contract() == "" || len(provider.request.Output.SchemaJSON()) == 0 {
				t.Fatal("provider request did not carry a static output contract")
			}
			if provider.callCtx == nil || ai.AgentIDFromContext(provider.callCtx) != 23 || ai.TaskIDFromContext(provider.callCtx) != 41 {
				t.Fatal("provider call lost agent/task context metadata")
			}
			if len(records) != 1 {
				t.Fatalf("invocation records = %d, want exactly one", len(records))
			}
			record := records[0]
			if record.Status != tc.wantStatus {
				t.Errorf("record status = %q, want %q", record.Status, tc.wantStatus)
			}
			if record.TokensInput != 31 || record.TokensOutput != 12 || record.CostMicros != 123456 {
				t.Errorf("record usage/cost = (%d,%d,%d), want (31,12,123456)", record.TokensInput, record.TokensOutput, record.CostMicros)
			}
			if record.AgentID != 23 || record.TaskID != 41 || record.Audience != ai.InvocationAudienceTask || len(record.InputResources) != 1 {
				t.Errorf("invocation provenance not preserved: %+v", record)
			}
			if record.Purpose != "compile_lens" {
				t.Errorf("record purpose = %q, want compile_lens", record.Purpose)
			}
			if metricCalls != 1 {
				t.Errorf("metric calls = %d, want exactly one", metricCalls)
			}
			if metricInput != 31 || metricOutput != 12 || metricCost != 123456 {
				t.Errorf("metric usage/cost = (%d,%d,%d), want (31,12,123456)", metricInput, metricOutput, metricCost)
			}
			if !errors.Is(metricErr, tc.wantErr) {
				t.Errorf("metric error = %v, want %v", metricErr, tc.wantErr)
			}
		})
	}
}
