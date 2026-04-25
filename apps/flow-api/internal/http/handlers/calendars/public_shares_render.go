package calendars

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
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
	Url            *string `json:"url,omitempty"`
	Kind           string  `json:"kind"`
	ShowAs         string  `json:"showAs"`
	BlockLabel     *string `json:"blockLabel,omitempty"`
	RecurrenceRule *string `json:"recurrenceRule,omitempty"`
	RecurrenceEnd  *int64  `json:"recurrenceEnd,omitempty"`
}

// PublicShareRenderPage is the workspace-facing metadata exposed on the
// public page. The workspace ID is included so the client can group
// events if multiple shares are ever rendered side-by-side.
type PublicShareRenderPage struct {
	Title               string  `json:"title"`
	Description         *string `json:"description,omitempty"`
	IconUrl             *string `json:"iconUrl,omitempty"`
	CoverUrl            *string `json:"coverUrl,omitempty"`
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

		page, err := deps.Queries.FindPublicShareByTokenHash(ctx, hash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.ShareShareTokenInvalid)
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}
		if page.ExpiresAt.Valid && page.ExpiresAt.Time.Before(time.Now()) {
			return nil, httpErr(apierrors.ShareShareExpired)
		}

		events, err := deps.Queries.ListPublicShareEventsByTokenHash(ctx, hash)
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		out := &RenderPublicShareOutput{}
		out.Body.Page = PublicShareRenderPage{
			Title:               page.Title,
			Description:         nullStringPtr(page.Description),
			IconUrl:             nullStringPtr(page.IconUrl),
			CoverUrl:            nullStringPtr(page.CoverUrl),
			Timezone:            page.Timezone,
			ShowHolidaysCountry: nullStringPtr(page.ShowHolidaysCountry),
			WorkspaceID:         page.WorkspacePublicID.String(),
			WorkspaceName:       page.WorkspaceName,
			CreatedAt:           page.CreatedAt.Unix(),
		}
		out.Body.Events = make([]PublicShareRenderEvent, len(events))
		for i, e := range events {
			out.Body.Events[i] = PublicShareRenderEvent{
				ID:             e.EventPublicID.String(),
				Title:          e.Title,
				StartAt:        nullTimeUnixPtr(e.StartAt),
				EndAt:          nullTimeUnixPtr(e.EndAt),
				AllDay:         e.AllDay,
				Timezone:       e.Timezone,
				Location:       nullStringPtr(e.Location),
				Memo:           nullStringPtr(e.Memo),
				Url:            nullStringPtr(e.Url),
				Kind:           string(e.Kind),
				ShowAs:         string(e.ShowAs),
				BlockLabel:     nullStringPtr(e.BlockLabel),
				RecurrenceRule: rawMessagePtr(e.RecurrenceRule),
				RecurrenceEnd:  nullTimeUnixPtr(e.RecurrenceEnd),
			}
		}
		return out, nil
	}
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
