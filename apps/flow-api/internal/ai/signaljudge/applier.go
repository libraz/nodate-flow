// Package signaljudge — Applier translates a judge verdict into
// task-domain events (ADR 0008 D4).
//
// The Applier is the sole writer of judge-driven task events. The
// invariant is enforced at runtime by [eventbus.Append], which
// rejects any caller that passes one of the judge-only kinds
// (TaskAutoCompleted, TaskRetroDrafted, SignalJudged, SignalApplied,
// SignalRejected). The Applier bypasses that gate via
// [eventbus.AppendJudgeEvent]; no other package may call that
// function — the package boundary is the gate.
//
// Apply() is deterministic and does not call the LLM. The verdict is
// the contract; the Runner produces it from the LLM response, the
// Applier validates and reifies it. A verdict that fails schema
// validation never reaches a task — instead SignalRejected is
// emitted with a structured reason payload so the audit trail still
// reflects that the judge ran.
package signaljudge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// SignalRef is the minimal projection of a signals row the Applier
// needs. Defined as a Plain Go struct (rather than reusing the
// sqlc-generated [generated.Signal]) so unit tests can construct
// fixtures without populating every column. Production wiring
// translates a generated.Signal into this shape in one place.
type SignalRef struct {
	// InternalID is signals.id; used for triggered_by_signal_id on
	// every emitted event and as the primary key for the
	// SignalUpdater write.
	InternalID int64
	// PublicID is signals.public_id; embedded into payload strings
	// (retro title, debug logs) so the timeline can render the
	// lineage without an extra join.
	PublicID string
	// WorkspaceID is the internal workspace id every event belongs
	// to.
	WorkspaceID uint32
	// Kind is the signal kind; passed verbatim to the autonomy
	// resolver so per-kind rules can match.
	Kind string
}

// AgentRef is the minimal projection of an ai_agents row the Applier
// needs. Only the internal id is actually written through to events
// (as actor_agent_id), but a typed struct keeps the contract honest
// against future additions.
type AgentRef struct {
	// InternalID is ai_agents.id; used as actor_agent_id on every
	// emitted event.
	InternalID uint32
}

// EventAppender is the narrow surface the Applier needs from the
// eventbus to write events. Production wiring satisfies it via the
// [eventbus.AppendJudgeEvent] free function bound through
// [AppendJudgeEventFunc]; tests can drop in an in-memory recorder
// without spinning up a real DB.
type EventAppender interface {
	AppendJudgeEvent(ctx context.Context, evt eventbus.Event) error
}

// AppendJudgeEventFunc adapts a plain function (the production
// signature being eventbus.AppendJudgeEvent) into an [EventAppender].
type AppendJudgeEventFunc func(ctx context.Context, evt eventbus.Event) error

// AppendJudgeEvent implements [EventAppender].
func (f AppendJudgeEventFunc) AppendJudgeEvent(ctx context.Context, evt eventbus.Event) error {
	return f(ctx, evt)
}

// TaskMutator is the narrow surface the Applier needs to mutate
// tasks in response to a verdict. Production implementations route
// these calls through the existing task / comment handlers so the
// derived_state projection, attachment lifecycle, and notification
// fan-out all stay on one rail.
//
// All methods receive the workspace's internal id and the task's
// internal id (the Applier resolves the public id once via
// [TaskResolver]). Returning an error short-circuits the entire
// Apply() call — the Applier does NOT swallow task mutator errors
// because the invariant is "every judge run lands a consistent set
// of events or none of them".
type TaskMutator interface {
	// CompleteTask marks the task as completed on behalf of the
	// judge agent. Implementations must not emit TaskAutoCompleted
	// themselves — the Applier owns that event.
	CompleteTask(ctx context.Context, workspaceID uint32, taskInternalID int64, agentID uint32) error
	// AddComment appends a comment to the task. The comment body is
	// the judge's reasoning excerpt, which the Applier has already
	// scrubbed of secret-bearing tokens via [SanitizeVerdict] and
	// length-capped via [ValidateVerdict] before this call.
	// Implementations emit task.comment.added through their normal
	// path; the Applier does not re-emit it.
	AddComment(ctx context.Context, workspaceID uint32, taskInternalID int64, agentID uint32, body string) error
	// DraftRetroTask creates a retrospective task linked to the
	// source task and signal. Returns the new task's internal id and
	// public id so the Applier can emit TaskRetroDrafted with the
	// new task reference. draft=true creates the task in draft
	// status; false creates it open (autonomy=auto branch).
	DraftRetroTask(ctx context.Context, workspaceID uint32, sourceTaskInternalID int64, agentID uint32, title string, draft bool) (newTaskInternalID int64, newTaskPublicID string, err error)
}

// TaskResolver resolves a task's public id to its internal id inside
// the workspace. Returned (0, false, nil) when the task does not
// exist so the Applier can emit SignalRejected with a structured
// "target_not_found" reason rather than crash.
type TaskResolver interface {
	ResolveTask(ctx context.Context, workspaceID uint32, publicID string) (int64, bool, error)
}

// SignalUpdater persists the judge-side mutations to the signals
// row: judge_run_id, judge_output_json, confidence, and applied_at.
// applied_at is only set on Apply branches that actually reified the
// verdict — Suggest, noop, and defer leave it NULL.
type SignalUpdater interface {
	UpdateJudgeOutput(ctx context.Context, signalInternalID int64, runID uint32, output json.RawMessage, confidence float64, appliedAt *time.Time) error
}

// Applier reifies one judge verdict against one signal.
//
// All dependencies are interfaces so unit tests can inject fakes;
// production wiring assembles the real bindings in cmd/api/main.go.
type Applier struct {
	// Bus is the judge-kind-aware event writer.
	Bus EventAppender
	// Tasks executes the task-domain side effects.
	Tasks TaskMutator
	// Resolver translates target_task_public_id → internal id.
	Resolver TaskResolver
	// Signals persists the judge_output / applied_at update.
	Signals SignalUpdater
	// Autonomy maps (workspace, kind, confidence) → autonomy decision.
	Autonomy AutonomyResolver
	// Now supplies the wall-clock used for applied_at; tests inject
	// a deterministic clock. nil falls back to time.Now.
	Now func() time.Time
	// Logger is used for warn-level diagnostics when a downstream
	// best-effort path fails. nil falls back to slog.Default().
	Logger *slog.Logger
}

// ApplyResult is the per-call summary the Runner persists to
// agent_runs.result_json so the timeline UI can show "this judge run
// emitted N events".
type ApplyResult struct {
	// EventsAppended counts every event the Applier emitted for the
	// verdict, including the always-present SignalJudged.
	EventsAppended int
	// Skipped reports whether the verdict was rejected or set to
	// noop/defer (i.e. signals.applied_at stayed NULL).
	Skipped bool
	// SkipReason explains why Skipped is true; empty when Skipped is
	// false.
	SkipReason string
	// AutonomyLevel records the autonomy decision the Applier used
	// so the timeline can render "suggested" vs "applied" badges
	// without re-resolving.
	AutonomyLevel AutonomyLevel
}

// Apply reifies verdict against sig. Returns an ApplyResult summary;
// any error indicates a non-recoverable infrastructure failure (DB
// write rejected, autonomy resolver crashed, ...). Verdict-level
// rejections are surfaced as SignalRejected events plus a non-nil
// ApplyResult.Skipped/SkipReason, not as a returned error.
//
// Flow:
//  1. Validate the verdict; on failure emit SignalRejected and return.
//  2. Resolve autonomy from (workspace, kind, confidence).
//  3. Branch on (action, autonomy):
//     - noop / defer → SignalJudged only, applied_at stays NULL.
//     - Suggest (any action) → SignalJudged only, applied_at stays NULL.
//     - Draft + generate_retro → SignalJudged + draft task + TaskRetroDrafted.
//     - Auto + complete_task → SignalJudged + TaskAutoCompleted + SignalApplied.
//     - Auto + add_comment → SignalJudged + comment side effect + SignalApplied.
//     - Auto + generate_retro → SignalJudged + open task + TaskRetroDrafted + SignalApplied.
//  4. Process verdict.ProposedEvents (Auto branches only).
//  5. Persist signals.judge_run_id / judge_output_json / confidence
//     / applied_at.
func (a *Applier) Apply(ctx context.Context, sig SignalRef, agent AgentRef, runID uint32, verdict Verdict) (*ApplyResult, error) {
	if a == nil {
		return nil, errors.New("signaljudge: nil applier")
	}
	if sig.InternalID <= 0 || sig.WorkspaceID == 0 || agent.InternalID == 0 {
		return nil, errors.New("signaljudge: invalid signal or agent ref")
	}
	if a.Bus == nil || a.Signals == nil || a.Autonomy == nil {
		return nil, errors.New("signaljudge: applier missing required dependencies")
	}

	nowFn := a.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	logger := a.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Schema gate. Reject malformed verdicts before anything else
	// touches the task domain, so a buggy LLM cannot cascade.
	if vErr := ValidateVerdict(verdict); vErr != nil {
		return a.rejectVerdict(ctx, sig, agent, runID, verdict, vErr)
	}

	// Response-side redaction. The verdict's free-text excerpt and any
	// proposed-event payloads are LLM-authored and from here flow
	// verbatim into the event log (judgedPayload, TaskAutoCompleted,
	// TaskRetroDrafted, SignalApplied) and into task comments via
	// AddComment. Scrub secret-bearing tokens once, on the local copy,
	// so every downstream use below reads the redacted text.
	verdict = SanitizeVerdict(verdict)

	signalInternalID := sig.InternalID
	taskID, taskOK, taskErr := a.resolveTarget(ctx, sig.WorkspaceID, verdict)
	if taskErr != nil {
		return nil, fmt.Errorf("signaljudge: resolve target task: %w", taskErr)
	}
	if verdict.requiresTask() && !taskOK {
		// Treat "target task not found" the same as a schema-invalid
		// verdict — the judge looked at the signal and named a task
		// that does not exist in the workspace, which is unsafe to
		// act on. Emit SignalRejected with a structured reason.
		return a.rejectVerdict(ctx, sig, agent, runID, verdict,
			fmt.Errorf("target_task_public_id %q not found", derefStr(verdict.TargetTaskPublicID)))
	}

	decision, dErr := a.Autonomy.Resolve(ctx, sig.WorkspaceID, signalkinds.Kind(sig.Kind), verdict.Confidence)
	if dErr != nil {
		return nil, fmt.Errorf("signaljudge: autonomy resolve: %w", dErr)
	}

	result := &ApplyResult{AutonomyLevel: decision.Level}

	// Every branch always emits SignalJudged. The payload is the
	// shape the timeline UI consumes; keep it stable across action
	// variants so a renderer change does not ripple across branches.
	judgedPayload := map[string]any{
		"action":           string(verdict.Action),
		"confidence":       verdict.Confidence,
		"autonomyLevel":    string(decision.Level),
		"reasoningExcerpt": verdict.ReasoningExcerpt,
	}
	if verdict.TargetTaskPublicID != nil {
		judgedPayload["targetTaskPublicId"] = *verdict.TargetTaskPublicID
	}
	if err := a.append(ctx, eventbus.Event{
		Type:                eventbus.SignalJudged,
		WorkspaceID:         sig.WorkspaceID,
		ActorAgentID:        agentInt64(agent),
		TaskID:              taskIntPtr(taskID),
		TriggeredBySignalID: &signalInternalID,
		Payload:             judgedPayload,
	}); err != nil {
		return nil, fmt.Errorf("signaljudge: append SignalJudged: %w", err)
	}
	result.EventsAppended++

	// Decide whether to materialise the verdict beyond SignalJudged.
	// Three short-circuit branches:
	//   - action=noop / defer: never materialise, regardless of
	//     autonomy. applied_at stays NULL.
	//   - autonomy=Suggest: never materialise. The verdict waits for
	//     a UI confirmation step.
	//   - everything else: branch on (action, autonomy) below.
	now := nowFn().UTC()
	var appliedAt *time.Time
	switch verdict.Action {
	case ActionNoop:
		result.Skipped = true
		result.SkipReason = "noop"
	case ActionDefer:
		result.Skipped = true
		result.SkipReason = "defer"
	default:
		if decision.Level == AutonomySuggest {
			result.Skipped = true
			result.SkipReason = "suggested_only"
			break
		}
		applied, applyErr := a.applyMaterialised(ctx, sig, agent, runID, verdict, decision, taskID)
		if applyErr != nil {
			return nil, applyErr
		}
		result.EventsAppended += applied
		appliedAt = &now
	}

	// Proposed events are only reified when the verdict was applied
	// (Auto branches). Under Suggest / noop / defer the LLM proposal
	// is recorded in judge_output_json but not emitted; the suggestion
	// UI can surface them later.
	if !result.Skipped && decision.Level == AutonomyAuto && len(verdict.ProposedEvents) > 0 {
		emitted, propErr := a.emitProposedEvents(ctx, sig, agent, taskID, verdict, decision)
		if propErr != nil {
			return nil, propErr
		}
		result.EventsAppended += emitted
	}

	// Persist the verdict and (optionally) applied_at. The Applier
	// always writes judge_run_id / judge_output_json / confidence —
	// even on Suggest / noop / defer — so the audit trail is
	// complete; applied_at distinguishes "judged" from "applied".
	verdictPayload, mErr := FormatVerdictPayload(verdict)
	if mErr != nil {
		// Marshalling our own struct should never fail; if it does,
		// we still want the events on the timeline, so we log loudly
		// and return success rather than rolling back the events
		// (which are immutable anyway).
		logger.ErrorContext(ctx, "signaljudge.applier: verdict marshal failed",
			slog.Any("err", mErr),
			slog.Uint64("workspace_internal", uint64(sig.WorkspaceID)),
			slog.Int64("signal_internal", signalInternalID),
		)
	} else if uErr := a.Signals.UpdateJudgeOutput(ctx, signalInternalID, runID, verdictPayload, verdict.Confidence, appliedAt); uErr != nil {
		// Same rationale: events are already on the timeline, so a
		// signals UPDATE failure is logged loudly but not fatal.
		logger.WarnContext(ctx, "signaljudge.applier: signals UPDATE failed",
			slog.Any("err", uErr),
			slog.Uint64("workspace_internal", uint64(sig.WorkspaceID)),
			slog.Int64("signal_internal", signalInternalID),
		)
	}

	return result, nil
}

// applyMaterialised handles the Draft / Auto autonomy branches: it
// runs the task-domain side effects and emits the action-specific
// follow-on events. Returns the number of events appended (excluding
// the always-emitted SignalJudged that Apply itself already wrote).
func (a *Applier) applyMaterialised(ctx context.Context, sig SignalRef, agent AgentRef, _ uint32, verdict Verdict, decision AutonomyDecision, taskID int64) (int, error) {
	signalInternalID := sig.InternalID
	emitted := 0

	switch verdict.Action {
	case ActionCompleteTask:
		if decision.Level != AutonomyAuto {
			// Only the Auto branch closes tasks. Draft/Suggest for
			// complete_task surface as suggestions only — adjacent
			// to the no-op semantics. A "queue draft completion"
			// surface could be added later; today nothing acts.
			return 0, nil
		}
		if a.Tasks != nil {
			if err := a.Tasks.CompleteTask(ctx, sig.WorkspaceID, taskID, agent.InternalID); err != nil {
				return emitted, fmt.Errorf("signaljudge: CompleteTask: %w", err)
			}
		}
		if err := a.append(ctx, eventbus.Event{
			Type:                eventbus.TaskAutoCompleted,
			WorkspaceID:         sig.WorkspaceID,
			ActorAgentID:        agentInt64(agent),
			TaskID:              taskIntPtr(taskID),
			TriggeredBySignalID: &signalInternalID,
			Payload: map[string]any{
				"reasoningExcerpt": verdict.ReasoningExcerpt,
				"confidence":       verdict.Confidence,
			},
		}); err != nil {
			return emitted, fmt.Errorf("signaljudge: append TaskAutoCompleted: %w", err)
		}
		emitted++

	case ActionAddComment:
		if decision.Level != AutonomyAuto {
			// Draft/Suggest for comments stay as suggestions.
			return 0, nil
		}
		if a.Tasks != nil {
			if err := a.Tasks.AddComment(ctx, sig.WorkspaceID, taskID, agent.InternalID, verdict.ReasoningExcerpt); err != nil {
				return emitted, fmt.Errorf("signaljudge: AddComment: %w", err)
			}
		}

	case ActionGenerateRetro:
		// Both Draft and Auto materialise a new task; the only
		// difference is the initial status flag.
		draft := decision.Level == AutonomyDraft
		var (
			newTaskInternalID int64
			newTaskPublicID   string
		)
		if a.Tasks != nil {
			id, pub, err := a.Tasks.DraftRetroTask(ctx, sig.WorkspaceID, taskID, agent.InternalID, retroTitle(sig), draft)
			if err != nil {
				return emitted, fmt.Errorf("signaljudge: DraftRetroTask: %w", err)
			}
			newTaskInternalID = id
			newTaskPublicID = pub
		}
		retroPayload := map[string]any{
			"reasoningExcerpt": verdict.ReasoningExcerpt,
			"confidence":       verdict.Confidence,
			"draft":            draft,
		}
		if newTaskPublicID != "" {
			retroPayload["newTaskPublicId"] = newTaskPublicID
		}
		if verdict.TargetTaskPublicID != nil {
			retroPayload["sourceTaskPublicId"] = *verdict.TargetTaskPublicID
		}
		var emitTaskID *int64
		if newTaskInternalID > 0 {
			emitTaskID = &newTaskInternalID
		} else if taskID > 0 {
			emitTaskID = taskIntPtr(taskID)
		}
		if err := a.append(ctx, eventbus.Event{
			Type:                eventbus.TaskRetroDrafted,
			WorkspaceID:         sig.WorkspaceID,
			ActorAgentID:        agentInt64(agent),
			TaskID:              emitTaskID,
			TriggeredBySignalID: &signalInternalID,
			Payload:             retroPayload,
		}); err != nil {
			return emitted, fmt.Errorf("signaljudge: append TaskRetroDrafted: %w", err)
		}
		emitted++
	}

	// SignalApplied seals every Auto branch. It is intentionally not
	// emitted for Draft (the draft task is the materialisation, not
	// a fait accompli that needs sealing) and obviously not for
	// Suggest / noop / defer.
	if decision.Level == AutonomyAuto {
		if err := a.append(ctx, eventbus.Event{
			Type:                eventbus.SignalApplied,
			WorkspaceID:         sig.WorkspaceID,
			ActorAgentID:        agentInt64(agent),
			TaskID:              taskIntPtr(taskID),
			TriggeredBySignalID: &signalInternalID,
			Payload: map[string]any{
				"action":           string(verdict.Action),
				"confidence":       verdict.Confidence,
				"reasoningExcerpt": verdict.ReasoningExcerpt,
			},
		}); err != nil {
			return emitted, fmt.Errorf("signaljudge: append SignalApplied: %w", err)
		}
		emitted++
	}

	return emitted, nil
}

// emitProposedEvents fans out verdict.ProposedEvents. The allowlist
// gate in [ValidateVerdict] already rejected unknown types so we
// just trust the type string here; payload is forwarded verbatim.
func (a *Applier) emitProposedEvents(ctx context.Context, sig SignalRef, agent AgentRef, taskID int64, verdict Verdict, decision AutonomyDecision) (int, error) {
	signalInternalID := sig.InternalID
	maxEvents := decision.MaxProposedEvents
	emitted := 0
	for i, pe := range verdict.ProposedEvents {
		if maxEvents > 0 && i >= maxEvents {
			break
		}
		var payload any
		if len(pe.PayloadJSON) > 0 {
			var raw any
			if err := json.Unmarshal(pe.PayloadJSON, &raw); err == nil {
				payload = raw
			} else {
				// Forward the raw bytes as a string so the timeline
				// still shows something rather than dropping silently.
				payload = string(pe.PayloadJSON)
			}
		}
		if err := a.append(ctx, eventbus.Event{
			// The kind arrives as a string from the judge's JSON output;
			// ValidateVerdict has already rejected anything outside
			// allowedProposedEventTypes, so the conversion is checked.
			Type:                eventbus.Kind(pe.Type),
			WorkspaceID:         sig.WorkspaceID,
			ActorAgentID:        agentInt64(agent),
			TaskID:              taskIntPtr(taskID),
			TriggeredBySignalID: &signalInternalID,
			Payload:             payload,
		}); err != nil {
			return emitted, fmt.Errorf("signaljudge: append proposed[%d] %q: %w", i, pe.Type, err)
		}
		emitted++
	}
	return emitted, nil
}

// rejectVerdict emits SignalRejected with a structured reason, then
// records the verdict (with confidence) on the signals row without
// setting applied_at. The original verdict bytes are stored so
// operators can inspect what the LLM actually returned.
func (a *Applier) rejectVerdict(ctx context.Context, sig SignalRef, agent AgentRef, runID uint32, verdict Verdict, vErr error) (*ApplyResult, error) {
	signalInternalID := sig.InternalID
	reasonPayload := map[string]any{
		"reason":          "verdict_invalid",
		"validationError": vErr.Error(),
		"action":          string(verdict.Action),
		"confidence":      verdict.Confidence,
	}
	if err := a.append(ctx, eventbus.Event{
		Type:                eventbus.SignalRejected,
		WorkspaceID:         sig.WorkspaceID,
		ActorAgentID:        agentInt64(agent),
		TriggeredBySignalID: &signalInternalID,
		Payload:             reasonPayload,
	}); err != nil {
		return nil, fmt.Errorf("signaljudge: append SignalRejected: %w", err)
	}
	// Persist the malformed verdict verbatim. Marshal failure here
	// is logged but does not block returning the rejection result —
	// the SignalRejected event is already on the timeline.
	if raw, mErr := FormatVerdictPayload(verdict); mErr == nil {
		_ = a.Signals.UpdateJudgeOutput(ctx, signalInternalID, runID, raw, verdict.Confidence, nil)
	}
	return &ApplyResult{
		EventsAppended: 1,
		Skipped:        true,
		SkipReason:     "verdict_invalid",
	}, nil
}

// resolveTarget looks up the internal id of verdict.TargetTaskPublicID.
// Returns (0, false, nil) when no public id was supplied (noop /
// defer branches), letting the caller decide whether that is an
// error based on the verdict's action.
func (a *Applier) resolveTarget(ctx context.Context, workspaceID uint32, verdict Verdict) (int64, bool, error) {
	if verdict.TargetTaskPublicID == nil || *verdict.TargetTaskPublicID == "" {
		return 0, false, nil
	}
	if a.Resolver == nil {
		return 0, false, nil
	}
	id, ok, err := a.Resolver.ResolveTask(ctx, workspaceID, *verdict.TargetTaskPublicID)
	if err != nil {
		return 0, false, err
	}
	return id, ok, nil
}

// append is a thin wrapper around the EventAppender that lets us
// swap in a recorder for tests without leaking the interface name
// across the file.
func (a *Applier) append(ctx context.Context, evt eventbus.Event) error {
	return a.Bus.AppendJudgeEvent(ctx, evt)
}

// agentInt64 returns the agent's internal id as a *int64 for the
// eventbus.Event.ActorAgentID field.
func agentInt64(a AgentRef) *int64 {
	if a.InternalID == 0 {
		return nil
	}
	id := int64(a.InternalID)
	return &id
}

// taskIntPtr returns a *int64 from a non-zero taskID; nil for zero.
// Pointer semantics line up with eventbus.Event.TaskID which is nil
// for workspace-scoped events.
func taskIntPtr(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}

// derefStr returns the dereferenced string or "" for a nil pointer.
// Used for log/payload formatting where empty strings are fine.
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// requiresTask reports whether the verdict's action mandates a
// target task. Mirrors the corresponding branch in [ValidateVerdict]
// so the Applier and the validator agree on the requirement.
func (v Verdict) requiresTask() bool {
	switch v.Action {
	case ActionCompleteTask, ActionGenerateRetro, ActionAddComment:
		return true
	}
	return false
}

// retroTitle builds a deterministic title for a retro-drafted task.
// Format pins the source signal and kind so the timeline can render
// the lineage without an extra join. Pure helper — kept here so the
// title template is in one place and easy to evolve.
func retroTitle(sig SignalRef) string {
	return fmt.Sprintf("Retro: %s (signal %s)", sig.Kind, sig.PublicID)
}
