package signaljudge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
)

// fakeAgentLookup is the in-memory stand-in for AgentLookup. It
// returns a canned snapshot or error for every workspace/agent pair.
type fakeAgentLookup struct {
	snap AgentSnapshot
	err  error
}

func (f *fakeAgentLookup) Load(_ context.Context, _, _ uint32) (AgentSnapshot, error) {
	return f.snap, f.err
}

// fakeSignalLookup is the in-memory stand-in for SignalLookup.
type fakeSignalLookup struct {
	snap SignalSnapshot
	err  error
}

func (f *fakeSignalLookup) Load(_ context.Context, _ uint32, _ int64) (SignalSnapshot, error) {
	return f.snap, f.err
}

// fakeResolver returns a fixed provider on every call.
type fakeResolver struct {
	provider providers.Provider
	err      error
}

func (f *fakeResolver) Default(_ context.Context, _ uint32) (providers.Provider, error) {
	return f.provider, f.err
}

// recordingProvider captures the most recent request and returns a
// canned response or error. Implements [providers.Provider].
type recordingProvider struct {
	kind providers.Kind
	resp *providers.Response
	err  error
	req  providers.Request
}

func (p *recordingProvider) Name() string         { return "recording" }
func (p *recordingProvider) Kind() providers.Kind { return p.kind }

func (p *recordingProvider) Complete(_ context.Context, req providers.Request) (*providers.Response, error) {
	p.req = req
	if p.err != nil {
		return nil, p.err
	}
	return p.resp, nil
}

// fakeGuard is the in-memory CostGuard returning a fixed error.
type fakeGuard struct{ err error }

func (g *fakeGuard) Check(_ context.Context, _ uint32) error { return g.err }

// TestExecuteJudgeHappyPath asserts the runner loads the agent +
// signal, calls the provider, and emits one invocation log row with
// status=ok.
func TestExecuteJudgeHappyPath(t *testing.T) {
	t.Parallel()
	prov := &recordingProvider{
		kind: providers.Kind("mock"),
		// reasoning_excerpt is required by ValidateVerdict; without it the
		// runner's parse / validate retry loop fires and the test sees two
		// invocations. The verdict shape mirrors what production prompts
		// instruct the LLM to emit.
		resp: &providers.Response{Text: `{"action":"noop","confidence":0.1,"reasoning_excerpt":"nothing actionable in this signal"}`, CostCents: 12},
	}
	var logged []InvocationRecord
	r := &Runner{
		Agents: &fakeAgentLookup{snap: AgentSnapshot{
			AgentID:      7,
			WorkspaceID:  3,
			SystemPrompt: "", // empty falls back to skeleton
			ModelName:    "fake-model",
		}},
		Signals: &fakeSignalLookup{snap: SignalSnapshot{
			SignalID:    99,
			WorkspaceID: 3,
			Kind:        "manual",
			Source:      "manual",
			SubjectType: "user",
			PayloadJSON: json.RawMessage(`{"hello":"world"}`),
		}},
		Resolver: &fakeResolver{provider: prov},
		Log:      func(_ context.Context, rec InvocationRecord) { logged = append(logged, rec) },
	}

	result, err := r.ExecuteJudge(context.Background(), 3, 7, 99)
	if err != nil {
		t.Fatalf("ExecuteJudge: %v", err)
	}
	if result.CostCents != 12 {
		t.Fatalf("CostCents = %d, want 12", result.CostCents)
	}
	if result.LastThought == "" {
		t.Fatalf("LastThought empty, want LLM text")
	}
	if len(logged) != 1 || logged[0].Status != "ok" || logged[0].AgentID != 7 || logged[0].Purpose != "signal_judge" {
		t.Fatalf("invocation log = %+v, want one ok record for agent 7 purpose signal_judge", logged)
	}
	// System prompt must default to the Phase 2 skeleton when the
	// agent row carries an empty one.
	if prov.req.System != SystemPromptSkeleton() {
		t.Fatalf("provider system prompt did not default to skeleton; got %q", prov.req.System)
	}
}

// TestExecuteJudgeRedactsLastThought asserts that a secret-prefixed token
// echoed inside the raw LLM response is redacted before it lands in
// ExecutionResult.LastThought (persisted to agent_memo and exposed via the
// API lastThought field) and before it reaches the ai_invocations log.
func TestExecuteJudgeRedactsLastThought(t *testing.T) {
	t.Parallel()
	const secret = "sk-ant-LEAKEDKEY0123456789"
	prov := &recordingProvider{
		kind: providers.Kind("mock"),
		// Valid verdict JSON so the parse/validate retry loop does not fire;
		// the secret is embedded in reasoning_excerpt exactly as a
		// misbehaving model might echo it back.
		resp: &providers.Response{
			Text:      `{"action":"noop","confidence":0.1,"reasoning_excerpt":"echoing key ` + secret + ` verbatim"}`,
			CostCents: 3,
		},
	}
	var logged []InvocationRecord
	r := &Runner{
		Agents:   &fakeAgentLookup{snap: AgentSnapshot{AgentID: 5, WorkspaceID: 2}},
		Signals:  &fakeSignalLookup{snap: SignalSnapshot{SignalID: 1, WorkspaceID: 2, Kind: "manual"}},
		Resolver: &fakeResolver{provider: prov},
		Log:      func(_ context.Context, rec InvocationRecord) { logged = append(logged, rec) },
	}

	result, err := r.ExecuteJudge(context.Background(), 2, 5, 1)
	if err != nil {
		t.Fatalf("ExecuteJudge: %v", err)
	}
	if strings.Contains(result.LastThought, secret) {
		t.Fatalf("LastThought leaked the secret: %q", result.LastThought)
	}
	if !strings.Contains(result.LastThought, "[REDACTED:sk-ant-]") {
		t.Fatalf("LastThought was not redacted: %q", result.LastThought)
	}
	if len(logged) != 1 {
		t.Fatalf("want one invocation record, got %d", len(logged))
	}
	if strings.Contains(logged[0].ResponseRedacted, secret) {
		t.Fatalf("ai_invocations response leaked the secret: %q", logged[0].ResponseRedacted)
	}
}

// TestExecuteJudgeCostCap asserts the CostGuard's ErrDailyBudgetExceeded
// surfaces as CostCapHit and short-circuits before any provider call.
func TestExecuteJudgeCostCap(t *testing.T) {
	t.Parallel()
	prov := &recordingProvider{kind: providers.Kind("mock")}
	r := &Runner{
		Agents:   &fakeAgentLookup{snap: AgentSnapshot{AgentID: 1, WorkspaceID: 1}},
		Signals:  &fakeSignalLookup{snap: SignalSnapshot{SignalID: 1, WorkspaceID: 1}},
		Resolver: &fakeResolver{provider: prov},
		Guard:    &fakeGuard{err: errors.New("budget exceeded")},
	}
	result, err := r.ExecuteJudge(context.Background(), 1, 1, 1)
	if err == nil {
		t.Fatalf("ExecuteJudge should have returned err")
	}
	if !result.CostCapHit {
		t.Fatalf("CostCapHit = false, want true (cost guard error must set the flag)")
	}
	if prov.req.System != "" || prov.req.Prompt != "" {
		t.Fatalf("provider was called despite cost-guard failure; req=%+v", prov.req)
	}
}

// TestExecuteJudgePaused asserts a paused agent short-circuits with
// ErrAgentPaused and skips the provider call.
func TestExecuteJudgePaused(t *testing.T) {
	t.Parallel()
	prov := &recordingProvider{kind: providers.Kind("mock")}
	r := &Runner{
		Agents:   &fakeAgentLookup{snap: AgentSnapshot{AgentID: 1, WorkspaceID: 1, Paused: true}},
		Signals:  &fakeSignalLookup{snap: SignalSnapshot{SignalID: 1, WorkspaceID: 1}},
		Resolver: &fakeResolver{provider: prov},
	}
	_, err := r.ExecuteJudge(context.Background(), 1, 1, 1)
	if !errors.Is(err, ErrAgentPaused) {
		t.Fatalf("err = %v, want ErrAgentPaused", err)
	}
	if prov.req.System != "" {
		t.Fatalf("paused agent must not call provider; req=%+v", prov.req)
	}
}

// TestExecuteJudgeOverrideSystemPrompt asserts a non-empty
// ai_agents.system_prompt overrides the skeleton, so workspace admins
// can customise the judge without code changes.
func TestExecuteJudgeOverrideSystemPrompt(t *testing.T) {
	t.Parallel()
	prov := &recordingProvider{kind: providers.Kind("mock"), resp: &providers.Response{Text: "ok"}}
	custom := "You are the custom judge for this workspace."
	r := &Runner{
		Agents:   &fakeAgentLookup{snap: AgentSnapshot{AgentID: 1, WorkspaceID: 1, SystemPrompt: custom}},
		Signals:  &fakeSignalLookup{snap: SignalSnapshot{SignalID: 1, WorkspaceID: 1, Kind: "manual"}},
		Resolver: &fakeResolver{provider: prov},
	}
	if _, err := r.ExecuteJudge(context.Background(), 1, 1, 1); err != nil {
		t.Fatalf("ExecuteJudge: %v", err)
	}
	if prov.req.System != custom {
		t.Fatalf("system prompt = %q, want %q", prov.req.System, custom)
	}
}

// TestComposeJudgePromptShape pins the user-prompt JSON shape so the
// Phase 3 Applier verdict parser can rely on the same input contract
// without a prompt rewrite.
func TestComposeJudgePromptShape(t *testing.T) {
	t.Parallel()
	got, err := composeJudgePrompt(SignalSnapshot{
		SignalID:    9,
		WorkspaceID: 1,
		Kind:        "discord.presence",
		Source:      "discord",
		SubjectType: "user",
		PayloadJSON: json.RawMessage(`{"status":"online"}`),
	})
	if err != nil {
		t.Fatalf("composeJudgePrompt: %v", err)
	}
	var parsed struct {
		Signal struct {
			Kind        string         `json:"kind"`
			Source      string         `json:"source"`
			SubjectType string         `json:"subjectType"`
			Payload     map[string]any `json:"payload"`
		} `json:"signal"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("prompt JSON unmarshal: %v (got %q)", err, got)
	}
	if parsed.Signal.Kind != "discord.presence" || parsed.Signal.Source != "discord" {
		t.Fatalf("prompt missing core signal fields: %+v", parsed)
	}
	if parsed.Signal.Payload["status"] != "online" {
		t.Fatalf("payload not surfaced verbatim: %+v", parsed.Signal.Payload)
	}
}

func TestComposeJudgePromptRedactsLegacyPayload(t *testing.T) {
	t.Parallel()
	got, err := composeJudgePrompt(SignalSnapshot{
		SignalID:    9,
		WorkspaceID: 1,
		Kind:        "webhook.received",
		Source:      "webhook",
		SubjectType: "workspace",
		PayloadJSON: json.RawMessage(`{"authorization":"Bearer secret","nested":{"refresh_token":"also-secret"},"status":"ok"}`),
	})
	if err != nil {
		t.Fatalf("composeJudgePrompt: %v", err)
	}
	if strings.Contains(got, "secret") || strings.Contains(got, "also-secret") {
		t.Fatalf("legacy prompt leaked secret payload: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("legacy prompt did not include redaction marker: %s", got)
	}
}
