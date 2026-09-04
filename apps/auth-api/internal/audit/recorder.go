// Package audit provides a thin helper for appending workspace-scoped
// audit log entries via the sqlc-generated AppendAuditLog query.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
)

// userAgentMaxLen matches the audit_logs.user_agent column width. The
// value is clipped before storage so an oversized header cannot be used
// as a write-amplification vector.
const userAgentMaxLen = 512

// Sink is the minimal contract used by handlers to append audit
// entries. The production implementation is *Recorder; tests inject
// fakes that capture the entries.
type Sink interface {
	Record(ctx context.Context, e Entry)
}

// NoopSink discards every entry. Useful in tests that do not assert
// on audit side effects but still need a non-nil dependency.
type NoopSink struct{}

// Record discards the entry.
func (NoopSink) Record(context.Context, Entry) {}

// Recorder appends audit log entries to the audit_logs table.
// A nil *Recorder is safe to use; all methods become no-ops so callers
// never need nil guards.
type Recorder struct {
	q *generated.Queries
}

// New creates a Recorder backed by the given sqlc Queries instance.
func New(q *generated.Queries) *Recorder {
	return &Recorder{q: q}
}

// Entry holds the data for a single audit log row. Fields mirror the
// audit_logs table columns that the caller is responsible for; the
// recorder fills in public_id and occurred_at automatically.
type Entry struct {
	// Action is a dot-separated identifier like "auth.login" or "task.create".
	Action string
	// ActorID is the internal user id of the actor. Zero means system/anonymous.
	ActorID uint32
	// WorkspaceID is the internal workspace id.
	WorkspaceID uint32
	// ResourceType identifies the kind of resource affected (e.g. "task", "project").
	ResourceType string
	// ResourceID is the public UUID string of the affected resource. Empty is allowed.
	ResourceID string
	// IPAddress is the caller's client IP. When empty the recorder falls
	// back to the value stashed on the request context by the ClientIP
	// middleware. It is packed to VARBINARY(16) before storage.
	IPAddress string
	// UserAgent is the caller's User-Agent header. When empty the recorder
	// falls back to the value stashed on the request context.
	UserAgent string
	// Metadata carries additional context. Values must be JSON-safe and
	// pre-redacted (no secrets). Nil is fine.
	Metadata map[string]any
}

// Record appends an audit log entry. Errors are logged but not returned
// so audit failures never block the primary operation.
func (r *Recorder) Record(ctx context.Context, e Entry) {
	if r == nil {
		return
	}

	var metaJSON json.RawMessage
	if len(e.Metadata) > 0 {
		b, err := json.Marshal(e.Metadata)
		if err != nil {
			slog.WarnContext(ctx, "audit: failed to marshal metadata", slog.String("action", e.Action), slog.String("err", err.Error()))
			metaJSON = []byte("{}")
		} else {
			metaJSON = b
		}
	}

	actorID := sql.NullInt32{}
	if e.ActorID > 0 {
		actorID = sql.NullInt32{Int32: int32(e.ActorID), Valid: true} //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
	}

	var resourcePublicID types.PublicID
	if e.ResourceID != "" {
		if parsed, perr := types.Parse(e.ResourceID); perr == nil {
			resourcePublicID = parsed
		}
	}

	// Resolve the client IP / User-Agent, preferring an explicit Entry
	// override and falling back to the request context populated by the
	// ClientIP middleware. The IP is packed to the VARBINARY(16) form.
	clientIP := e.IPAddress
	if clientIP == "" {
		clientIP = authn.ClientIPFromContext(ctx)
	}
	ipAddress := dbtype.NullStringFromIP(clientIP)

	userAgent := e.UserAgent
	if userAgent == "" {
		userAgent = authn.UserAgentFromContext(ctx)
	}
	uaValue := sql.NullString{}
	if userAgent != "" {
		uaValue = sql.NullString{String: stringutil.TruncateBytes(userAgent, userAgentMaxLen), Valid: true}
	}

	now := time.Now()

	// Workspace-scoped entries go to audit_logs; entries without a
	// workspace (e.g. auth.login before workspace resolution) go to
	// instance_audit_logs so the FK on workspace_id is never violated.
	if e.WorkspaceID == 0 {
		_, err := r.q.AppendInstanceAuditLog(ctx, generated.AppendInstanceAuditLogParams{
			PublicID:               types.New(),
			ActorUserID:            actorID,
			Action:                 e.Action,
			TargetResourceType:     sql.NullString{String: e.ResourceType, Valid: e.ResourceType != ""},
			TargetResourcePublicID: resourcePublicID,
			IpAddress:              ipAddress,
			UserAgent:              uaValue,
			PayloadJson:            metaJSON,
			OccurredAt:             now,
		})
		if err != nil {
			recordLoss(ctx, auditTableInstance, e.Action, err)
		}
		return
	}

	_, err := r.q.AppendAuditLog(ctx, generated.AppendAuditLogParams{
		PublicID:         types.New(),
		WorkspaceID:      e.WorkspaceID,
		ActorUserID:      actorID,
		Action:           e.Action,
		ResourceType:     e.ResourceType,
		ResourcePublicID: resourcePublicID,
		IpAddress:        ipAddress,
		UserAgent:        uaValue,
		MetadataJson:     metaJSON,
		OccurredAt:       now,
	})
	if err != nil {
		recordLoss(ctx, auditTableWorkspace, e.Action, err)
	}
}

// Audit destination table labels, shared by the log lines below and by
// whatever counts them.
const (
	auditTableWorkspace = "audit_logs"
	auditTableInstance  = "instance_audit_logs"
)

// recordLoss reports an audit row that was built but never stored.
//
// The caller is not failed: an audit backend problem must not turn into
// a service outage. That decision is what makes the loss silent, so it
// is logged at error level, because the request it belongs to has
// already been answered 2xx and nothing else will ever mention it.
//
// This service exposes no metrics endpoint, so the log line is the only
// signal; the flow-api recorder counts the same event on
// nf_audit_write_failures_total.
func recordLoss(ctx context.Context, table, action string, err error) {
	slog.ErrorContext(ctx, "audit: entry lost, write failed",
		slog.String("table", table),
		slog.String("action", action),
		slog.String("err", err.Error()))
}
