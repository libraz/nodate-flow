package calendars

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	generated "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
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
	// Audit appends workspace-scoped audit log entries for calendar
	// mutations so calendar activity surfaces in v_workspace_activity
	// alongside the other mutation domains. Nil-safe: a nil recorder
	// makes every Record call a no-op. Recording is best-effort and
	// never fails the primary operation.
	Audit *audit.Recorder
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
	// Embedder refreshes the search embedding of a task whose title moved
	// because the event projecting it was renamed. Nil-safe: a deployment
	// with no embedding provider writes no embeddings, and the rename is
	// still a complete rename.
	Embedder *embed.Client
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

// noOwningCalendar is the calendarID a caller passes when the event
// genuinely belongs to no single calendar. A public share aggregates
// events drawn from several calendars, so its own lifecycle -- created,
// rotated, deleted -- is a workspace fact rather than a calendar one,
// and events.calendar_id stays NULL for those rows.
//
// It exists so that "no calendar" has to be written down. The parameter
// is required precisely so nobody can leave the column unset by
// omission, which is how it came to be unwritten everywhere; a named
// constant keeps the deliberate exceptions greppable.
const noOwningCalendar uint32 = 0

// appendCalendarEvent is the adapter that translates the calendar handlers'
// call shape into flow-api's eventbus.Event API. The callers pass
// workspaceID + calendarID + eventType + actorUserID + payload directly;
// flow-api's eventbus.Append takes a structured Event with optional
// nullable fields.
//
// calendarID is a required parameter rather than an optional field
// because every event these handlers emit happens inside exactly one
// calendar, and the column that records which one had never been written
// by anything: events.calendar_id, its index and its foreign key all
// existed while every INSERT left it NULL, so the per-calendar activity
// feed the schema was shaped for could not be built. Making it a
// parameter is what keeps that from being true again — a new emitter
// cannot compile without deciding which calendar it belongs to.
//
// It appends best effort and returns nothing. Every caller runs after the
// calendar row it describes is already committed on its own connection,
// so failing the request would report "nothing happened" for work that
// did, and the client's retry would duplicate it. Returning an error
// there only offered the caller a choice it never took: all of them
// discarded it, which left a dropped event with no record of what it
// would have said. callSite names the operation in that record, e.g.
// "calendars.CreateEvent".
func appendCalendarEvent(ctx context.Context, db dbretry.CommitBoundary, workspaceID, calendarID uint32, eventType eventbus.Kind, actorUserID *uint32, payload map[string]any, callSite string) {
	var actor *int64
	if actorUserID != nil {
		v := int64(*actorUserID)
		actor = &v
	}
	var cal *int64
	if calendarID != noOwningCalendar {
		v := int64(calendarID)
		cal = &v
	}
	eventbus.AppendBestEffort(ctx, db, eventbus.Event{
		Type:        eventType,
		WorkspaceID: workspaceID,
		CalendarID:  cal,
		ActorUserID: actor,
		Payload:     payload,
	}, callSite)
}
