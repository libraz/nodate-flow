package calendars

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	generated "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
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
	// Mutations records every change these handlers make, in both the
	// event log the timeline reads and the audit log an administrator
	// queries by action name. It replaces a bare audit recorder so
	// neither half can be written without the other; mutation_static_test.go
	// is what keeps a later handler from reaching around it.
	Mutations *mutationlog.Recorder
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

// calendarMutationActor is the [mutationlog.Actor] both halves of a
// record are stamped with. actorUserID zero means there is no
// authenticated user behind the change — the unauthenticated magic-link
// accept — and both rows then carry a NULL actor rather than a
// fabricated one.
func calendarMutationActor(workspaceID, actorUserID uint32) mutationlog.Actor {
	return mutationlog.Actor{UserID: actorUserID, WorkspaceID: workspaceID}
}

// calendarMutationCalendar maps a calendar id onto the nullable column
// the event row carries, so [noOwningCalendar] stays the one spelling of
// "this change belongs to no single calendar".
func calendarMutationCalendar(calendarID uint32) *int64 {
	if calendarID == noOwningCalendar {
		return nil
	}
	v := int64(calendarID)
	return &v
}

// recordCalendarChange is the adapter that translates the calendar
// handlers' call shape into the mutation log's. One call writes both the
// `events` row the timeline, notifications and SSE feeds read and the
// `audit_logs` row an administrator queries by action name; neither can
// be written here without the other.
//
// calendarID is a required parameter rather than a field of m because
// events.calendar_id, its index and its foreign key all existed while
// every INSERT left the column NULL, so the per-calendar activity feed
// the schema was shaped for could not be built. A new emitter therefore
// cannot compile without deciding which calendar its change belongs to,
// and the deliberate exceptions say so by naming [noOwningCalendar].
//
// The rest of what the change is travels in m, written out at the call
// site. The audit action had the matching hole — membership, checklist,
// comment, attendee, attachment and subscription changes appended an
// event and left no audit row at all — and what holds it closed is
// mutation_static_test.go, which reads every literal handed to this
// function and fails one that names no action, resource or call site. It
// is a literal rather than more parameters because the cross-transport
// trail check reads the action off exactly this shape: behind a string
// parameter the action becomes unreadable, and the MCP tool performing
// the same change has nothing left to be compared against.
//
// It records best effort and returns nothing. Every caller runs after
// the calendar row it describes is already committed on its own
// connection, so failing the request would report "nothing happened" for
// work that did, and the client's retry would duplicate it.
func recordCalendarChange(
	ctx context.Context,
	deps Deps,
	workspaceID, calendarID, actorUserID uint32,
	m mutationlog.Mutation,
) {
	m.CalendarID = calendarMutationCalendar(calendarID)
	deps.Mutations.Record(ctx, calendarMutationActor(workspaceID, actorUserID), m)
}

// recordCalendarAudit is [recordCalendarChange] for a change whose event
// row a shared transactional helper already appended inside the same
// transaction as the write itself. Appending it again here would put the
// change on the timeline twice, so this records the audit half only —
// the half those helpers cannot supply, because they are shared across
// transports and know nothing about which one called them.
//
// It takes no calendar id: nothing is appended to `events`, so there is
// no column for one to reach. Every call site names the helper that owns
// its event in a comment, so a reader can find the row this one
// deliberately does not write.
func recordCalendarAudit(
	ctx context.Context,
	deps Deps,
	workspaceID, actorUserID uint32,
	m mutationlog.Mutation,
) {
	deps.Mutations.RecordTxAudit(ctx, calendarMutationActor(workspaceID, actorUserID), m)
}
