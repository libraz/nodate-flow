package calendars

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/eventacl"
)

// --- Public render (unauthenticated) ---

// PublicShareRenderEvent is the externally visible event shape for the
// /share/cal/{token} render. It deliberately omits fields a public
// viewer should not see (attendees, owner, memos flagged private, task
// linkage). Recurrence data is passed through so the client renderer
// can expand instances.
//
// RecurrenceRule and RecurrenceExceptions are the decoded JSON values,
// the same shape EventResponse and the cross-calendar list return. They
// used to be strings here — the stored JSON handed through as an opaque
// blob — so the same event read through the public page and through the
// authenticated API arrived as two different types, and the client grew
// two recurrence parsers that had already drifted apart. One wire shape
// is what lets one parser serve both.
//
// OverriddenStarts is spelled the same way it is on EventResponse and
// CrossCalendarEventResponse — a list of RFC 3339 UTC instants — for the
// same reason: the expander reads it and RecurrenceExceptions through one
// parser, and the public page runs the same expander the app does. What
// differs is its scope, not its shape: see overriddenStartsByShareEvent.
type PublicShareRenderEvent struct {
	ID             string           `json:"id"`
	Title          string           `json:"title"`
	StartAt        *int64           `json:"startAt,omitempty"`
	EndAt          *int64           `json:"endAt,omitempty"`
	AllDay         bool             `json:"allDay"`
	Timezone       string           `json:"timezone"`
	Location       *string          `json:"location,omitempty"`
	Memo           *string          `json:"memo,omitempty"`
	URL            *string          `json:"url,omitempty"`
	Kind           string           `json:"kind"`
	ShowAs         string           `json:"showAs"`
	Flexibility    string           `json:"flexibility"`
	BlockLabel     *string          `json:"blockLabel,omitempty"`
	RecurrenceRule *json.RawMessage `json:"recurrenceRule,omitempty"`
	RecurrenceEnd  *int64           `json:"recurrenceEnd,omitempty"`
	// Array of ISO 8601 dates/times to exclude from recurrence.
	RecurrenceExceptions *json.RawMessage `json:"recurrenceExceptions,omitempty"`
	OverriddenStarts     []string         `json:"overriddenStarts,omitempty" doc:"Occurrence starts an override row published on this same share already stands in for (RFC 3339 UTC). Recurring masters only."`
}

// PublicShareRenderPage is the workspace-facing metadata exposed on the
// public page. The workspace ID is included so the client can group
// events if multiple shares are ever rendered side-by-side.
type PublicShareRenderPage struct {
	Title               string  `json:"title"`
	Description         *string `json:"description,omitempty"`
	IconURL             *string `json:"iconUrl,omitempty"`
	CoverURL            *string `json:"coverUrl,omitempty"`
	Timezone            string  `json:"timezone"`
	ShowHolidaysCountry *string `json:"showHolidaysCountry,omitempty"`
	WorkspaceID         string  `json:"workspaceId"`
	WorkspaceName       string  `json:"workspaceName"`
	CreatedAt           int64   `json:"createdAt"`
}

// RenderPublicShareInput is keyed by the URL token (caller passes plaintext).
type RenderPublicShareInput struct {
	Token string `path:"token" doc:"Public share URL token"`
}

// RenderPublicShareOutput is the full public page payload.
type RenderPublicShareOutput struct {
	Body struct {
		Page   PublicShareRenderPage    `json:"page"`
		Events []PublicShareRenderEvent `json:"events"`
	}
}

// RenderPublicShare serves the unauthenticated /share/cal/{token} endpoint.
// Token shape and length are not prescribed here — the SHA-256 is computed
// over the raw path param, so any mismatch (including empty) lands in
// the token_invalid code path.
func RenderPublicShare(deps Deps) func(context.Context, *RenderPublicShareInput) (*RenderPublicShareOutput, error) {
	return func(ctx context.Context, input *RenderPublicShareInput) (*RenderPublicShareOutput, error) {
		if input.Token == "" {
			return nil, httpErr(apierrors.ShareShareTokenInvalid)
		}
		sum := sha256.Sum256([]byte(input.Token))
		hash := hex.EncodeToString(sum[:])

		page, err := deps.CalendarQueries.FindPublicShareByTokenHash(ctx, hash)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.ShareShareTokenInvalid, apierrors.CalendarCalendarStoreReadInterrupted))
		}
		if page.ExpiresAt.Valid && page.ExpiresAt.Time.Before(time.Now()) {
			return nil, httpErr(apierrors.ShareShareExpired)
		}

		events, err := deps.CalendarQueries.ListPublicShareEventsByTokenHash(ctx, hash)
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		out := &RenderPublicShareOutput{}
		out.Body.Page = PublicShareRenderPage{
			Title:               page.Title,
			Description:         nullStringPtr(page.Description),
			IconURL:             nullStringPtr(page.IconUrl),
			CoverURL:            nullStringPtr(page.CoverUrl),
			Timezone:            page.Timezone,
			ShowHolidaysCountry: nullStringPtr(page.ShowHolidaysCountry),
			WorkspaceID:         page.WorkspacePublicID.String(),
			WorkspaceName:       page.WorkspaceName,
			CreatedAt:           handlerutil.TimeToUnix(page.CreatedAt),
		}
		overridden := overriddenStartsByShareEvent(events)

		out.Body.Events = make([]PublicShareRenderEvent, len(events))
		for i, e := range events {
			ev := PublicShareRenderEvent{
				ID:             e.EventPublicID.String(),
				Title:          e.Title,
				StartAt:        nullTimeUnixPtr(e.StartAt),
				EndAt:          nullTimeUnixPtr(e.EndAt),
				AllDay:         e.AllDay,
				Timezone:       e.Timezone,
				Location:       nullStringPtr(e.Location),
				Memo:           nullStringPtr(e.Memo),
				URL:            nullStringPtr(e.Url),
				Kind:           string(e.Kind),
				ShowAs:         string(e.ShowAs),
				Flexibility:    string(e.Flexibility),
				BlockLabel:     nullStringPtr(e.BlockLabel),
				RecurrenceRule: rawMessagePtr(e.RecurrenceRule),
				RecurrenceEnd:  nullTimeUnixPtr(e.RecurrenceEnd),
				RecurrenceExceptions: rawMessagePtr(
					e.RecurrenceExceptions,
				),
				OverriddenStarts: overridden[e.EventID],
			}
			// `private`-visibility events honour a "time only" contract on
			// the public, unauthenticated page: the time block stays visible
			// (start/end, show_as, flexibility, block_label) but all
			// descriptive content is stripped so title/notes/location/url
			// never leak. `confidential` events are excluded entirely by the
			// render query.
			//
			// `default` resolves against the calendar's setting rather than
			// falling through as public. It is the column's own default, so
			// most events carry it, and reading it as public put the full
			// text of ordinary events on an unauthenticated URL — the one
			// place where guessing wrong is irreversible.
			if !eventacl.CanSeeDetails(
				eventacl.Event{
					Visibility:      eventacl.Visibility(e.Visibility),
					OwnerUserID:     0,
					CalendarDefault: eventacl.Visibility(e.CalendarDefaultVisibility),
				},
				eventacl.Actor{},
			) {
				stripPrivateEventDetails(&ev)
			}
			out.Body.Events[i] = ev
		}
		return out, nil
	}
}

// stripPrivateEventDetails enforces the "time only" contract for
// `private`-visibility events on the public render page. The time block
// stays observable (start/end, all-day, timezone, show_as, flexibility,
// block_label, recurrence) while every descriptive field that could leak
// content to an unauthenticated viewer is cleared: title, location, memo,
// and url.
//
// flexibility stays because it is an availability property rather than
// content: someone looking at the share to find a slot needs to know which
// blocks are movable, which is the whole reason the column exists.
func stripPrivateEventDetails(ev *PublicShareRenderEvent) {
	ev.Title = ""
	ev.Location = nil
	ev.Memo = nil
	ev.URL = nil
}

// rawMessagePtr hands a stored JSON column through to the response as
// JSON, so it is rendered as the object or array it is rather than as a
// string holding its own encoding.
//
// An absent column and the literal "null" COALESCE emits both answer
// nil, which omitempty then drops: a rule that is not there must not
// arrive as a JSON null the client has to distinguish from a missing
// field.
func rawMessagePtr(raw []byte) *json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	msg := json.RawMessage(raw)
	return &msg
}
