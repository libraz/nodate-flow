// Package signaljudge — verdict schema and validator for the LLM
// signal_judge agent's structured output (ADR 0008 D4 / Phase 3 J4).
//
// The verdict is the contract between the Runner (LLM-facing) and the
// Applier (event-emitting). The Runner is responsible for parsing the
// LLM response into a [Verdict]; the Applier rejects any verdict that
// fails [ValidateVerdict] and emits SignalRejected so the audit trail
// always reflects what the judge attempted, even when its output was
// malformed.
//
// Schema discipline (ADR 0008 D4):
//   - Action is a closed enumeration; new action kinds are a code
//     change, not a prompt change.
//   - Confidence is bounded to [0.0, 1.0] so the autonomy resolver can
//     compare against ai_settings.auto_action_threshold without
//     branching on out-of-range values.
//   - ProposedEvents[].Type is restricted to a hard-coded allowlist.
//     Without this gate, a creative LLM could propose task.deleted
//     and the Applier would happily emit it. The set is intentionally
//     minimal at landing (only the five judge-only kinds plus
//     task.comment.added) and grows only by code review.
package signaljudge

import (
	"encoding/json"
	"fmt"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// Verdict is the structured output the signal_judge LLM returns.
// Marshalling round-trips through [json.Marshal]; the JSON shape is
// pinned by the field tags below and forms the prompt-side schema
// the Runner asks the LLM to produce (Phase 6 will switch this to
// provider-native structured output where available).
type Verdict struct {
	// Action selects which Applier branch reifies the verdict.
	Action VerdictAction `json:"action"`
	// TargetTaskPublicID names the task the action targets. Required
	// for complete_task, generate_retro (the source task that the
	// retro relates to), and add_comment. Must be nil for noop / defer
	// — those branches do not write to a specific task.
	TargetTaskPublicID *string `json:"target_task_public_id,omitempty"`
	// Confidence is the judge's confidence in the verdict, scaled to
	// [0.0, 1.0]. Compared against the autonomy resolver's thresholds
	// to decide suggest / draft / auto.
	Confidence float64 `json:"confidence"`
	// ReasoningExcerpt is a short free-text explanation the timeline UI
	// surfaces alongside the verdict. It is scrubbed of secret-bearing
	// tokens by [SanitizeVerdict] (which the Applier runs before any
	// event or comment is persisted) and length-capped by
	// [ValidateVerdict] so prompt-injection attempts to stuff arbitrary
	// content into the audit log are bounded at the Applier rather than
	// relying on the LLM to truncate or sanitise itself.
	ReasoningExcerpt string `json:"reasoning_excerpt"`
	// ProposedEvents are optional extra events the Applier should
	// emit alongside the primary action. Restricted to the kinds
	// listed in [allowedProposedEventTypes] — any other type causes
	// the entire verdict to be rejected.
	ProposedEvents []ProposedEvent `json:"proposed_events,omitempty"`
}

// VerdictAction is the closed enumeration of branches the Applier
// supports.
type VerdictAction string

// Verdict action values. New values require Applier branch coverage
// and an autonomy resolver mapping — adding one here without the
// matching Apply() arm is a build error caught by the action switch.
const (
	// ActionCompleteTask asks the Applier to mark the target task as
	// completed by emitting TaskAutoCompleted (under autonomy=auto).
	ActionCompleteTask VerdictAction = "complete_task"
	// ActionGenerateRetro asks the Applier to create a draft (or open,
	// under autonomy=auto) task linked back to the source signal /
	// target task and emit TaskRetroDrafted.
	ActionGenerateRetro VerdictAction = "generate_retro"
	// ActionAddComment asks the Applier to append a comment on the
	// target task (under autonomy=auto only; suggest/draft surface
	// the proposed comment in a future Phase 6 review queue).
	ActionAddComment VerdictAction = "add_comment"
	// ActionDefer means the judge chose to wait for more information
	// rather than act. The Applier emits SignalJudged only and does
	// not set signals.applied_at — a follow-up signal may flip the
	// decision later.
	ActionDefer VerdictAction = "defer"
	// ActionNoop means the judge looked at the signal and decided no
	// action is warranted. Same Applier behaviour as defer; the
	// distinction is purely semantic in the audit trail.
	ActionNoop VerdictAction = "noop"
)

// MaxReasoningExcerptLen caps the byte length of [Verdict.ReasoningExcerpt].
// Picked to be large enough for one sentence of LLM rationale but
// small enough that an unbounded prompt-injection attempt is rejected
// rather than stored on the timeline.
const MaxReasoningExcerptLen = 500

// ProposedEvent is a single optional additional event the Applier
// emits alongside the primary action. The Type must be in
// [allowedProposedEventTypes]; PayloadJSON is forwarded verbatim to
// [eventbus.AppendJudgeEvent] as the event's payload.
type ProposedEvent struct {
	Type        string          `json:"type"`
	PayloadJSON json.RawMessage `json:"payload_json"`
}

// allowedProposedEventTypes is the hard-coded allowlist of event
// kinds the judge may include in [Verdict.ProposedEvents]. The five
// Applier-driven kinds are present so a judge can, e.g., emit a
// SignalApplied without a separate primary action; task.comment.added
// is present so a verdict can attach a comment as a side effect
// without needing the primary action to also be add_comment.
//
// Any new entry here MUST come with a security review — proposed
// events bypass the per-action switch in the Applier, so the
// allowlist is the only gate stopping a creative LLM from emitting
// task.deleted or similar destructive kinds.
var allowedProposedEventTypes = map[string]bool{
	eventbus.SignalJudged:      true,
	eventbus.SignalApplied:     true,
	eventbus.SignalRejected:    true,
	eventbus.TaskAutoCompleted: true,
	eventbus.TaskRetroDrafted:  true,
	eventbus.TaskCommentAdded:  true,
}

// ValidateVerdict checks that v can safely be handed to the Applier.
// Returns a typed [*apierr.APIError] carrying INTERNAL.UNEXPECTED on
// schema violation so the Applier's SignalRejected payload can carry
// the canonical code; the error message is included as the validation
// detail so operators can debug malformed LLM output without parsing
// the verdict by hand.
//
// Validation order (each step short-circuits):
//  1. Action is one of the closed enumeration values.
//  2. Confidence is finite and in [0.0, 1.0].
//  3. ReasoningExcerpt is non-empty and <= MaxReasoningExcerptLen.
//  4. TargetTaskPublicID is set when action requires it and nil for
//     noop/defer.
//  5. Every ProposedEvents[].Type is in [allowedProposedEventTypes].
func ValidateVerdict(v Verdict) error {
	switch v.Action {
	case ActionCompleteTask, ActionGenerateRetro, ActionAddComment, ActionDefer, ActionNoop:
		// ok
	default:
		return verdictError("invalid action %q", v.Action)
	}

	// NaN / Inf comparisons against [0, 1] are false, so checking the
	// range here also rejects non-finite values without an extra
	// math.IsNaN / math.IsInf branch.
	if !(v.Confidence >= 0.0 && v.Confidence <= 1.0) {
		return verdictError("confidence %v not in [0.0, 1.0]", v.Confidence)
	}

	if v.ReasoningExcerpt == "" {
		return verdictError("reasoning_excerpt is required")
	}
	if len(v.ReasoningExcerpt) > MaxReasoningExcerptLen {
		return verdictError("reasoning_excerpt length %d exceeds max %d", len(v.ReasoningExcerpt), MaxReasoningExcerptLen)
	}

	switch v.Action {
	case ActionCompleteTask, ActionGenerateRetro, ActionAddComment:
		if v.TargetTaskPublicID == nil || *v.TargetTaskPublicID == "" {
			return verdictError("action %q requires target_task_public_id", v.Action)
		}
	case ActionDefer, ActionNoop:
		if v.TargetTaskPublicID != nil && *v.TargetTaskPublicID != "" {
			return verdictError("action %q must not set target_task_public_id", v.Action)
		}
	}

	for i, pe := range v.ProposedEvents {
		if pe.Type == "" {
			return verdictError("proposed_events[%d].type is required", i)
		}
		if !allowedProposedEventTypes[pe.Type] {
			return verdictError("proposed_events[%d].type %q is not allowed", i, pe.Type)
		}
	}

	return nil
}

// SanitizeVerdict returns a copy of v with every operator-facing
// free-text field scrubbed of secret-bearing tokens before the verdict
// reaches the event log or a task comment. The LLM produces
// [Verdict.ReasoningExcerpt] and [ProposedEvent.PayloadJSON] from a
// prompt that already redacts its inputs (prompt.go), but a creative or
// compromised model can still echo or fabricate a token in its output;
// this is the second redaction pass on the response side, mirroring the
// Runner's redaction of the raw LLM response before it lands in
// ai_invocations (runner.go:275).
//
// Reuses the same package-local redactors the prompt path uses:
//   - [redactFreeFormText] for the prose excerpt (Bearer / sk- / ghp_ … prefixes).
//   - [redactJSON] for each proposed-event payload (JSON-key blocklist +
//     per-string substring scrub).
//
// The input is treated as immutable: ProposedEvents is copied before
// any payload is rewritten so a caller holding the original slice is
// unaffected.
func SanitizeVerdict(v Verdict) Verdict {
	v.ReasoningExcerpt = redactFreeFormText(v.ReasoningExcerpt)
	if len(v.ProposedEvents) > 0 {
		cleaned := make([]ProposedEvent, len(v.ProposedEvents))
		copy(cleaned, v.ProposedEvents)
		for i := range cleaned {
			cleaned[i].PayloadJSON = redactJSON(cleaned[i].PayloadJSON)
		}
		v.ProposedEvents = cleaned
	}
	return v
}

// verdictError wraps a formatted validation message in a typed
// APIError so callers can use errors.As to inspect the Spec while
// still getting a human-readable detail string in the SignalRejected
// payload.
func verdictError(format string, args ...any) error {
	return apierrors.Newf(apierrors.InternalUnexpected, format, args...)
}

// FormatVerdictPayload marshals v into a deterministic JSON payload
// suitable for storage in signals.judge_output_json. Returns the raw
// bytes so the caller controls retention; the Applier passes them
// directly to the SignalUpdater.
func FormatVerdictPayload(v Verdict) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("signaljudge: marshal verdict: %w", err)
	}
	return raw, nil
}
