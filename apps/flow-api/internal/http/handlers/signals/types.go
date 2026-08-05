// Package signals contains Huma operation handlers and chi handlers for the
// /signals and /webhooks/* endpoints. Webhook handlers are intentionally
// implemented at the chi layer (not Huma) so they can verify the raw
// request body before unmarshalling.
package signals

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
	"github.com/libraz/nodate-flow/packages/go-shared/signalwire"
)

// sourceEnumTag is the comma-joined source enum value baked into the
// SignalCreateInputBody.Source `enum:"..."` struct tag below. Huma reads
// struct tags via reflection, so the tag value cannot be computed at
// runtime — it must be a literal. To keep it honest, init() asserts the
// literal equals signalwire.SourceEnumTag(); a future drift between the
// hand-written tag and the canonical source list (when, e.g., a new
// source is added to signalwire but not here) fails the build's test run
// and panics at startup instead of silently rejecting valid signals.
const sourceEnumTag = "manual,github,slack,email,google,webhook,calendar,discord"

func init() {
	if sourceEnumTag != signalwire.SourceEnumTag() {
		panic(fmt.Sprintf(
			"signals: SignalCreateInputBody.Source enum tag %q drifted from signalwire.SourceEnumTag() %q; "+
				"update the enum tag in signals/types.go to match packages/go-shared/signalwire",
			sourceEnumTag, signalwire.SourceEnumTag()))
	}
}

// JudgeEnqueuer is the narrow surface signal handlers use to wake the
// signal_judge agent runtime after a successful signal insert. The
// production wiring satisfies this with *signaljudge.Enqueuer; the
// interface is declared here so handlers do not import the signaljudge
// package directly (which would pull the agentruntime + provider stack
// into a webhook handler that does not otherwise need them).
//
// Implementations MUST be best-effort: a non-nil error returned from
// EnqueueForSignal is logged by the caller but never surfaces to the
// HTTP response — the signal row is the canonical write, and failing
// the judge dispatch must not cause the signal POST / webhook ACK to
// fail. A nil JudgeEnqueuer is the safe single-binary default and
// disables judge dispatch.
type JudgeEnqueuer interface {
	EnqueueForSignal(ctx context.Context, signalID int64, workspaceID uint32, kind signalkinds.Kind) error
}

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	// Audit records audit log entries. Nil-safe.
	Audit *audit.Recorder

	// GhWebhookSecret is the shared secret used to verify inbound
	// GitHub webhook signatures.
	GhWebhookSecret string
	// SlackSigningSecret is the v0 signing secret used to verify
	// inbound Slack event signatures.
	SlackSigningSecret string
	// GoogleChannelToken is the per-channel shared secret Google Drive
	// echoes back via X-Goog-Channel-Token on every push notification.
	GoogleChannelToken string
	// DefaultWorkspaceID is the workspace public id (UUID v7) that
	// inbound webhook signals are routed to.
	DefaultWorkspaceID string

	// JudgeEnqueuer is the optional hook called after a signal row is
	// inserted to wake any matching signal_judge agents (ADR 0008 D3).
	// Nil disables judge dispatch — the signal still lands and the
	// rest of the system continues to work, which is the single-binary
	// default before LLM provider config is in place.
	JudgeEnqueuer JudgeEnqueuer
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// Signal is the public DTO for a signals row. The subject pair
// (`subjectType` + `subjectId`) and `expiresAt` were added in ADR 0008 D1;
// every new write path populates them, legacy writes that predate the
// registry leave `subjectType` set to "workspace" with NULL `subjectId`.
type Signal struct {
	ID          string          `json:"id" doc:"Signal public id (UUID v7)"`
	TaskID      string          `json:"taskId,omitempty"`
	Source      string          `json:"source"`
	Kind        string          `json:"kind"`
	ExternalID  string          `json:"externalId,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	SubjectType string          `json:"subjectType" enum:"user,task,workspace,calendar_event" doc:"What the signal is about (ADR 0008 D1)."`
	SubjectID   string          `json:"subjectId,omitempty" doc:"Subject row public id (UUID v7). Omitted when subject_type=workspace because workspace_id already identifies the owner."`
	ReceivedAt  int64           `json:"receivedAt"`
	ExpiresAt   *int64          `json:"expiresAt,omitempty" doc:"Provider-derived TTL in unix seconds; null for signals that never expire (e.g. manual)."`
	CreatedAt   int64           `json:"createdAt"`
}

// SignalCreateInputBody is the JSON body of POST /signals. SubjectType and
// SubjectID were added in ADR 0008 D1; when omitted, the handler resolves
// SubjectType from the kind's signal_kinds/*.yaml SubjectTypeDefault.
//
// TaskID is the legacy fast path equivalent to (SubjectType="task",
// SubjectID=<task_public_id>). When both forms are supplied, they must
// reference the same task; otherwise the handler returns
// WS.SIGNAL.SUBJECT_MISMATCH.
type SignalCreateInputBody struct {
	WorkspaceID string          `json:"workspaceId" required:"true" doc:"Workspace public id (UUID v7)"`
	Source      string          `json:"source" required:"true" enum:"manual,github,slack,email,google,webhook,calendar,discord"`
	Kind        string          `json:"kind" required:"true" minLength:"1" maxLength:"255" doc:"Signal kind from signal_kinds/*.yaml registry (e.g. discord.presence, manual). Unknown kinds are rejected with WS.SIGNAL.KIND_UNKNOWN."`
	ExternalID  string          `json:"externalId,omitempty" maxLength:"255"`
	TaskID      string          `json:"taskId,omitempty" doc:"Legacy fast path equivalent to (subjectType='task', subjectId=<task public id>). When both forms are supplied they must point at the same task."`
	SubjectType string          `json:"subjectType,omitempty" enum:"user,task,workspace,calendar_event" doc:"What the signal is about. Defaults to the kind's SubjectTypeDefault from signal_kinds/*.yaml when omitted."`
	SubjectID   string          `json:"subjectId,omitempty" doc:"Subject row public id (UUID v7). Required when subjectType is user, task, or calendar_event; ignored when subjectType=workspace."`
	Payload     json.RawMessage `json:"payload,omitempty"`
	ExpiresAt   *int64          `json:"expiresAt,omitempty" doc:"Provider-derived TTL in unix seconds; omit for signals that never expire."`
}

// Compile-time field-parity assertion between the Huma input DTO and the
// shared wire body. Go permits a struct conversion only when both types
// have identical field names and types (struct tags are ignored), so
// these two conversions fail to compile the moment a field is added,
// removed, or retyped on either side — making the worker / presence-
// discord emitters (which serialise signalwire.CreateRequest) and the
// flow-api receiver structurally inseparable. The DTO keeps its own
// validation/enum tags so the OpenAPI document stays authoritative.
var (
	_ = func(b SignalCreateInputBody) signalwire.CreateRequest { return signalwire.CreateRequest(b) }
	_ = func(r signalwire.CreateRequest) SignalCreateInputBody { return SignalCreateInputBody(r) }
)

// CreateInput is the body for POST /signals.
type CreateInput struct {
	Body SignalCreateInputBody
}

// CreateOutput is the response for POST /signals.
type CreateOutput struct {
	Body Signal
}
