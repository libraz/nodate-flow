package signaljudge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

type protocolBudgetChecker struct {
	calls   int
	errorAt map[int]error
}

func (g *protocolBudgetChecker) Check(context.Context, uint32) error {
	g.calls++
	return g.errorAt[g.calls]
}

type protocolAgentLookup struct {
	snap       AgentSnapshot
	capCalls   int
	capErrorAt map[int]error
}

func (a *protocolAgentLookup) Load(context.Context, uint32, uint32) (AgentSnapshot, error) {
	return a.snap, nil
}

func (a *protocolAgentLookup) CheckCostCap(context.Context, uint32) error {
	a.capCalls++
	return a.capErrorAt[a.capCalls]
}

func protocolRunner(prov providers.Provider, agents *protocolAgentLookup, guard *protocolBudgetChecker, applier *Applier) *Runner {
	return &Runner{
		Agents:   agents,
		Signals:  &fakeSignalLookup{snap: SignalSnapshot{SignalID: 2, PublicID: "signal-public-id", WorkspaceID: 6, Kind: "manual"}},
		Resolver: &fakeResolver{provider: prov},
		Guard:    guard,
		Applier:  applier,
	}
}

func TestExecuteJudgeRejectsUnusableProviderOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		response      *providers.Response
		wantStatus    string
		wantErrorCode string
	}{
		{
			name: "truncated valid JSON",
			response: &providers.Response{
				Text:        validVerdict,
				Termination: providers.TerminationIncomplete,
				InputTokens: 101, OutputTokens: 17, CostMicros: 21_345,
			},
			wantStatus: "error", wantErrorCode: "AI.RESPONSE.GENERATION_INCOMPLETE",
		},
		{
			name: "refusal without text",
			response: &providers.Response{
				Termination: providers.TerminationRefused,
				InputTokens: 102, OutputTokens: 0, CostMicros: 22_345,
			},
			wantStatus: "blocked", wantErrorCode: "AI.RESPONSE.MODEL_REFUSED",
		},
		{
			name: "unknown stop with valid JSON",
			response: &providers.Response{
				Text:        validVerdict,
				Termination: providers.TerminationUnknown,
				InputTokens: 103, OutputTokens: 18, CostMicros: 23_345,
			},
			wantStatus: "error", wantErrorCode: "AI.RESPONSE.GENERATION_INCOMPLETE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			provider := &recordingProvider{kind: providers.Kind("mock"), resp: tt.response}
			agents := &protocolAgentLookup{snap: AgentSnapshot{AgentID: 4, WorkspaceID: 6}}
			guard := &protocolBudgetChecker{}
			appender := &recordingAppender{}
			runner := protocolRunner(provider, agents, guard, newTestApplier(appender, &recordingMutator{}))
			var records []InvocationRecord
			var metrics []metricSample
			runner.Log = func(_ context.Context, record InvocationRecord) { records = append(records, record) }
			runner.OnInvocation = func(_, model string, input, output int, cost int64, elapsed time.Duration, err error) {
				metrics = append(metrics, metricSample{model: model, inTok: input, outTok: output, cost: cost, elapsed: elapsed, err: err})
			}

			result, err := runner.ExecuteJudge(context.Background(), 6, 4, 2)
			if err != nil {
				t.Fatalf("ExecuteJudge: %v", err)
			}
			if len(provider.reqs) != 1 {
				t.Fatalf("unusable output made %d provider calls, want one", len(provider.reqs))
			}
			if provider.reqs[0].Output == nil || provider.reqs[0].Output.Contract() != "signal_verdict" {
				t.Errorf("request output contract = %v, want immutable signal_verdict schema", provider.reqs[0].Output)
			}
			if len(records) != 1 {
				t.Fatalf("got %d invocation rows, want one: %+v", len(records), records)
			}
			if records[0].Status != tt.wantStatus || records[0].ErrorCode != tt.wantErrorCode {
				t.Errorf("invocation outcome = (%q, %q), want (%q, %q)", records[0].Status, records[0].ErrorCode, tt.wantStatus, tt.wantErrorCode)
			}
			if records[0].TokensInput != tt.response.InputTokens || records[0].TokensOutput != tt.response.OutputTokens || records[0].CostMicros != tt.response.CostMicros {
				t.Errorf("rejected invocation lost provider usage/cost: %+v", records[0])
			}
			if len(metrics) != 1 || metrics[0].err == nil {
				t.Errorf("metrics = %+v, want one failed attempt", metrics)
			} else if metrics[0].inTok != tt.response.InputTokens || metrics[0].outTok != tt.response.OutputTokens || metrics[0].cost != tt.response.CostMicros {
				t.Errorf("rejected metric lost provider usage/cost: %+v", metrics[0])
			}
			if result.CostMicros != tt.response.CostMicros {
				t.Errorf("ExecutionResult.CostMicros = %d, want %d", result.CostMicros, tt.response.CostMicros)
			}
			if len(appender.events) != 1 || appender.events[0].Type != eventbus.SignalRejected {
				t.Errorf("events = %+v, want exactly one SignalRejected and no judged/application event", appender.events)
			} else {
				payload, ok := appender.events[0].Payload.(map[string]any)
				if !ok {
					t.Errorf("SignalRejected payload type = %T, want map[string]any", appender.events[0].Payload)
				} else if len(payload) != 4 ||
					payload["reason"] != rejectVerdictInvalid ||
					payload["validationError"] != tt.wantErrorCode ||
					payload["action"] != "" ||
					payload["confidence"] != 0.0 {
					t.Errorf("SignalRejected payload = %#v, want existing four-field rejection shape", payload)
				}
			}
		})
	}
}

func TestExecuteJudgeRepairsCompleteInvalidOutputAfterRecheckingBudget(t *testing.T) {
	t.Parallel()

	provider := &recordingProvider{
		kind: providers.Kind("mock"),
		script: []providerAnswer{
			{resp: &providers.Response{Text: "not json", Termination: providers.TerminationComplete, InputTokens: 200, OutputTokens: 4, CostMicros: 31_000}},
			{resp: &providers.Response{Text: validVerdict, Termination: providers.TerminationComplete, InputTokens: 210, OutputTokens: 24, CostMicros: 42_000}},
		},
	}
	agents := &protocolAgentLookup{snap: AgentSnapshot{AgentID: 4, WorkspaceID: 6}}
	guard := &protocolBudgetChecker{}
	appender := &recordingAppender{}
	runner := protocolRunner(provider, agents, guard, newTestApplier(appender, &recordingMutator{}))
	var records []InvocationRecord
	var metrics []metricSample
	runner.Log = func(_ context.Context, record InvocationRecord) { records = append(records, record) }
	runner.OnInvocation = func(_, model string, input, output int, cost int64, elapsed time.Duration, err error) {
		metrics = append(metrics, metricSample{model: model, inTok: input, outTok: output, cost: cost, elapsed: elapsed, err: err})
	}

	result, err := runner.ExecuteJudge(context.Background(), 6, 4, 2)
	if err != nil {
		t.Fatalf("ExecuteJudge: %v", err)
	}
	if len(provider.reqs) != 2 {
		t.Fatalf("complete invalid output made %d calls, want one repair", len(provider.reqs))
	}
	if provider.reqs[0].Output == nil || provider.reqs[1].Output == nil || provider.reqs[0].Output.Contract() != provider.reqs[1].Output.Contract() {
		t.Errorf("repair did not preserve the original output schema: first=%v second=%v", provider.reqs[0].Output, provider.reqs[1].Output)
	}
	if guard.calls != 2 || agents.capCalls != 2 {
		t.Errorf("workspace/agent budget checks = (%d, %d), want initial plus repair recheck", guard.calls, agents.capCalls)
	}
	if len(records) != 2 || records[0].Status != "error" || records[0].ErrorCode != "AI.RESPONSE.INVALID_JSON" || records[1].Status != "ok" {
		t.Errorf("attempt outcomes = %+v, want invalid JSON error then validated success", records)
	}
	if len(metrics) != 2 || metrics[0].err == nil || metrics[1].err != nil {
		t.Errorf("attempt metrics = %+v, want failed first validation and successful repair", metrics)
	}
	if result.CostMicros != 73_000 {
		t.Errorf("ExecutionResult.CostMicros = %d, want both attempts' 73000", result.CostMicros)
	}
	if len(appender.events) != 1 || appender.events[0].Type != eventbus.SignalJudged {
		t.Errorf("final valid verdict events = %+v, want only SignalJudged", appender.events)
	}
}

func TestExecuteJudgeRejectsInvalidOutputWhenRepairBudgetBlocks(t *testing.T) {
	t.Parallel()

	provider := &recordingProvider{
		kind: providers.Kind("mock"),
		resp: &providers.Response{Text: "not json", Termination: providers.TerminationComplete, InputTokens: 300, OutputTokens: 8, CostMicros: 51_234},
	}
	agents := &protocolAgentLookup{
		snap:       AgentSnapshot{AgentID: 4, WorkspaceID: 6},
		capErrorAt: map[int]error{2: ai.ErrAgentCostCapExceeded},
	}
	guard := &protocolBudgetChecker{}
	appender := &recordingAppender{}
	runner := protocolRunner(provider, agents, guard, newTestApplier(appender, &recordingMutator{}))
	var records []InvocationRecord
	runner.Log = func(_ context.Context, record InvocationRecord) { records = append(records, record) }

	result, err := runner.ExecuteJudge(context.Background(), 6, 4, 2)
	if !errors.Is(err, ai.ErrAgentCostCapExceeded) {
		t.Fatalf("ExecuteJudge error = %v, want agent cap error", err)
	}
	if !result.CostCapHit || result.CostMicros != 51_234 {
		t.Errorf("ExecutionResult = %+v, want cap hit and first attempt cost preserved", result)
	}
	if len(provider.reqs) != 1 {
		t.Errorf("repair budget cap still allowed %d provider calls", len(provider.reqs)-1)
	}
	if guard.calls != 2 || agents.capCalls != 2 {
		t.Errorf("workspace/agent budget checks = (%d, %d), want both rechecked before repair", guard.calls, agents.capCalls)
	}
	if len(records) != 1 || records[0].Status != "error" || records[0].CostMicros != 51_234 {
		t.Errorf("model attempt records = %+v, want one invalid-output error with actual cost", records)
	}
	if len(appender.events) != 1 || appender.events[0].Type != eventbus.SignalRejected {
		t.Errorf("events = %+v, want terminal SignalRejected before returning cap error", appender.events)
	}
}

func TestExecuteJudgePreservesInvocationContextThroughAccountingAdapter(t *testing.T) {
	t.Parallel()

	provider := &recordingProvider{
		kind: providers.Kind("mock"),
		resp: &providers.Response{Text: validVerdict, Termination: providers.TerminationComplete},
	}
	agents := &protocolAgentLookup{snap: AgentSnapshot{AgentID: 44, WorkspaceID: 6}}
	runner := protocolRunner(provider, agents, &protocolBudgetChecker{}, nil)
	resource := ai.TaskInputResource(types.New().String())
	ctx := ai.WithAgentID(context.Background(), 44)
	ctx = ai.WithTaskID(ctx, 88)
	ctx = ai.WithInvocationAudience(ctx, ai.InvocationAudienceTask)
	ctx = ai.WithInputResources(ctx, resource)
	var called bool
	runner.Log = func(logCtx context.Context, record InvocationRecord) {
		called = true
		if record.AgentID != 44 || ai.AgentIDFromContext(logCtx) != 44 {
			t.Errorf("agent attribution lost across accounting adapter: record=%d context=%d", record.AgentID, ai.AgentIDFromContext(logCtx))
		}
		if record.TaskID != 88 || ai.TaskIDFromContext(logCtx) != 88 {
			t.Errorf("task attribution lost across accounting adapter: record=%d context=%d", record.TaskID, ai.TaskIDFromContext(logCtx))
		}
		if ai.InvocationAudienceFromContext(logCtx) != ai.InvocationAudienceTask {
			t.Errorf("invocation audience lost across accounting adapter: %q", ai.InvocationAudienceFromContext(logCtx))
		}
		resources := ai.InputResourcesFromContext(logCtx)
		if len(resources) != 1 || resources[0] != resource {
			t.Errorf("input resource provenance lost across accounting adapter: %+v", resources)
		}
	}

	if _, err := runner.ExecuteJudge(ctx, 6, 44, 2); err != nil {
		t.Fatalf("ExecuteJudge: %v", err)
	}
	if !called {
		t.Fatal("valid invocation was not recorded")
	}
}

func TestVerdictOutputSchemaMatchesPromptSchema(t *testing.T) {
	t.Parallel()

	provider := &recordingProvider{
		kind: providers.Kind("mock"),
		resp: &providers.Response{Text: validVerdict, Termination: providers.TerminationComplete},
	}
	runner := protocolRunner(provider, &protocolAgentLookup{snap: AgentSnapshot{AgentID: 4, WorkspaceID: 6}}, &protocolBudgetChecker{}, nil)
	if _, err := runner.ExecuteJudge(context.Background(), 6, 4, 2); err != nil {
		t.Fatalf("ExecuteJudge: %v", err)
	}
	if len(provider.reqs) != 1 || provider.reqs[0].Output == nil {
		t.Fatal("signal_judge request did not carry an output schema")
	}
	var requestSchema, promptSchema any
	if err := json.Unmarshal(provider.reqs[0].Output.SchemaJSON(), &requestSchema); err != nil {
		t.Fatalf("request schema JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(verdictJSONSchema), &promptSchema); err != nil {
		t.Fatalf("prompt schema JSON: %v", err)
	}
	requestBytes, _ := json.Marshal(requestSchema)
	promptBytes, _ := json.Marshal(promptSchema)
	if string(requestBytes) != string(promptBytes) {
		t.Errorf("provider schema differs from existing prompt schema:\nrequest: %s\nprompt:  %s", requestBytes, promptBytes)
	}
}
