package calendars

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
)

// --- Public render (unauthenticated) ---

// PublicShareRenderEvent is the externally visible event shape for the
// /share/cal/{token} render. It deliberately omits fields a public
// viewer should not see (attendees, owner, memos flagged private, task
// linkage). Recurrence data is passed through so the client renderer
// can expand instances.
type PublicShareRenderEvent struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	StartAt        *int64  `json:"startAt,omitempty"`
	EndAt          *int64  `json:"endAt,omitempty"`
	AllDay         bool    `json:"allDay"`
	Timezone       string  `json:"timezone"`
	Location       *string `json:"location,omitempty"`
	Memo           *string `json:"memo,omitempty"`
	URL            *string `json:"url,omitempty"`
	Kind           string  `json:"kind"`
	ShowAs         string  `json:"showAs"`
	Flexibility    string  `json:"flexibility"`
	BlockLabel     *string `json:"blockLabel,omitempty"`
	RecurrenceRule *string `json:"recurrenceRule,omitempty"`
	RecurrenceEnd  *int64  `json:"recurrenceEnd,omitempty"`
	// JSON array of ISO 8601 dates/times to exclude from recurrence.
	RecurrenceExceptions *string `json:"recurrenceExceptions,omitempty"`
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
			}
			// `private`-visibility events honour a "time only" contract on
			// the public, unauthenticated page: the time block stays visible
			// (start/end, show_as, flexibility, block_label) but all
			// descriptive content is stripped so title/notes/location/url
			// never leak. `confidential` events are excluded entirely by the
			// render query.
			if e.Visibility == calendar.CalendarEventsVisibilityPrivate {
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

// rawMessagePtr converts the JSON-encoded recurrence rule into a string
// pointer. The sqlc-generated column is `json.RawMessage`; we pass it
// through as an opaque string so the client can parse it without schema
// coupling, and return nil for the literal "null" value COALESCE emits.
func rawMessagePtr(raw []byte) *string {
	if len(raw) == 0 {
		return nil
	}
	s := string(raw)
	if s == "null" {
		return nil
	}
	return &s
}
