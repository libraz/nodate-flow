// Package audit provides a thin helper for appending workspace-scoped
// audit log entries via the sqlc-generated AppendAuditLog query.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
)

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
		actorID = sql.NullInt32{Int32: int32(e.ActorID), Valid: true}
	}

	resourcePublicID := sql.NullString{}
	if e.ResourceID != "" {
		resourcePublicID = sql.NullString{String: e.ResourceID, Valid: true}
	}

	_, err := r.q.AppendAuditLog(ctx, generated.AppendAuditLogParams{
		PublicID:         types.New(),
		WorkspaceID:      e.WorkspaceID,
		ActorUserID:      actorID,
		Action:           e.Action,
		ResourceType:     e.ResourceType,
		ResourcePublicID: resourcePublicID,
		MetadataJson:     metaJSON,
		OccurredAt:       time.Now(),
	})
	if err != nil {
		slog.WarnContext(ctx, "audit: failed to append log", slog.String("action", e.Action), slog.String("err", err.Error()))
	}
}
