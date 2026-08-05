// Package audit provides a thin helper for appending workspace-scoped
// audit log entries via the sqlc-generated AppendAuditLog query.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"
	"time"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// userAgentMaxLen matches the audit_logs.user_agent column width. The
// value is clipped before storage so an oversized header cannot be used
// as a write-amplification vector.
const userAgentMaxLen = 512

// packIP normalizes a client IP string into the 16-byte packed form the
// audit_logs.ip_address VARBINARY(16) column expects. Both IPv4 and IPv6
// map to a fixed 16-byte representation via [net.IP.To16], so the value
// never overflows the column. Empty or unparseable input yields SQL NULL.
func packIP(ip string) sql.NullString {
	if ip == "" {
		return sql.NullString{}
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return sql.NullString{}
	}
	packed := parsed.To16()
	if packed == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(packed), Valid: true}
}

// truncateUserAgent clips an oversized User-Agent to the column width.
func truncateUserAgent(ua string) string {
	if len(ua) > userAgentMaxLen {
		return ua[:userAgentMaxLen]
	}
	return ua
}

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
	ipAddress := packIP(clientIP)

	userAgent := e.UserAgent
	if userAgent == "" {
		userAgent = authn.UserAgentFromContext(ctx)
	}
	uaValue := sql.NullString{}
	if userAgent != "" {
		uaValue = sql.NullString{String: truncateUserAgent(userAgent), Valid: true}
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
			slog.WarnContext(ctx, "audit: failed to append instance log", slog.String("action", e.Action), slog.String("err", err.Error()))
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
		slog.WarnContext(ctx, "audit: failed to append log", slog.String("action", e.Action), slog.String("err", err.Error()))
	}
}
