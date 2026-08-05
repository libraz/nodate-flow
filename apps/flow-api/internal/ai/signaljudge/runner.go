package signaljudge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// ProviderResolver is the narrow contract the runner needs to obtain a
// workspace-default LLM provider client. It mirrors the resolver
// signature used by [ai.AgentExecutor] so production wiring can pass
// the same [providers.WorkspaceResolver] instance to both paths.
type ProviderResolver interface {
	Default(ctx context.Context, workspaceID uint32) (providers.Provider, error)
}

// CostGuard is the narrow contract the runner needs to participate in
// the per-workspace daily cap. It mirrors [ai.CostGuard.Check] so the
// task-agent and signal-judge paths share one budget — there is one
// budget per workspace, not one per agent kind (ADR 0008 D3).
type CostGuard interface {
	Check(ctx context.Context, workspaceID uint32) error
}

// InvocationLogger persists a redacted record of the LLM call. The
// signature matches [ai.InvocationLogger] verbatim so production
// wiring can pass the same logger to both the task-agent and judge
// paths and ai_invocations stays a single, uniform audit surface.
//
// The logger receives the redacted prompt and response together with
// the provider / model identifiers and the workspace id. agentID is
// set to the signal_judge agent so judge runs are filterable in the
// invocation log UI.
type InvocationLogger func(ctx context.Context, rec InvocationRecord)

// InvocationRecord is the redacted ai_invocations payload the judge
// emits. Field shape mirrors [ai.InvocationRecord] so callers can
// pass a shared logger without translating between two structs.
type InvocationRecord struct {
	WorkspaceID      uint32
	AgentID          uint32
	Purpose          string
	Model            string
	PromptRedacted   string
	ResponseRedacted string
	TokensInput      int
	TokensOutput     int
	CostMicros       int64
	CostCents        int64
	Status           string
	ErrorCode        string
}

// InvocationMetricsHook is called after each LLM provider call. Same
// shape as [ai.InvocationMetricsHook] so the obs package's
// RecordAIInvocation can be reused without an adapter.
type InvocationMetricsHook func(provider, model, workspaceID string, costMicros int64)

// AgentLookup is the narrow surface the runner needs to read an
// agent's system prompt + kind by internal id. The production wiring
// supplies a function that does a single SELECT against ai_agents
// (and the runner already has *sql.DB in hand, so the production
// adapter is a one-liner).
type AgentLookup interface {
	Load(ctx context.Context, workspaceID, agentID uint32) (AgentSnapshot, error)
}

// AgentSnapshot is the minimal projection of an ai_agents row the
// runner needs to compose a judge invocation. system_prompt is taken
// from the row when non-empty so workspace admins can override the
// Phase 2 SystemPromptSkeleton without code edits; an empty value
// falls back to the skeleton.
type AgentSnapshot struct {
	WorkspaceID  uint32
	AgentID      uint32
	Paused       bool
	SystemPrompt string
	ModelName    string
}

// SignalLookup is the narrow surface the runner needs to load the
// signal payload by internal id. The production adapter does a single
// SELECT against signals and returns the workspace id, kind, and
// payload_json. The runner uses these to build the judge prompt; the
// Applier (Phase 3 / J4) will read the verdict back from agent_runs.
type SignalLookup interface {
	Load(ctx context.Context, workspaceID uint32, signalID int64) (SignalSnapshot, error)
}

// SignalSnapshot is the minimal projection of a signals row the
// runner passes to the judge prompt. payload_json is the raw,
// provider-shaped blob; the judge sees it verbatim so it has the same
// view as the constraint engine.
type SignalSnapshot struct {
	SignalID int64
	// PublicID is signals.public_id rendered as the canonical UUID
	// string. The Applier embeds it in payload strings (retro task
	// title, debug logs) so the timeline can render the lineage
	// without an extra join. Empty when the lookup adapter did not
	// populate it (e.g. early Phase 2 deployments before the column
	// projection was widened).
	PublicID     string
	WorkspaceID  uint32
	Kind         string
	Source       string
	SubjectType  string
	SubjectID    sql.NullInt32
	PayloadJSON  json.RawMessage
	ReceivedAtMs int64
}

// Runner executes one signal_judge agent tick. It is the production
// adapter behind the OrchestratorRunner's dispatch branch for the
// signal_judge kind. The runner does not write events — only the
// Applier (Phase 3 / J4) does — and it does not write to signals
// either; for now the verdict is logged via the InvocationLogger so
// every judge run produces an audit-visible ai_invocations row.
//
// As of Phase 3 / J4, the runner parses the LLM response into a
// [Verdict] and forwards it to [Applier.Apply] when one is wired. The
// runner itself still never writes events — every event the verdict
// produces is appended by the Applier via [eventbus.AppendJudgeEvent],
// which is the sole legitimate path for the judge-only kinds.
type Runner struct {
	Agents       AgentLookup
	Signals      SignalLookup
	Resolver     ProviderResolver
	Guard        CostGuard
	Log          InvocationLogger
	OnInvocation InvocationMetricsHook
	// Applier reifies the parsed verdict into events. nil disables
	// Applier dispatch — the runner then just logs the invocation
	// and returns; useful for smoke deployments that have not opted
	// into the judge loop yet.
	Applier *Applier
	// RunIDFromContext extracts the agent_runs.id of the currently
	// dispatching judge run. The OrchestratorRunner is the producer
	// of that id and tags the context before calling ExecuteJudge;
	// the Applier records it in signals.judge_run_id so the
	// timeline can link the verdict back to the agent run row. A
	// zero return is acceptable — the Applier writes NULL in that
	// case rather than rejecting the verdict.
	RunIDFromContext func(ctx context.Context) uint32
	// Prompt configures the per-run context window the runner
	// renders alongside the system prompt (Phase 6 / L1). Every
	// field on [PromptDeps] is optional; an empty value collapses
	// the relevant context section so the runner can be wired
	// progressively. When the entire struct is zero-valued the
	// runner falls back to the Phase 2 [composeJudgePrompt] shape
	// (a JSON-serialised signal snapshot only) so legacy callers
	// that have not yet wired the lookups keep working.
	Prompt PromptDeps
}

// ErrJudgeNotConfigured indicates the runner is missing one of the
// mandatory dependencies (agent lookup, signal lookup, or provider
// resolver). It is returned without ever touching the LLM so a
// half-wired deployment fails fast and visibly.
var ErrJudgeNotConfigured = errors.New("signaljudge: runner not configured")

// ErrAgentPaused mirrors [ai.ErrAgentPaused] so the orchestrator
// runner's failure-event payload renders the same code regardless of
// whether the paused agent was a task agent or a signal judge.
var ErrAgentPaused = errors.New("signaljudge: agent is paused")

// ErrSignalMissing is returned when the signal id parsed out of
// agent_runs.dedupe_key does not point at a live signals row. This
// happens when a signal is retention-swept before its judge run
// claims its queue row. The orchestrator treats it as a non-retryable
// failure.
var ErrSignalMissing = errors.New("signaljudge: signal not found")

// ExecuteJudge runs one judge tick for the given (agent, signal)
// pair. It loads the agent + signal snapshots, enforces the cost
// guard, resolves the workspace's default LLM provider, builds the
// minimal Phase 2 prompt, invokes the provider, logs the redacted
// invocation, and returns the ExecutionResult the OrchestratorRunner
// uses for memo bookkeeping / handoff decisions.
//
// The function does not write events; the Applier (Phase 3 / J4)
// owns event emission. The function does not write to signals
// either; persisting judge_output_json is also Applier work.
func (r *Runner) ExecuteJudge(ctx context.Context, workspaceID, agentID uint32, signalID int64) (agentruntime.ExecutionResult, error) {
	var result agentruntime.ExecutionResult
	if r == nil || r.Agents == nil || r.Signals == nil || r.Resolver == nil {
		return result, ErrJudgeNotConfigured
	}

	agent, err := r.Agents.Load(ctx, workspaceID, agentID)
	if err != nil {
		return result, fmt.Errorf("signaljudge: agent load: %w", err)
	}
	if agent.Paused {
		return result, ErrAgentPaused
	}

	if r.Guard != nil {
		if err := r.Guard.Check(ctx, workspaceID); err != nil {
			// CostCapHit lets the OrchestratorRunner's existing cost_cap
			// handoff trigger fire for judge runs too, with no new
			// branching in agentruntime itself.
			result.CostCapHit = true
			return result, err
		}
	}

	signal, err := r.Signals.Load(ctx, workspaceID, signalID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, ErrSignalMissing
		}
		return result, fmt.Errorf("signaljudge: signal load: %w", err)
	}

	prov, err := r.Resolver.Default(ctx, workspaceID)
	if err != nil {
		return result, err
	}
	if prov == nil {
		return result, ErrJudgeNotConfigured
	}

	systemPrompt := agent.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = SystemPrompt()
	}
	userPrompt, perr := r.renderUserPrompt(ctx, signal)
	if perr != nil {
		return result, fmt.Errorf("signaljudge: compose prompt: %w", perr)
	}

	ctx = providers.WithWorkspaceID(ctx, workspaceID)
	req := providers.Request{
		System: systemPrompt,
		Prompt: userPrompt,
	}
	wsIDStr := strconv.FormatUint(uint64(workspaceID), 10)
	// Pre-compose the combined prompt for redaction so both branches
	// (success / failure) below see the same shape and any registered
	// SecretPrefixes are scrubbed before the ai_invocations write.
	combinedPrompt := logutil.Redact(strings.TrimSpace(req.System + "\n" + req.Prompt))
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		if r.OnInvocation != nil {
			r.OnInvocation(string(prov.Kind()), req.Model, wsIDStr, 0)
		}
		r.logInvocation(ctx, InvocationRecord{
			WorkspaceID:    workspaceID,
			AgentID:        agentID,
			Purpose:        "signal_judge",
			Model:          req.Model,
			PromptRedacted: combinedPrompt,
			Status:         "error",
			ErrorCode:      logutil.Redact(err.Error()),
		})
		return result, fmt.Errorf("signaljudge: provider call: %w", err)
	}

	if r.OnInvocation != nil {
		r.OnInvocation(string(prov.Kind()), req.Model, wsIDStr, resp.EstimatedCostMicros())
	}
	model := resp.Model
	if model == "" {
		model = req.Model
	}
	r.logInvocation(ctx, InvocationRecord{
		WorkspaceID:      workspaceID,
		AgentID:          agentID,
		Purpose:          "signal_judge",
		Model:            model,
		PromptRedacted:   combinedPrompt,
		ResponseRedacted: logutil.Redact(resp.Text),
		TokensInput:      resp.InputTokens,
		TokensOutput:     resp.OutputTokens,
		CostMicros:       resp.EstimatedCostMicros(),
		CostCents:        resp.EstimatedCostCents(),
		Status:           "ok",
	})
	result.CostMicros = resp.EstimatedCostMicros()
	result.CostCents = resp.EstimatedCostCents()
	// LastThought is persisted to agent_memo and exposed via the API; the
	// raw parse text (verdictText) stays unredacted below since it must
	// round-trip through the JSON verdict parser.
	result.LastThought = logutil.Redact(resp.Text)

	// If the response does not parse into a valid Verdict, retry
	// once with the same prompt plus a stern reminder appended.
	// Both attempts count against the workspace's per-signal cost
	// guard budget; the runner does not loop more than once because
	// a persistent parse failure usually means the model is
	// fundamentally misconfigured (wrong system prompt override,
	// non-JSON-capable model) — additional retries just burn budget.
	verdictText := resp.Text
	if r.shouldRetry(verdictText) {
		retryReq := providers.Request{
			System: systemPrompt,
			Prompt: userPrompt + "\n\n" + retryReminder,
			Model:  req.Model,
		}
		retryResp, retryErr := prov.Complete(ctx, retryReq)
		if retryErr == nil && retryResp != nil {
			if r.OnInvocation != nil {
				r.OnInvocation(string(prov.Kind()), retryReq.Model, wsIDStr, retryResp.EstimatedCostMicros())
			}
			retryModel := retryResp.Model
			if retryModel == "" {
				retryModel = retryReq.Model
			}
			r.logInvocation(ctx, InvocationRecord{
				WorkspaceID:      workspaceID,
				AgentID:          agentID,
				Purpose:          "signal_judge",
				Model:            retryModel,
				PromptRedacted:   logutil.Redact(strings.TrimSpace(retryReq.System + "\n" + retryReq.Prompt)),
				ResponseRedacted: logutil.Redact(retryResp.Text),
				TokensInput:      retryResp.InputTokens,
				TokensOutput:     retryResp.OutputTokens,
				CostMicros:       retryResp.EstimatedCostMicros(),
				CostCents:        retryResp.EstimatedCostCents(),
				Status:           "ok",
			})
			result.CostMicros += retryResp.EstimatedCostMicros()
			result.CostCents = result.CostMicros / 10_000
			result.LastThought = logutil.Redact(retryResp.Text)
			verdictText = retryResp.Text
		}
		// A retry that also errors is treated like an unparseable
		// response: the synthesised noop verdict below routes
		// through the Applier's SignalRejected path.
	}

	// Phase 3 / J4 — once an Applier is wired, translate the LLM
	// response into a [Verdict] and let the Applier reify it. The
	// runner stays write-free for events/signals; everything from
	// here on is the Applier's responsibility.
	if r.Applier != nil {
		ref := SignalRef{
			InternalID:  signal.SignalID,
			PublicID:    signal.PublicID,
			WorkspaceID: signal.WorkspaceID,
			Kind:        signal.Kind,
		}
		agentRef := AgentRef{InternalID: agentID}
		runID := uint32(0)
		if r.RunIDFromContext != nil {
			runID = r.RunIDFromContext(ctx)
		}
		applyRes, applyErr := r.applyVerdict(ctx, ref, agentRef, runID, verdictText)
		if applyErr != nil {
			// Apply failure is a server bug, not an LLM bug — return
			// it so the OrchestratorRunner records the run as failed.
			return result, fmt.Errorf("signaljudge: applier: %w", applyErr)
		}
		if applyRes != nil {
			result.LastThought = applyRes.SkipReason
			if applyRes.SkipReason == "" {
				result.LastThought = fmt.Sprintf("applied %d events", applyRes.EventsAppended)
			}
		}
	}
	return result, nil
}

// retryReminder is the user-message suffix the runner appends on the
// second attempt when the first response did not parse. Kept
// intentionally short and procedural — the model must understand the
// constraint without spending tokens on a polite re-explanation.
const retryReminder = "Your previous response was not valid JSON matching the schema. Please respond with valid JSON only, no prose, no markdown fences."

// shouldRetry reports whether the LLM's first response is malformed
// enough to justify a single retry. The decision is intentionally
// cheap: parse the trimmed text as a [Verdict] and run
// [ValidateVerdict]; if either step fails, retry. Cost-guard budget
// is checked once per ExecuteJudge call (not per attempt) so an
// already-charged retry is free of further accounting; the upstream
// provider cost is what we are spending.
func (r *Runner) shouldRetry(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	var v Verdict
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return true
	}
	return ValidateVerdict(v) != nil
}

// renderUserPrompt builds the user-message body. When the [Runner]
// has been configured with [PromptDeps] (any non-nil lookup), it
// invokes [BuildPromptContext] + [RenderUserPrompt] for the full
// Phase 6 context window. Otherwise it falls back to the legacy
// Phase 2 [composeJudgePrompt] shape — a JSON-serialised signal
// snapshot only — so callers that have not yet wired the lookups
// keep working unchanged.
func (r *Runner) renderUserPrompt(ctx context.Context, sig SignalSnapshot) (string, error) {
	if r.Prompt.RecentTasks == nil && r.Prompt.LinkedTasks == nil && r.Prompt.JudgeInstructions == nil && r.Prompt.WorkspaceNow == nil {
		return composeJudgePrompt(sig)
	}
	pc, err := BuildPromptContext(ctx, r.Prompt, sig)
	if err != nil {
		return "", err
	}
	return RenderUserPrompt(pc), nil
}

// applyVerdict parses the LLM response into a [Verdict] and forwards
// it to the Applier. A parse error is itself a verdict-shape failure
// and routes through the Applier so SignalRejected is emitted with a
// "parse_error" reason — the timeline must surface "the LLM said
// something we could not consume" just like it surfaces a schema
// violation.
//
// The runner's 1-retry loop in ExecuteJudge already gave the LLM a
// second chance with the retry reminder appended; by the time we
// reach this function we are committed to either reifying a verdict
// or recording a rejection. Synthesising a Verdict shell (with empty
// Action so [ValidateVerdict] refuses it) routes through the Applier's
// reject path which emits SignalRejected and persists the
// pseudo-verdict on the signals row, completing the audit trail.
func (r *Runner) applyVerdict(ctx context.Context, sig SignalRef, agent AgentRef, runID uint32, llmText string) (*ApplyResult, error) {
	trimmed := strings.TrimSpace(llmText)
	var verdict Verdict
	if uerr := json.Unmarshal([]byte(trimmed), &verdict); uerr != nil {
		// Synthesize a verdict shell so the Applier's reject path
		// has something to record. Action stays the zero value
		// ("") which ValidateVerdict will refuse, and the synthetic
		// reasoning excerpt explains the parse failure in-line so
		// operators can see it without opening the agent_runs row.
		verdict = Verdict{
			Action:           "",
			Confidence:       0,
			ReasoningExcerpt: fmt.Sprintf("Judge response could not be parsed (parse_error): %s", uerr.Error()),
		}
	}
	return r.Applier.Apply(ctx, sig, agent, runID, verdict)
}

// logInvocation forwards to the configured InvocationLogger when
// present. A nil logger is silently dropped — single-binary smoke
// deployments may not have an ai_invocations writer wired and we do
// not want that to wedge the judge.
func (r *Runner) logInvocation(ctx context.Context, rec InvocationRecord) {
	if r.Log == nil {
		return
	}
	r.Log(ctx, rec)
}

// composeJudgePrompt assembles the Phase 2 user-prompt body the judge
// sees alongside its system prompt. The shape is intentionally a
// JSON-serialised signal snapshot — Phase 6 / L1 will layer the
// workspace context window, recent events, and active lenses on top.
//
// Returning a JSON-shaped prompt now gives the Phase 3 Applier a
// stable input contract to write a structured-output verdict parser
// against without a prompt rewrite.
func composeJudgePrompt(s SignalSnapshot) (string, error) {
	payload := map[string]any{
		"signal": map[string]any{
			"kind":        s.Kind,
			"source":      s.Source,
			"subjectType": s.SubjectType,
			"receivedAt":  s.ReceivedAtMs,
		},
	}
	if s.SubjectID.Valid {
		payload["signal"].(map[string]any)["subjectId"] = s.SubjectID.Int32
	}
	redactedPayload := redactJSONPayload(s.PayloadJSON)
	if len(redactedPayload) > 0 {
		var raw any
		if err := json.Unmarshal(redactedPayload, &raw); err == nil {
			payload["signal"].(map[string]any)["payload"] = raw
		} else {
			// Provider-shaped payload is not valid JSON (should not
			// happen in practice — the ingestion handlers reject
			// non-JSON bodies — but defensive serialisation keeps the
			// judge prompt well-formed even then).
			payload["signal"].(map[string]any)["payload"] = string(redactedPayload)
		}
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SQLAgentLookup is the default production AgentLookup. It runs one
// SELECT against ai_agents for each ExecuteJudge call, which matches
// the cost profile of the task-agent path (which also does one
// lookup per tick via GetAgentForExec). Keeping the lookups separate
// avoids extending the sqlc query surface for Phase 2; the optimised
// shared lookup is a Phase 6 follow-up.
type SQLAgentLookup struct {
	DB *sql.DB
}

// Load implements AgentLookup against the live ai_agents table.
func (l *SQLAgentLookup) Load(ctx context.Context, workspaceID, agentID uint32) (AgentSnapshot, error) {
	const q = `SELECT a.id, a.workspace_id, a.paused, a.system_prompt, m.name
		FROM ai_agents a
		INNER JOIN ai_models m ON m.id = a.model_id AND m.enabled = TRUE
		WHERE a.workspace_id = ? AND a.id = ? AND a.enabled = TRUE
		LIMIT 1`
	var snap AgentSnapshot
	row := l.DB.QueryRowContext(ctx, q, workspaceID, agentID)
	if err := row.Scan(&snap.AgentID, &snap.WorkspaceID, &snap.Paused, &snap.SystemPrompt, &snap.ModelName); err != nil {
		return AgentSnapshot{}, err
	}
	return snap, nil
}

// SQLSignalLookup is the default production SignalLookup. One SELECT
// per judge invocation; the signal row stays in the DB rather than
// being copied into the queue payload so retention-sweep semantics
// are unambiguous (a swept signal returns ErrSignalMissing here).
type SQLSignalLookup struct {
	DB *sql.DB
}

// Load implements SignalLookup. Returns sql.ErrNoRows when the signal
// no longer exists; the runner translates that to ErrSignalMissing.
//
// The UNIX_TIMESTAMP arithmetic is wrapped in CAST(... AS SIGNED)
// because `received_at` is DATETIME(3); without the cast MySQL 9.6+
// emits the multiplication result as DECIMAL(16,3) which the Go
// driver scans into []uint8 rather than int64, crashing the
// SignalSnapshot.ReceivedAtMs destination. The mirrors the same
// CAST pattern used by readSignalSnapshot in the integration tests
// (apps/flow-api/tests/signaljudge/prompt_render_test.go).
func (l *SQLSignalLookup) Load(ctx context.Context, workspaceID uint32, signalID int64) (SignalSnapshot, error) {
	const q = `SELECT id, BIN_TO_UUID(public_id, 0), workspace_id, source, kind, subject_type, subject_id, payload_json, CAST(UNIX_TIMESTAMP(received_at) * 1000 AS SIGNED)
		FROM signals
		WHERE workspace_id = ? AND id = ? AND enabled = TRUE
		LIMIT 1`
	var snap SignalSnapshot
	row := l.DB.QueryRowContext(ctx, q, workspaceID, signalID)
	if err := row.Scan(
		&snap.SignalID,
		&snap.PublicID,
		&snap.WorkspaceID,
		&snap.Source,
		&snap.Kind,
		&snap.SubjectType,
		&snap.SubjectID,
		&snap.PayloadJSON,
		&snap.ReceivedAtMs,
	); err != nil {
		return SignalSnapshot{}, err
	}
	return snap, nil
}
