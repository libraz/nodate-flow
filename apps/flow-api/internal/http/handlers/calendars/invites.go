package calendars

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
	sharedtoken "github.com/libraz/nodate-flow/packages/go-shared/token"
)

// Default and maximum expiry windows for a freshly minted invite token.
// The caller may pass a custom window on create; values outside the range
// are clamped to these bounds so an attacker cannot request unbounded
// lifetimes.
const (
	defaultInviteExpiryHours = 168 // 7 days
	maxInviteExpiryHours     = 720 // 30 days
)

// --- Input/Output types ---

// CreateEventInviteInput is the body for POST .../attendees/{attendeeId}/invite.
// expiresInHours is optional and clamped to [1, 720]; 0/missing falls back
// to the 7-day default.
type CreateEventInviteInput struct {
	WsID       string `path:"wsId" doc:"Workspace public ID"`
	CalID      string `path:"calId" doc:"Calendar public ID"`
	EvtID      string `path:"evtId" doc:"Event public ID"`
	AttendeeID string `path:"attendeeId" doc:"Attendee public ID"`
	Body       struct {
		ExpiresInHours *int `json:"expiresInHours,omitempty" required:"false" minimum:"1" maximum:"720" doc:"Token lifetime in hours; default 168, cap 720"`
	}
}

// EventInviteCreateResponse carries the plaintext token exactly once. The
// caller is expected to drop it into the outbound email body and never
// persist it elsewhere — subsequent reads return metadata only.
type EventInviteCreateResponse struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expiresAt"`
}

// CreateEventInviteOutput wraps the create response for Huma.
type CreateEventInviteOutput struct {
	Body EventInviteCreateResponse
}

// AcceptEventInviteInput is the body for the unauthenticated accept
// endpoint. The token is the plaintext magic-link value; the RSVP value
// matches the calendar_event_attendees.rsvp enum minus "pending".
type AcceptEventInviteInput struct {
	Body struct {
		Token string `json:"token" minLength:"1" doc:"Plaintext magic-link token from the invite email"`
		Rsvp  string `json:"rsvp" enum:"accepted,declined,tentative" doc:"RSVP response to record for the attendee"`
	}
}

// AcceptEventInviteResponse is the minimum the accept-page UI needs to
// confirm "your RSVP has been recorded for <event>". Workspace/calendar
// IDs are intentionally omitted because the viewer is unauthenticated.
//
// EventTitle / EventStartAt / EventEndAt / CalendarName are populated
// when the existing query surface lets us enrich the row; otherwise the
// frontend renders a neutral confirmation ("Your RSVP has been
// recorded") without the event name. A follow-up sqlc query joining
// invites → events → calendars would let us always enrich; wiring that
// is tracked separately to keep this handler change scoped.
type AcceptEventInviteResponse struct {
	InviteID     string  `json:"inviteId"`
	EventID      *string `json:"eventId,omitempty"`
	EventTitle   *string `json:"eventTitle,omitempty"`
	EventStartAt *int64  `json:"eventStartAt,omitempty"`
	EventEndAt   *int64  `json:"eventEndAt,omitempty"`
	CalendarName *string `json:"calendarName,omitempty"`
	Rsvp         string  `json:"rsvp"`
}

// AcceptEventInviteOutput wraps the accept response for Huma.
type AcceptEventInviteOutput struct {
	Body AcceptEventInviteResponse
}

// RevokeEventInviteInput deletes a single invite row.
type RevokeEventInviteInput struct {
	WsID     string `path:"wsId" doc:"Workspace public ID"`
	CalID    string `path:"calId" doc:"Calendar public ID"`
	EvtID    string `path:"evtId" doc:"Event public ID"`
	InviteID string `path:"inviteId" doc:"Invite public ID"`
}

// RevokeEventInviteOutput is a 204-style confirmation envelope. We use a
// Huma-friendly JSON body instead of no-content so clients that expect a
// consistent shape (SDK type codegen) still get a non-empty payload.
type RevokeEventInviteOutput struct {
	Body struct {
		Revoked bool `json:"revoked"`
	}
}

// InviteSummaryResponse is the editor-facing projection of an invite row.
// The token hash is deliberately omitted — revealing it would weaken the
// one-time-reveal guarantee of the plaintext token.
type InviteSummaryResponse struct {
	ID               string `json:"id"`
	AttendeePublicID string `json:"attendeePublicId"`
	Email            string `json:"email"`
	ExpiresAt        int64  `json:"expiresAt"`
	SentAt           *int64 `json:"sentAt,omitempty"`
	AcceptedAt       *int64 `json:"acceptedAt,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
}

// ListEventInvitesInput lists every active invite for a single event.
type ListEventInvitesInput struct {
	WsID   string `path:"wsId" doc:"Workspace public ID"`
	CalID  string `path:"calId" doc:"Calendar public ID"`
	EvtID  string `path:"evtId" doc:"Event public ID"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListEventInvitesOutput is the response shape for the per-event invite
// list. Keyed by the plural resource name to match repo conventions.
type ListEventInvitesOutput struct {
	Body struct {
		Total   int64                   `json:"total" doc:"Total invites on the event before paging"`
		Invites []InviteSummaryResponse `json:"invites"`
	}
}

// --- Handlers ---

// CreateEventInvite mints (or rotates) a magic-link invite for a single
// attendee on a calendar event. Only the event owner may call this: the
// invite grants RSVP-only authority, but rotating it invalidates any
// previously distributed link so only the owner should have that power.
//
// When an active invite already exists for the (event, attendee) pair
// (enforced by the UNIQUE constraint in calendar_event_invites), the
// token is rotated in place and the response carries the new plaintext
// token. Audit type is "calendar.event.invite.rotated" in that case,
// ".created" otherwise.
func CreateEventInvite(deps Deps) func(context.Context, *CreateEventInviteInput) (*CreateEventInviteOutput, error) {
	return func(ctx context.Context, input *CreateEventInviteInput) (*CreateEventInviteOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendarWrite(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}
		evt, err := resolveEvent(ctx, deps.CalendarQueries, cal.ID, wsID, input.EvtID)
		if err != nil {
			return nil, err
		}

		// Owner gate. Attendee edit rights are irrelevant here: invite
		// lifecycle is owner-only.
		if evt.OwnerUserID != actorID {
			return nil, httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
		}

		// Resolve the attendee row by its public ID. We don't have a
		// direct (publicID → attendee) query, so we list active
		// attendees for the event and match locally. This keeps the
		// scope of this change inside the handler layer.
		attendeePID, err := parsePublicID(input.AttendeeID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttendeeUserNotFound)
		}
		attendees, err := deps.CalendarQueries.ListCalendarEventAttendees(ctx, calendar.ListCalendarEventAttendeesParams{
			EventID:     handlerutil.NullInt32From(evt.ID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteStoreLookupInterrupted)
		}
		var matched *calendar.ListCalendarEventAttendeesRow
		for i := range attendees {
			if attendees[i].PublicID == attendeePID {
				matched = &attendees[i]
				break
			}
		}
		if matched == nil {
			return nil, httpErr(apierrors.CalendarAttendeeUserNotFound)
		}

		// Fetch the attendee user's email; denormalize onto the invite
		// row so the inbox query can hit it without another JOIN.
		profile, err := deps.Queries.FindUserProfileById(ctx, matched.UserID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteStoreLookupInterrupted)
		}

		token, tokenHash, err := mintInviteToken()
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteTokenGenerateInterrupted)
		}

		// Clamp expiry into [1h, 720h] with a 168h default when absent.
		hours := defaultInviteExpiryHours
		if input.Body.ExpiresInHours != nil {
			hours = *input.Body.ExpiresInHours
			if hours < 1 {
				hours = 1
			}
			if hours > maxInviteExpiryHours {
				hours = maxInviteExpiryHours
			}
		}
		expiresAt := time.Now().UTC().Add(time.Duration(hours) * time.Hour)

		// Find the attendee's internal id so we can look up an existing
		// invite (UNIQUE on (event_id, attendee_id)). The list query
		// does not surface the internal id, so go through the direct
		// per-user lookup.
		attRow, err := deps.CalendarQueries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
			EventID: handlerutil.NullInt32From(evt.ID),
			UserID:  matched.UserID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteStoreLookupInterrupted)
		}

		existing, err := deps.CalendarQueries.FindCalendarEventInviteForAttendee(ctx, calendar.FindCalendarEventInviteForAttendeeParams{
			EventID:    handlerutil.NullInt32From(evt.ID),
			AttendeeID: handlerutil.NullInt32From(attRow.ID),
		})
		rotated := false
		var invitePublicID types.PublicID
		var inviteInternalID uint32
		switch {
		case err == nil:
			// A row already exists for this (event, attendee), live or
			// revoked, and UNIQUE(event_id, attendee_id) says there is
			// only ever the one. Revive it with a fresh token rather
			// than inserting beside it: the previous behaviour looked
			// only at live rows, so re-inviting someone after a revoke
			// went to the insert below, collided with the revoked row
			// and failed for good. The response still carries the new
			// plaintext token exactly once either way.
			if rerr := deps.CalendarQueries.ReviveCalendarEventInvite(ctx, calendar.ReviveCalendarEventInviteParams{
				TokenHash: tokenHash,
				ExpiresAt: expiresAt,
				ID:        existing.ID,
			}); rerr != nil {
				return nil, httpErr(apierrors.CalendarInviteStoreWriteInterrupted)
			}
			rotated = true
			invitePublicID = existing.PublicID
			inviteInternalID = existing.ID
		case errors.Is(err, sql.ErrNoRows):
			invitePublicID = types.New()
			res, cerr := deps.CalendarQueries.CreateCalendarEventInvite(ctx, calendar.CreateCalendarEventInviteParams{
				PublicID:    invitePublicID,
				WorkspaceID: wsID,
				CalendarID:  cal.ID,
				EventID:     handlerutil.NullInt32From(evt.ID),
				AttendeeID:  handlerutil.NullInt32From(attRow.ID),
				Email:       profile.Email,
				TokenHash:   tokenHash,
				ExpiresAt:   expiresAt,
			})
			if cerr != nil {
				if handlerutil.IsDuplicateEntry(cerr) {
					existing, rerr := deps.CalendarQueries.FindCalendarEventInviteForAttendee(ctx, calendar.FindCalendarEventInviteForAttendeeParams{
						EventID:    handlerutil.NullInt32From(evt.ID),
						AttendeeID: handlerutil.NullInt32From(attRow.ID),
					})
					if rerr == nil {
						if rerr = deps.CalendarQueries.ReviveCalendarEventInvite(ctx, calendar.ReviveCalendarEventInviteParams{
							TokenHash: tokenHash,
							ExpiresAt: expiresAt,
							ID:        existing.ID,
						}); rerr != nil {
							return nil, httpErr(apierrors.CalendarInviteStoreWriteInterrupted)
						}
						rotated = true
						invitePublicID = existing.PublicID
						inviteInternalID = existing.ID
						break
					}
				}
				return nil, httpErr(apierrors.CalendarInviteStoreWriteInterrupted)
			}
			if id, idErr := res.LastInsertId(); idErr == nil && id > 0 {
				inviteInternalID = uint32(id) //#nosec G115 -- LastInsertId for calendar_event_invites.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
			}
		default:
			return nil, httpErr(apierrors.CalendarInviteStoreLookupInterrupted)
		}

		eventType := "calendar.event.invite.created"
		auditAction := "calendar.invite.create"
		if rotated {
			eventType = "calendar.event.invite.rotated"
			auditAction = "calendar.invite.rotate"
		}
		_ = appendCalendarEvent(ctx, deps.DB, wsID, cal.ID, eventType, &actorID, map[string]any{
			"eventId":          input.EvtID,
			"calendarId":       input.CalID,
			"attendeePublicId": input.AttendeeID,
			"invitePublicId":   invitePublicID.String(),
		})

		deps.Audit.Record(ctx, audit.Entry{
			Action:       auditAction,
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.invite",
			ResourceID:   invitePublicID.String(),
			Metadata: map[string]any{
				"calendarId":       input.CalID,
				"eventId":          input.EvtID,
				"attendeePublicId": input.AttendeeID,
			},
		})

		// Dispatch the magic-link email. Best-effort: a delivery
		// failure never fails the API response — the plaintext token
		// is still returned to the caller for manual retrieval. Only
		// stamp sent_at when the transport confirmed acceptance.
		dispatchInviteEmail(ctx, deps, inviteInternalID, profile.Email, token, expiresAt, evt, cal)

		out := &CreateEventInviteOutput{}
		out.Body = EventInviteCreateResponse{
			ID:        invitePublicID.String(),
			Token:     token,
			ExpiresAt: expiresAt.Unix(),
		}
		return out, nil
	}
}

// dispatchInviteEmail sends the magic-link invite to the attendee and,
// on success, marks the invite row as sent. Failures are logged but
// never propagated — the caller still receives the plaintext token in
// the HTTP response so the invite can be delivered out-of-band.
//
// [email.ErrNotConfigured] is demoted to Info-level because it is the
// expected dev/stub case when NF_FLOW_SMTP_HOST is unset; every other
// error is logged at Error to surface real delivery problems without
// failing the request.
func dispatchInviteEmail(
	ctx context.Context,
	deps Deps,
	inviteInternalID uint32,
	attendeeEmail string,
	token string,
	expiresAt time.Time,
	evt calendar.FindCalendarEventByPublicIdRow,
	cal calendar.FindCalendarByPublicIdRow,
) {
	if deps.EmailSender == nil || attendeeEmail == "" {
		return
	}

	base := deps.FlowWebURL
	if base == "" {
		base = "http://localhost:5173"
	}
	acceptURL := base + "/invites/accept?token=" + token

	body := buildInviteEmailBody(evt, cal, acceptURL, expiresAt)

	msg := email.Message{
		From:    deps.EmailFrom,
		To:      []string{attendeeEmail},
		Subject: "You're invited to: " + evt.Title,
		Body:    body,
	}

	if err := deps.EmailSender.Send(ctx, msg); err != nil {
		if errors.Is(err, email.ErrNotConfigured) {
			slog.InfoContext(ctx, "invite email dispatch skipped - sender not configured",
				"eventId", evt.PublicID.String(),
			)
			return
		}
		slog.ErrorContext(ctx, "invite email dispatch failed",
			"eventId", evt.PublicID.String(),
			"err", err,
		)
		return
	}

	if inviteInternalID == 0 {
		// No internal id means we cannot mark the row as sent; the
		// rest of the flow still succeeded so we just skip the stamp.
		return
	}
	if merr := deps.CalendarQueries.MarkCalendarEventInviteSent(ctx, inviteInternalID); merr != nil {
		slog.ErrorContext(ctx, "invite email mark-sent failed",
			"inviteId", inviteInternalID,
			"err", merr,
		)
	}
}

// buildInviteEmailBody renders the plain-text body of the invite
// email. The content is intentionally English-only for this iteration —
// localisation hooks will land in a follow-up pass.
func buildInviteEmailBody(
	evt calendar.FindCalendarEventByPublicIdRow,
	cal calendar.FindCalendarByPublicIdRow,
	acceptURL string,
	expiresAt time.Time,
) string {
	var b strings.Builder
	b.WriteString("You've been invited to the following event:\n\n")
	b.WriteString("Event: ")
	b.WriteString(evt.Title)
	b.WriteString("\n")
	b.WriteString("Calendar: ")
	b.WriteString(cal.Name)
	b.WriteString("\n")
	if evt.StartAt.Valid {
		b.WriteString("Starts: ")
		b.WriteString(evt.StartAt.Time.UTC().Format(time.RFC3339))
		b.WriteString("\n")
	}
	if evt.EndAt.Valid {
		b.WriteString("Ends: ")
		b.WriteString(evt.EndAt.Time.UTC().Format(time.RFC3339))
		b.WriteString("\n")
	}
	b.WriteString("\nOpen this link to respond (link expires ")
	b.WriteString(expiresAt.UTC().Format(time.RFC3339))
	b.WriteString("):\n\n")
	b.WriteString(acceptURL)
	b.WriteString("\n")
	return b.String()
}

// AcceptEventInvite consumes a magic-link token and stamps the invite
// accepted_at + attendee RSVP. Unauthenticated: the token is the
// capability. Expired tokens collapse into the same NOT_FOUND response
// as missing tokens so we don't leak lifecycle state to scanners.
//
// Idempotent on RSVP: a recipient can change their mind (decline then
// accept) as long as the token has not expired and the invite row is
// still enabled.
func AcceptEventInvite(deps Deps) func(context.Context, *AcceptEventInviteInput) (*AcceptEventInviteOutput, error) {
	return func(ctx context.Context, input *AcceptEventInviteInput) (*AcceptEventInviteOutput, error) {
		if input.Body.Token == "" {
			return nil, httpErr(apierrors.CalendarInviteNotFound)
		}
		sum := sha256.Sum256([]byte(input.Body.Token))
		invite, err := deps.CalendarQueries.FindCalendarEventInviteByTokenHash(ctx, sum[:])
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarInviteNotFound, apierrors.CalendarInviteStoreLookupInterrupted))
		}
		if !invite.ExpiresAt.After(time.Now().UTC()) {
			// Expired invites are surfaced as NOT_FOUND per the code
			// spec so an attacker cannot distinguish "never existed"
			// from "expired".
			return nil, httpErr(apierrors.CalendarInviteNotFound)
		}

		rsvp := calendar.CalendarEventAttendeesRsvp(input.Body.Rsvp)

		// Resolve attendee → user_id so we can target the RSVP update.
		// ListCalendarEventAttendees is the cheapest available path
		// since the schema doesn't expose a (attendee_id → user_id)
		// lookup directly.
		attendees, err := deps.CalendarQueries.ListCalendarEventAttendees(ctx, calendar.ListCalendarEventAttendeesParams{
			EventID:     invite.EventID,
			WorkspaceID: invite.WorkspaceID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteStoreLookupInterrupted)
		}
		var targetUserID uint32
		var attendeeMatched bool
		for _, a := range attendees {
			// AttendeeID is internal; we can't compare directly without
			// fetching each attendee's internal id. Fall back to a
			// per-user lookup against the attendee row (event_id,
			// user_id) and compare the returned internal id.
			row, lerr := deps.CalendarQueries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
				EventID: invite.EventID,
				UserID:  a.UserID,
			})
			if lerr != nil {
				continue
			}
			if row.ID == handlerutil.Int32ToUint32(invite.AttendeeID) {
				targetUserID = a.UserID
				attendeeMatched = true
				break
			}
		}
		if !attendeeMatched {
			return nil, httpErr(apierrors.CalendarInviteNotFound)
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteStoreWriteInterrupted)
		}
		defer func() { _ = tx.Rollback() }()
		txCalendarQueries := calendar.New(tx)

		// Stamp the invite accepted even if the RSVP is "declined" — the
		// invite is considered consumed once the recipient interacts
		// with it. The caller can still flip RSVP later while the token
		// is valid. Keep this and the RSVP write in one transaction so a
		// later attendee update failure cannot leave accepted_at stamped
		// without the visible RSVP change.
		if err := txCalendarQueries.MarkCalendarEventInviteAccepted(ctx, invite.ID); err != nil {
			return nil, httpErr(apierrors.CalendarInviteStoreWriteInterrupted)
		}
		if err := txCalendarQueries.UpdateAttendeeRsvp(ctx, calendar.UpdateAttendeeRsvpParams{
			Rsvp:    rsvp,
			EventID: invite.EventID,
			UserID:  targetUserID,
		}); err != nil {
			return nil, httpErr(apierrors.CalendarAttendeeRsvpUpdateInterrupted)
		}
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.CalendarInviteStoreWriteInterrupted)
		}

		// The confirmation payload is intentionally minimal: the
		// invite's public ID and the RSVP that was just recorded. The
		// current sqlc surface does not expose a (invite.EventID,
		// invite.CalendarID) → (event title, start_at, end_at, calendar
		// name) lookup by internal id, and the unauthenticated accept
		// endpoint cannot safely synthesize one from path parameters.
		// The frontend renders "Your RSVP has been recorded" using
		// translation strings; enrichment is a follow-up once a JOIN
		// query lands.
		out := &AcceptEventInviteOutput{}
		out.Body.InviteID = invite.PublicID.String()
		out.Body.Rsvp = string(rsvp)

		// Unauthenticated magic-link accept: the actor is the anonymous
		// invite holder, so ActorID stays 0 (recorded as a NULL actor).
		// The invite row carries its own workspace_id so the entry is
		// still workspace-scoped and surfaces in v_workspace_activity.
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.invite.accept",
			WorkspaceID:  invite.WorkspaceID,
			ResourceType: "calendar.invite",
			ResourceID:   invite.PublicID.String(),
			Metadata: map[string]any{
				"rsvp": string(rsvp),
			},
		})

		slog.InfoContext(ctx, "calendar event invite accepted",
			"inviteId", invite.PublicID.String(),
			"rsvp", out.Body.Rsvp,
		)
		return out, nil
	}
}

// RevokeEventInvite soft-disables a single invite row. Owner-only, same
// ACL as create. Once disabled, the magic link returns NOT_FOUND.
func RevokeEventInvite(deps Deps) func(context.Context, *RevokeEventInviteInput) (*RevokeEventInviteOutput, error) {
	return func(ctx context.Context, input *RevokeEventInviteInput) (*RevokeEventInviteOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendarWrite(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}
		evt, err := resolveEvent(ctx, deps.CalendarQueries, cal.ID, wsID, input.EvtID)
		if err != nil {
			return nil, err
		}
		if evt.OwnerUserID != actorID {
			return nil, httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
		}
		invitePID, err := parsePublicID(input.InviteID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteNotFound)
		}
		invite, err := deps.CalendarQueries.FindCalendarEventInviteByPublicId(ctx, invitePID)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarInviteNotFound, apierrors.CalendarInviteStoreLookupInterrupted))
		}
		// Guard against cross-event revoke: the invite must belong to
		// the event path parameter the caller supplied. Without this
		// check an event owner could revoke invites for an event they
		// don't own simply by knowing a public ID.
		if handlerutil.Int32ToUint32(invite.EventID) != evt.ID {
			return nil, httpErr(apierrors.CalendarInviteNotFound)
		}
		if err := deps.CalendarQueries.DisableCalendarEventInvite(ctx, invite.ID); err != nil {
			return nil, httpErr(apierrors.CalendarInviteStoreRevokeInterrupted)
		}
		_ = appendCalendarEvent(ctx, deps.DB, wsID, cal.ID, "calendar.event.invite.revoked", &actorID, map[string]any{
			"eventId":        input.EvtID,
			"calendarId":     input.CalID,
			"invitePublicId": input.InviteID,
		})
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.invite.revoke",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.invite",
			ResourceID:   input.InviteID,
			Metadata: map[string]any{
				"calendarId": input.CalID,
				"eventId":    input.EvtID,
			},
		})
		out := &RevokeEventInviteOutput{}
		out.Body.Revoked = true
		return out, nil
	}
}

// ListEventInvites returns every active invite for an event. Owner-only
// so non-owners can't discover who was invited before they accept.
func ListEventInvites(deps Deps) func(context.Context, *ListEventInvitesInput) (*ListEventInvitesOutput, error) {
	return func(ctx context.Context, input *ListEventInvitesInput) (*ListEventInvitesOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}
		evt, err := resolveEvent(ctx, deps.CalendarQueries, cal.ID, wsID, input.EvtID)
		if err != nil {
			return nil, err
		}
		if evt.OwnerUserID != actorID {
			return nil, httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
		}
		page := handlerutil.Bind(input.Limit, input.Offset, handlerutil.DefaultListLimit, handlerutil.MaxListLimit)
		rows, err := deps.CalendarQueries.ListCalendarEventInvitesForEvent(ctx, calendar.ListCalendarEventInvitesForEventParams{
			EventID: handlerutil.NullInt32From(evt.ID),
			Limit:   page.Limit,
			Offset:  page.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteListQueryInterrupted)
		}

		// Build a (internal attendee id → public ID) index so we can
		// attach attendeePublicId to each invite without N round-trips.
		attendees, err := deps.CalendarQueries.ListCalendarEventAttendees(ctx, calendar.ListCalendarEventAttendeesParams{
			EventID:     handlerutil.NullInt32From(evt.ID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteListQueryInterrupted)
		}
		internalToPublic := make(map[uint32]types.PublicID, len(attendees))
		for _, a := range attendees {
			// Fetch the attendee's internal id via the per-user lookup;
			// the list query omits it. This is O(attendees) per list
			// call — acceptable given typical event attendee counts.
			row, lerr := deps.CalendarQueries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
				EventID: handlerutil.NullInt32From(evt.ID),
				UserID:  a.UserID,
			})
			if lerr != nil {
				continue
			}
			internalToPublic[row.ID] = a.PublicID
		}

		out := &ListEventInvitesOutput{}
		out.Body.Invites = make([]InviteSummaryResponse, 0, len(rows))
		for _, r := range rows {
			item := InviteSummaryResponse{
				ID:         r.PublicID.String(),
				Email:      r.Email,
				ExpiresAt:  r.ExpiresAt.Unix(),
				SentAt:     nullTimeUnixPtr(r.SentAt),
				AcceptedAt: nullTimeUnixPtr(r.AcceptedAt),
				CreatedAt:  r.CreatedAt.Unix(),
			}
			if pid, ok := internalToPublic[handlerutil.Int32ToUint32(r.AttendeeID)]; ok {
				item.AttendeePublicID = pid.String()
			}
			out.Body.Invites = append(out.Body.Invites, item)
		}
		if len(rows) > 0 {
			out.Body.Total = handlerutil.TotalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// --- Helpers ---

// mintInviteToken delegates to the shared token package and converts
// the hex-encoded hash to the raw-byte form expected by
// calendar_event_invites.token_hash (BINARY(32)). The plaintext is
// only surfaced once; subsequent reads hash the incoming token via
// HashInviteToken and look the result up in the column.
func mintInviteToken() (string, []byte, error) {
	raw, hashHex, err := sharedtoken.MintToken()
	if err != nil {
		return "", nil, err
	}
	hashBytes, err := hex.DecodeString(hashHex)
	if err != nil {
		return "", nil, err
	}
	return raw, hashBytes, nil
}
