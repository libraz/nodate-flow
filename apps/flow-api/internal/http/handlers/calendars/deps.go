package calendars

import (
	"context"
	"database/sql"

	generated "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/storage"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
)

// Deps holds the dependencies required by calendar handlers.
type Deps struct {
	Queries *generated.Queries
	// CalendarQueries is the dedicated sqlc subpackage handle that emits
	// every calendar-domain query. Calendar handlers reach for it instead
	// of [generated.Queries] for any *_calendar_*, *_event_*, *_invite_*,
	// *_attendee_*, *_memo_* operation.
	CalendarQueries *calendar.Queries
	DB              *sql.DB
	// EmailSender dispatches transactional emails (e.g. event-invite
	// magic links). Nil-safe: when unset, handlers fall back to
	// [email.NoopSender] so the invite row is still created but no
	// message is delivered. This matches the auth-api "always succeed"
	// contract.
	EmailSender email.Sender
	// EmailFrom is the envelope sender address used when a [email.Message]
	// does not specify one. Sourced from NF_FLOW_SMTP_FROM.
	EmailFrom string
	// FlowWebURL is the origin of the flow-web frontend that hosts the
	// public /invites/accept RSVP page, used to build magic-link URLs
	// for outbound invite emails. Sourced from NF_FLOW_WEB_URL; defaults
	// to http://localhost:5173 in development.
	FlowWebURL string
	// Storage is the S3-compatible object store client for event
	// attachment uploads / downloads. Optional: nil makes the
	// presign / download endpoints return INTERNAL.STORAGE.NOT_CONFIGURED.
	Storage *storage.Client
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// int64Ptr returns a pointer to an int64 value.
func int64Ptr(v int64) *int64 {
	return &v
}

// nullTimeUnixValue returns the unix seconds of a nullable time, or 0 when
// the time is NULL. Used to keep DTO fields stable while start_at / end_at
// are nullable in the schema.
func nullTimeUnixValue(t sql.NullTime) int64 {
	if !t.Valid {
		return 0
	}
	return t.Time.Unix()
}

// nullTimeUnixPtr maps a nullable time to *int64 so DTOs with omitempty
// drop the field entirely when the DB column is NULL (undated event).
func nullTimeUnixPtr(t sql.NullTime) *int64 {
	if !t.Valid {
		return nil
	}
	v := t.Time.Unix()
	return &v
}

// appendCalendarEvent is the adapter that translates the calendar handlers'
// legacy eventbus call shape into flow-api's eventbus.Event API. The
// legacy callers pass workspaceID + eventType + actorUserID + payload
// directly; flow-api's eventbus.Append takes a structured Event with
// optional nullable fields. Keeping the wrapper here lets the relocated
// handler bodies stay byte-identical to their pre-merge form.
func appendCalendarEvent(ctx context.Context, db eventbus.DBTX, workspaceID uint32, eventType string, actorUserID *uint32, payload map[string]any) error {
	var actor *int64
	if actorUserID != nil {
		v := int64(*actorUserID)
		actor = &v
	}
	return eventbus.Append(ctx, db, eventbus.Event{
		Type:        eventType,
		WorkspaceID: workspaceID,
		ActorUserID: actor,
		Payload:     payload,
	})
}
