package signaljudge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/airequest"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/outputcontract"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// ProviderResolver is the narrow contract the runner needs to obtain the
// client for the provider that owns the judge agent's model. It mirrors
// [ai.AgentProviderResolver] so production wiring can pass the same
// [providers.WorkspaceResolver] instance to both paths.
type ProviderResolver interface {
	ForProvider(ctx context.Context, workspaceID uint32, providerPublicID types.PublicID) (providers.Provider, error)
}

// CostGuard is the narrow contract the runner needs to participate in
// the per-workspace daily cap. It mirrors [ai.CostGuard.Check] so the
// task-agent and signal-judge paths share one budget — there is one
// budget per workspace, not one per agent kind (ADR 0008 D3).
type CostGuard interface {
	Check(ctx context.Context, workspaceID uint32) error
}

// InvocationLogger, InvocationRecord and InvocationMetricsHook alias the
// shared AI contracts so signal judge audit rows retain agent, task,
// audience and input-resource provenance without a lossy conversion.
type InvocationLogger = ai.InvocationLogger
type InvocationRecord = ai.InvocationRecord
type InvocationMetricsHook = ai.InvocationMetricsHook

// AgentLookup is the narrow surface the runner needs to read an
// agent's system prompt + kind by internal id. The production wiring
// supplies a function that does a single SELECT against ai_agents
// (and the runner already has *sql.DB in hand, so the production
// adapter is a one-liner).
type AgentLookup interface {
	Load(ctx context.Context, workspaceID, agentID uint32) (AgentSnapshot, error)
	// CheckCostCap returns [ai.ErrAgentCostCapExceeded] once the agent's
	// month-to-date spend reaches its monthly cap.
	CheckCostCap(ctx context.Context, agentID uint32) error
}

// AgentSnapshot is the minimal projection of an ai_agents row the
// runner needs to compose a judge invocation. system_prompt is taken
// from the row when non-empty so workspace admins can override the
// default SystemPromptSkeleton without code edits; an empty value
// falls back to the skeleton.
type AgentSnapshot struct {
	WorkspaceID  uint32
	AgentID      uint32
	Paused       bool
	SystemPrompt string
	// Settings carries the model, output cap, and temperature the agent
	// is configured with. It reaches the provider through
	// [airequest.ForAgent]; a zero value means the lookup did not consult
	// an agent row and the provider's own defaults apply.
	Settings airequest.AgentSettings
	// ProviderPublicID is the provider that owns the agent's bound model.
	ProviderPublicID types.PublicID
}

// SignalLookup is the narrow surface the runner needs to load the
// signal payload by internal id. The production adapter does a single
// SELECT against signals and returns the workspace id, kind, and
// payload_json. The runner uses these to build the judge prompt; the
// Applier will read the verdict back from agent_runs.
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
	// populate it (e.g. deployments predating the widened column
	// projection).
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
// Applier does — and it does not write to signals
// either; for now the verdict is logged via the InvocationLogger so
// every judge run produces an audit-visible ai_invocations row.
//
// The runner parses the LLM response into a
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
	// renders alongside the system prompt. Every
	// field on [PromptDeps] is optional; an empty value collapses
	// the relevant context section so the runner can be wired
	// progressively. When the entire struct is zero-valued the
	// runner falls back to the legacy [composeJudgePrompt] shape
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
// minimal prompt, invokes the provider, logs the redacted
// invocation, and returns the ExecutionResult the OrchestratorRunner
// uses for memo bookkeeping / handoff decisions.
//
// The function does not write events; the Applier
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

	// CostCapHit lets the OrchestratorRunner's existing cost_cap handoff
	// trigger fire for judge runs too, with no new branching in agentruntime
	// itself.
	if capHit, err := ai.CheckBudget(ctx, r.Guard, workspaceID); err != nil {
		result.CostCapHit = capHit
		return result, err
	}
	if err := r.Agents.CheckCostCap(ctx, agentID); err != nil {
		result.CostCapHit = errors.Is(err, ai.ErrAgentCostCapExceeded)
		return result, err
	}

	signal, err := r.Signals.Load(ctx, workspaceID, signalID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, ErrSignalMissing
		}
		return result, fmt.Errorf("signaljudge: signal load: %w", err)
	}

	prov, err := r.Resolver.ForProvider(ctx, workspaceID, agent.ProviderPublicID)
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
	userPrompt, offered, perr := r.renderUserPrompt(ctx, signal)
	if perr != nil {
		return result, fmt.Errorf("signaljudge: compose prompt: %w", perr)
	}

	ctx = ai.WithAgentID(ctx, agentID)
	ctx = ai.WithInputResources(ctx, offeredTaskResources(offered)...)
	ctx = providers.WithWorkspaceID(ctx, workspaceID)
	outputSpec, err := VerdictOutputSpec()
	if err != nil {
		return result, err
	}
	req := airequest.ForAgent(prov, agent.Settings, airequest.Args{
		System: systemPrompt,
		Prompt: userPrompt,
		Output: outputSpec,
	})
	ref := SignalRef{
		InternalID:  signal.SignalID,
		PublicID:    signal.PublicID,
		WorkspaceID: signal.WorkspaceID,
		Kind:        signal.Kind,
		// Only tasks the model was shown may become a verdict target.
		OfferedTasks: offered,
	}
	agentRef := AgentRef{InternalID: agentID}
	runID := uint32(0)
	if r.RunIDFromContext != nil {
		runID = r.RunIDFromContext(ctx)
	}

	accounting := &ai.Orchestrator{
		LogInvoke:    r.Log,
		OnInvocation: r.OnInvocation,
	}

	var verdict Verdict
	validationAttempted := false
	validate := func(response *providers.Response) error {
		validationAttempted = true
		parsed, validateErr := validateSignalVerdictResponse(outputSpec, response)
		if validateErr != nil {
			return validateErr
		}
		verdict = parsed
		return nil
	}
	response, outcomeErr := accounting.CompleteAndRecord(ctx, workspaceID, "signal_judge", prov, req, validate)
	addResponseCost(&result, response)
	if outcomeErr != nil {
		if validationAttempted && isRepairableVerdictFailure(response, outcomeErr) {
			capHit, budgetErr := ai.CheckBudget(ctx, r.Guard, workspaceID)
			if budgetErr == nil {
				budgetErr = r.Agents.CheckCostCap(ctx, agentID)
				capHit = errors.Is(budgetErr, ai.ErrAgentCostCapExceeded)
			}
			if budgetErr != nil {
				result.CostCapHit = capHit
				if rejectErr := r.rejectOutput(ctx, ref, agentRef, outcomeErr); rejectErr != nil {
					return result, rejectErr
				}
				return result, fmt.Errorf("signaljudge: repair budget check: %w", budgetErr)
			}

			retryReq := airequest.WithPrompt(req, userPrompt+"\n\n"+retryReminder)
			var retryVerdict Verdict
			retryValidate := func(retryResponse *providers.Response) error {
				parsed, validateErr := validateSignalVerdictResponse(outputSpec, retryResponse)
				if validateErr != nil {
					return validateErr
				}
				retryVerdict = parsed
				return nil
			}
			retryResponse, retryErr := accounting.CompleteAndRecord(ctx, workspaceID, "signal_judge", prov, retryReq, retryValidate)
			addResponseCost(&result, retryResponse)
			if retryErr != nil {
				// A second unusable answer is terminal. Preserve the first
				// invalid response as the rejection reason if the repair call
				// failed before it returned a decoded response.
				rejectionErr := retryErr
				if retryResponse == nil && !isProviderOutputFailure(retryErr) {
					rejectionErr = outcomeErr
				}
				if rejectErr := r.rejectOutput(ctx, ref, agentRef, rejectionErr); rejectErr != nil {
					return result, rejectErr
				}
				if r.Applier == nil {
					return result, fmt.Errorf("signaljudge: repair output: %w", retryErr)
				}
				return result, nil
			}
			verdict = retryVerdict
			response = retryResponse
		} else {
			if !isProviderOutputFailure(outcomeErr) {
				return result, fmt.Errorf("signaljudge: provider call: %w", outcomeErr)
			}
			if rejectErr := r.rejectOutput(ctx, ref, agentRef, outcomeErr); rejectErr != nil {
				return result, rejectErr
			}
			if r.Applier == nil {
				return result, fmt.Errorf("signaljudge: provider output: %w", outcomeErr)
			}
			return result, nil
		}
	}
	if response != nil {
		result.LastThought = logutil.Redact(response.Text)
	}

	// The model attempt is complete and the output contract passed. Domain
	// authorization, autonomy, and workflow checks remain in Apply; an
	// application failure is therefore distinct from model validation.
	if r.Applier != nil {
		applyRes, applyErr := r.Applier.Apply(ctx, ref, agentRef, runID, verdict)
		if applyErr != nil {
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
// repair attempt when the first complete response failed validation. Kept
// intentionally short and procedural — the model must understand the
// constraint without spending tokens on a polite re-explanation.
const retryReminder = "Your previous response was not valid JSON matching the schema. Please respond with valid JSON only, no prose, no markdown fences."

func validateSignalVerdictResponse(spec *providers.OutputSpec, response *providers.Response) (Verdict, error) {
	if response == nil {
		return Verdict{}, providers.ErrResponseSchemaMismatch
	}
	if err := outputcontract.Validate(spec, []byte(response.Text)); err != nil {
		switch {
		case errors.Is(err, outputcontract.ErrInvalidJSON):
			return Verdict{}, providers.ErrResponseInvalidJSON
		case errors.Is(err, outputcontract.ErrValidationFailed):
			return Verdict{}, providers.ErrResponseSchemaMismatch
		default:
			// A broken static contract is a code/configuration failure. It
			// must not spend a repair call or expose validator internals.
			return Verdict{}, errors.New("signaljudge: output contract unavailable")
		}
	}
	var verdict Verdict
	if err := json.Unmarshal([]byte(strings.TrimSpace(response.Text)), &verdict); err != nil {
		return Verdict{}, providers.ErrResponseInvalidJSON
	}
	if err := ValidateVerdict(verdict); err != nil {
		return Verdict{}, providers.ErrResponseSchemaMismatch
	}
	return verdict, nil
}

func isRepairableVerdictFailure(response *providers.Response, err error) bool {
	if response == nil || response.Termination != providers.TerminationComplete {
		return false
	}
	return errors.Is(err, providers.ErrResponseInvalidJSON) || errors.Is(err, providers.ErrResponseSchemaMismatch)
}

func isProviderOutputFailure(err error) bool {
	return errors.Is(err, providers.ErrGenerationIncomplete) ||
		errors.Is(err, providers.ErrModelRefused) ||
		errors.Is(err, providers.ErrResponseInvalidJSON) ||
		errors.Is(err, providers.ErrResponseSchemaMismatch)
}

func addResponseCost(result *agentruntime.ExecutionResult, response *providers.Response) {
	if response == nil {
		return
	}
	result.CostMicros += response.EstimatedCostMicros()
	result.CostCents = result.CostMicros / 10_000
}

func offeredTaskResources(offered map[string]struct{}) []ai.InvocationResource {
	ids := make([]string, 0, len(offered))
	for publicID := range offered {
		ids = append(ids, publicID)
	}
	sort.Strings(ids)
	resources := make([]ai.InvocationResource, 0, len(ids))
	for _, publicID := range ids {
		resources = append(resources, ai.TaskInputResource(publicID))
	}
	return resources
}

func (r *Runner) rejectOutput(ctx context.Context, sig SignalRef, agent AgentRef, failure error) error {
	if r.Applier == nil {
		return nil
	}
	_, err := r.Applier.RejectOutput(ctx, sig, agent, failure)
	return err
}

// renderUserPrompt builds the user-message body. When the [Runner]
// has been configured with [PromptDeps] (any non-nil lookup), it
// invokes [BuildPromptContext] + [RenderUserPromptOffered] for the full
// context window, and returns the task ids the body lists. Otherwise it falls back to the legacy
// [composeJudgePrompt] shape — a JSON-serialised signal
// snapshot only — so callers that have not yet wired the lookups
// keep working unchanged.
func (r *Runner) renderUserPrompt(ctx context.Context, sig SignalSnapshot) (string, map[string]struct{}, error) {
	if r.Prompt.RecentTasks == nil && r.Prompt.LinkedTasks == nil && r.Prompt.JudgeInstructions == nil && r.Prompt.WorkspaceNow == nil {
		prompt, err := composeJudgePrompt(sig)
		return prompt, nil, err
	}
	pc, err := BuildPromptContext(ctx, r.Prompt, sig)
	if err != nil {
		return "", nil, err
	}
	prompt, offered := RenderUserPromptOffered(pc)
	return prompt, offered, nil
}

// composeJudgePrompt assembles the fallback user-prompt body the judge
// sees alongside its system prompt. The shape is intentionally a
// JSON-serialised signal snapshot; [RenderUserPrompt] layers the
// workspace context window, recent events, and active lenses on top.
//
// Returning a JSON-shaped prompt gives the Applier a
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
// the cost profile of the task-agent path.
//
// It uses the same GetAgentForExec query that path uses, rather than a
// judge-local SELECT of its own. Two hand-written projections of the
// same row are how the judge came to run without the agent's configured
// output cap and temperature while the task agent (eventually) had them:
// widening one query left the other behind, silently. Sharing the query
// makes a new per-agent knob arrive on both paths or neither.
type SQLAgentLookup struct {
	DB *sql.DB
}

// Load implements AgentLookup against the live ai_agents table.
func (l *SQLAgentLookup) Load(ctx context.Context, workspaceID, agentID uint32) (AgentSnapshot, error) {
	row, err := generated.New(l.DB).GetAgentForExec(ctx, generated.GetAgentForExecParams{
		WorkspaceID: workspaceID,
		ID:          agentID,
	})
	if err != nil {
		return AgentSnapshot{}, err
	}
	return AgentSnapshot{
		WorkspaceID:  row.WorkspaceID,
		AgentID:      row.ID,
		Paused:       row.Paused,
		SystemPrompt: row.SystemPrompt,
		Settings:     airequest.FromExecRow(row),

		ProviderPublicID: row.ProviderPublicID,
	}, nil
}

// CheckCostCap implements AgentLookup through the same
// [ai.CheckAgentCostCap] the task-agent executor and the MCP guard share.
func (l *SQLAgentLookup) CheckCostCap(ctx context.Context, agentID uint32) error {
	return ai.CheckAgentCostCap(ctx, generated.New(l.DB), agentID, time.Now())
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
