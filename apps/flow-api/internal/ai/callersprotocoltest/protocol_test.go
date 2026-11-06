package callersprotocoltest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

type fakeProvider struct {
	response *providers.Response
	err      error
	calls    int
	request  providers.Request
}

func (p *fakeProvider) Name() string         { return "protocol-test" }
func (p *fakeProvider) Kind() providers.Kind { return providers.KindOllama }
func (p *fakeProvider) Model() string        { return "test-model" }
func (p *fakeProvider) Complete(_ context.Context, req providers.Request) (*providers.Response, error) {
	p.calls++
	p.request = req
	return p.response, p.err
}

type metricSample struct {
	inputTokens, outputTokens int
	costMicros                int64
	err                       error
}

func orchestratorFor(p *fakeProvider, records *[]ai.InvocationRecord, metrics *[]metricSample) *ai.Orchestrator {
	return &ai.Orchestrator{
		Resolver: ai.ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return p, nil
		}),
		LogInvoke: func(_ context.Context, record ai.InvocationRecord) {
			*records = append(*records, record)
		},
		OnInvocation: func(_ string, _ string, inputTokens, outputTokens int, costMicros int64, _ time.Duration, err error) {
			*metrics = append(*metrics, metricSample{inputTokens: inputTokens, outputTokens: outputTokens, costMicros: costMicros, err: err})
		},
	}
}

func TestProposeTasksFromRejectsUnusableProviderOutputsAndKeepsUsage(t *testing.T) {
	const valid = `[{"title":"Prepare release","description":"Collect notes","priority":"high"}]`
	tests := []struct {
		name          string
		response      *providers.Response
		wantErr       error
		wantStatus    string
		wantErrorCode string
	}{
		{
			name:          "truncated valid-looking JSON",
			response:      &providers.Response{Text: valid, Termination: providers.TerminationIncomplete},
			wantErr:       providers.ErrGenerationIncomplete,
			wantStatus:    "error",
			wantErrorCode: "AI.RESPONSE.GENERATION_INCOMPLETE",
		},
		{
			name:          "refusal without text",
			response:      &providers.Response{Termination: providers.TerminationRefused},
			wantErr:       providers.ErrModelRefused,
			wantStatus:    "blocked",
			wantErrorCode: "AI.RESPONSE.MODEL_REFUSED",
		},
		{
			name:          "missing stop evidence",
			response:      &providers.Response{Text: valid},
			wantErr:       providers.ErrGenerationIncomplete,
			wantStatus:    "error",
			wantErrorCode: "AI.RESPONSE.GENERATION_INCOMPLETE",
		},
		{
			name:          "invalid schema",
			response:      &providers.Response{Text: `[{"title":9,"description":"d","priority":"high"}]`, Termination: providers.TerminationComplete},
			wantErr:       providers.ErrResponseSchemaMismatch,
			wantStatus:    "error",
			wantErrorCode: "AI.RESPONSE.SCHEMA_MISMATCH",
		},
		{
			name:          "invalid priority domain value",
			response:      &providers.Response{Text: `[{"title":"Prepare release","description":"Collect notes","priority":"urgent"}]`, Termination: providers.TerminationComplete},
			wantErr:       providers.ErrResponseSchemaMismatch,
			wantStatus:    "error",
			wantErrorCode: "AI.RESPONSE.SCHEMA_MISMATCH",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const wantInputTokens, wantOutputTokens, wantCost = 13, 8, int64(123456)
			tt.response.Model = "test-model"
			tt.response.InputTokens = wantInputTokens
			tt.response.OutputTokens = wantOutputTokens
			tt.response.CostMicros = wantCost
			provider := &fakeProvider{response: tt.response}
			var records []ai.InvocationRecord
			var metrics []metricSample
			o := orchestratorFor(provider, &records, &metrics)

			tasks, err := o.ProposeTasksFrom(context.Background(), 42, "prepare a release")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ProposeTasksFrom error = %v, want %v", err, tt.wantErr)
			}
			if tasks != nil {
				t.Fatalf("rejected output returned tasks: %+v", tasks)
			}
			if provider.calls != 1 {
				t.Fatalf("provider calls = %d, want exactly one", provider.calls)
			}
			if provider.request.Output == nil {
				t.Fatal("structured task request did not attach its output contract")
			}
			if provider.request.Output.Contract() != "tasks_from" {
				t.Errorf("output contract = %q, want tasks_from", provider.request.Output.Contract())
			}
			if len(records) != 1 {
				t.Fatalf("invocation records = %d, want one terminal outcome", len(records))
			}
			record := records[0]
			if record.Status != tt.wantStatus || record.ErrorCode != tt.wantErrorCode {
				t.Errorf("outcome = (%q, %q), want (%q, %q)", record.Status, record.ErrorCode, tt.wantStatus, tt.wantErrorCode)
			}
			if record.TokensInput != wantInputTokens || record.TokensOutput != wantOutputTokens || record.CostMicros != wantCost {
				t.Errorf("recorded usage = (%d tokens, %d tokens, %d micros), want (%d, %d, %d)", record.TokensInput, record.TokensOutput, record.CostMicros, wantInputTokens, wantOutputTokens, wantCost)
			}
			if len(metrics) != 1 || metrics[0].err == nil {
				t.Fatalf("metrics = %+v, want one failed outcome", metrics)
			}
			if metrics[0].inputTokens != wantInputTokens || metrics[0].outputTokens != wantOutputTokens || metrics[0].costMicros != wantCost {
				t.Errorf("metric usage = %+v, want response token counts and cost", metrics[0])
			}
			if tt.response.Text != "" && record.ResponseRedacted != tt.response.Text {
				t.Errorf("rejected response was not retained in redacted record: %q", record.ResponseRedacted)
			}
			if !canonicalASCII(record.ErrorCode) || len(record.ErrorCode) > 128 {
				t.Errorf("error code is not a bounded ASCII identifier: %q", record.ErrorCode)
			}
		})
	}
}

func TestProposeTasksFromReturnsAndRecordsCompleteValidatedOutput(t *testing.T) {
	provider := &fakeProvider{response: &providers.Response{
		Model:        "test-model",
		Text:         `[{"title":"Prepare release","description":"Collect notes","priority":"high"}]`,
		Termination:  providers.TerminationComplete,
		InputTokens:  13,
		OutputTokens: 8,
		CostMicros:   123456,
	}}
	var records []ai.InvocationRecord
	var metrics []metricSample
	o := orchestratorFor(provider, &records, &metrics)

	tasks, err := o.ProposeTasksFrom(context.Background(), 42, "prepare a release")
	if err != nil {
		t.Fatalf("ProposeTasksFrom: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Priority != "high" {
		t.Fatalf("returned tasks = %+v, want one complete task with high priority", tasks)
	}
	if provider.request.Output == nil || provider.calls != 1 {
		t.Fatalf("request output contract = %v, provider calls = %d", provider.request.Output, provider.calls)
	}
	if len(records) != 1 || records[0].Status != "ok" || records[0].CostMicros != 123456 {
		t.Fatalf("success record = %+v, want one successful record with actual cost", records)
	}
	if len(metrics) != 1 || metrics[0].err != nil {
		t.Fatalf("success metrics = %+v, want one successful outcome", metrics)
	}
}

func TestProposeTasksFromAcceptsUnboundedTaskCounts(t *testing.T) {
	manyTasks := make([]ai.ProposedTask, 21)
	for i := range manyTasks {
		manyTasks[i] = ai.ProposedTask{Title: "Task", Description: "Details", Priority: "medium"}
	}
	manyTasksJSON, err := json.Marshal(manyTasks)
	if err != nil {
		t.Fatalf("marshal many valid tasks: %v", err)
	}

	tests := []struct {
		name      string
		text      string
		wantCount int
	}{
		{name: "no actionable tasks", text: `[]`, wantCount: 0},
		{name: "more than twenty valid tasks", text: string(manyTasksJSON), wantCount: 21},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const wantInputTokens, wantOutputTokens, wantCost = 23, 31, int64(456789)
			provider := &fakeProvider{response: &providers.Response{
				Model:        "test-model",
				Text:         tt.text,
				Termination:  providers.TerminationComplete,
				InputTokens:  wantInputTokens,
				OutputTokens: wantOutputTokens,
				CostMicros:   wantCost,
			}}
			var records []ai.InvocationRecord
			var metrics []metricSample
			o := orchestratorFor(provider, &records, &metrics)

			tasks, err := o.ProposeTasksFrom(context.Background(), 42, "extract actionable work")
			if err != nil {
				t.Fatalf("ProposeTasksFrom: %v", err)
			}
			if len(tasks) != tt.wantCount {
				t.Fatalf("returned %d tasks, want %d", len(tasks), tt.wantCount)
			}
			if provider.request.Output == nil || provider.request.Output.Contract() != "tasks_from" {
				t.Fatalf("request output contract = %v, want tasks_from", provider.request.Output)
			}
			if provider.calls != 1 || len(records) != 1 || records[0].Status != "ok" {
				t.Fatalf("calls=%d records=%+v, want one successful outcome", provider.calls, records)
			}
			if records[0].TokensInput != wantInputTokens || records[0].TokensOutput != wantOutputTokens || records[0].CostMicros != wantCost {
				t.Errorf("invocation usage = (%d, %d, %d), want (%d, %d, %d)", records[0].TokensInput, records[0].TokensOutput, records[0].CostMicros, wantInputTokens, wantOutputTokens, wantCost)
			}
			if len(metrics) != 1 || metrics[0].err != nil || metrics[0].inputTokens != wantInputTokens || metrics[0].outputTokens != wantOutputTokens || metrics[0].costMicros != wantCost {
				t.Errorf("metrics = %+v, want one successful sample with actual usage", metrics)
			}
		})
	}
}

func TestProposePriorityRejectsUnknownPriority(t *testing.T) {
	provider := &fakeProvider{response: &providers.Response{
		Model:        "test-model",
		Text:         `{"priority":"urgent","reason":"the model invented a value"}`,
		Termination:  providers.TerminationComplete,
		InputTokens:  4,
		OutputTokens: 5,
		CostMicros:   654321,
	}}
	var records []ai.InvocationRecord
	var metrics []metricSample
	o := orchestratorFor(provider, &records, &metrics)

	priority, err := o.ProposePriority(context.Background(), 42, "ship the thing")
	if !errors.Is(err, providers.ErrResponseSchemaMismatch) {
		t.Fatalf("ProposePriority error = %v, want schema mismatch", err)
	}
	if priority != "" {
		t.Fatalf("returned invalid priority %q", priority)
	}
	if provider.request.Output == nil {
		t.Fatal("priority request did not attach its output contract")
	}
	if len(records) != 1 || records[0].Status != "error" || records[0].CostMicros != 654321 {
		t.Fatalf("invalid priority outcome = %+v, want one error with actual cost", records)
	}
	if len(metrics) != 1 || metrics[0].err == nil || metrics[0].costMicros != 654321 {
		t.Fatalf("invalid priority metrics = %+v, want one failed outcome with actual cost", metrics)
	}
}

type fakeEmbedClient struct{}

func (fakeEmbedClient) EmbedQuery(context.Context, uint32, string) ([]float32, error) {
	return []float32{1}, nil
}
func (fakeEmbedClient) Model() string { return "test-embedding-model" }

type fakeSmartCreateReader struct {
	members []generated.ListWorkspaceMembersForSmartCreateRow
}

func (r fakeSmartCreateReader) ListCandidateTaskEmbeddings(context.Context, generated.ListCandidateTaskEmbeddingsParams) ([]generated.ListCandidateTaskEmbeddingsRow, error) {
	return nil, nil
}
func (r fakeSmartCreateReader) ListTasksWithAssigneesForSmartCreate(context.Context, generated.ListTasksWithAssigneesForSmartCreateParams) ([]generated.ListTasksWithAssigneesForSmartCreateRow, error) {
	return nil, nil
}
func (r fakeSmartCreateReader) ListWorkspaceMembersForSmartCreate(context.Context, uint32) ([]generated.ListWorkspaceMembersForSmartCreateRow, error) {
	return r.members, nil
}

func (fakeSmartCreateReader) GetProposalCacheWorkspaceAccess(context.Context, generated.GetProposalCacheWorkspaceAccessParams) (interface{}, error) {
	return "owner", nil
}

func (fakeSmartCreateReader) ListProposalCacheTaskInputs(context.Context, generated.ListProposalCacheTaskInputsParams) ([]generated.ListProposalCacheTaskInputsRow, error) {
	return nil, nil
}

func TestProposeSmartCreateRejectsAssigneeOutsideWorkspace(t *testing.T) {
	memberID := types.New()
	unknownID := types.New()
	provider := &fakeProvider{response: &providers.Response{
		Model: "test-model",
		Text: `{"suggestedAssignees":[{"userPublicId":"` + unknownID.String() + `","displayName":"Not a member","confidence":0.9,"reason":"historical match"}],` +
			`"subtasks":[{"title":"Prepare release","description":"Collect notes","priority":"high"},` +
			`{"title":"Review release","description":"Check the final notes","priority":"medium"}]}`,
		Termination:  providers.TerminationComplete,
		InputTokens:  17,
		OutputTokens: 12,
		CostMicros:   456789,
	}}
	reader := fakeSmartCreateReader{members: []generated.ListWorkspaceMembersForSmartCreateRow{{
		UserPublicID: memberID,
		DisplayName:  "Current member",
	}}}
	var records []ai.InvocationRecord
	var metrics []metricSample
	o := orchestratorFor(provider, &records, &metrics)

	proposal, err := o.ProposeSmartCreate(context.Background(), 42, "prepare a release", "", fakeEmbedClient{}, reader, acl.VisibilityArgs{})
	if !errors.Is(err, providers.ErrResponseSchemaMismatch) {
		t.Fatalf("ProposeSmartCreate error = %v, want schema mismatch", err)
	}
	if proposal != nil {
		t.Fatalf("returned proposal with an unoffered assignee: %+v", proposal)
	}
	if provider.request.Output == nil || provider.request.Output.Contract() != "smart_create" {
		t.Fatalf("smart-create output contract = %v", provider.request.Output)
	}
	if len(records) != 1 || records[0].Status != "error" || records[0].CostMicros != 456789 {
		t.Fatalf("unknown-assignee outcome = %+v, want one cost-bearing error", records)
	}
	if len(metrics) != 1 || metrics[0].err == nil || metrics[0].costMicros != 456789 {
		t.Fatalf("unknown-assignee metrics = %+v, want one failed outcome with actual cost", metrics)
	}
}

func TestGeneratePageBodyRequiresCompleteNonemptyText(t *testing.T) {
	provider := &fakeProvider{response: &providers.Response{
		Model:        "test-model",
		Text:         "partial draft",
		Termination:  providers.TerminationIncomplete,
		InputTokens:  9,
		OutputTokens: 11,
		CostMicros:   99001,
	}}
	var records []ai.InvocationRecord
	var metrics []metricSample
	o := orchestratorFor(provider, &records, &metrics)

	body, err := o.GeneratePageBody(context.Background(), 42, "Release notes", "Draft release notes")
	if !errors.Is(err, providers.ErrGenerationIncomplete) {
		t.Fatalf("GeneratePageBody error = %v, want incomplete generation", err)
	}
	if body != "" {
		t.Fatalf("returned incomplete page body %q", body)
	}
	if provider.request.Output != nil {
		t.Fatalf("plain text page request unexpectedly attached schema %q", provider.request.Output.Contract())
	}
	if len(records) != 1 || records[0].Status != "error" || records[0].CostMicros != 99001 {
		t.Fatalf("incomplete page outcome = %+v, want one cost-bearing error", records)
	}
}

func canonicalASCII(code string) bool {
	if code == "" {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < 0x20 || code[i] > 0x7e {
			return false
		}
	}
	return true
}
