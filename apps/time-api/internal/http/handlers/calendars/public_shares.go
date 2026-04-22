package calendars

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/region"
)

// --- Input/Output types ---

// PublicShareResponse is the editor-facing shape of a share page. The
// plaintext token is only populated for create and rotate responses;
// every other endpoint leaves it empty.
type PublicShareResponse struct {
	ID                  string  `json:"id"`
	Title               string  `json:"title"`
	Description         *string `json:"description,omitempty"`
	IconUrl             *string `json:"iconUrl,omitempty"`
	CoverUrl            *string `json:"coverUrl,omitempty"`
	Timezone            string  `json:"timezone"`
	ShowHolidaysCountry *string `json:"showHolidaysCountry,omitempty"`
	ExpiresAt           *int64  `json:"expiresAt,omitempty"`
	SortWeight          int32   `json:"sortWeight"`
	EventCount          int64   `json:"eventCount"`
	CreatorID           *string `json:"creatorId,omitempty"`
	CreatorDisplayName  *string `json:"creatorDisplayName,omitempty"`
	Token               string  `json:"token,omitempty"`
	UpdatedAt           *int64  `json:"updatedAt,omitempty"`
	CreatedAt           int64   `json:"createdAt"`
}

// ShareEventResponse is the editor-facing projection of an event published on a share.
type ShareEventResponse struct {
	LinkID         string  `json:"linkId"`
	EventID        string  `json:"eventId"`
	Title          string  `json:"title"`
	StartAt        *int64  `json:"startAt,omitempty"`
	EndAt          *int64  `json:"endAt,omitempty"`
	AllDay         bool    `json:"allDay"`
	Timezone       string  `json:"timezone"`
	Location       *string `json:"location,omitempty"`
	Visibility     string  `json:"visibility"`
	CalendarID     string  `json:"calendarId"`
	CalendarName   string  `json:"calendarName"`
	LinkSortWeight int32   `json:"linkSortWeight"`
	LinkCreatedAt  int64   `json:"linkCreatedAt"`
}

// CreatePublicShareInput is the body for creating a new share page.
type CreatePublicShareInput struct {
	WsId string `path:"wsId" doc:"Workspace public ID"`
	Body struct {
		Title               string  `json:"title" minLength:"1" maxLength:"255" doc:"Public-facing title"`
		Description         *string `json:"description,omitempty" required:"false" doc:"Markdown description"`
		IconUrl             *string `json:"iconUrl,omitempty" required:"false" maxLength:"2048" doc:"Icon image URL"`
		CoverUrl            *string `json:"coverUrl,omitempty" required:"false" maxLength:"2048" doc:"Cover image URL"`
		Timezone            *string `json:"timezone,omitempty" required:"false" doc:"IANA timezone; defaults to workspace tz"`
		ShowHolidaysCountry *string `json:"showHolidaysCountry,omitempty" required:"false" minLength:"2" maxLength:"2" doc:"ISO 3166-1 alpha-2 country code; enables holiday overlay"`
		ExpiresAt           *int64  `json:"expiresAt,omitempty" required:"false" doc:"Unix seconds; omit for no expiry"`
	}
}

// CreatePublicShareOutput returns the new share with the plaintext token.
type CreatePublicShareOutput struct {
	Body PublicShareResponse
}

// ListPublicSharesInput lists every workspace share.
type ListPublicSharesInput struct {
	WsId string `path:"wsId" doc:"Workspace public ID"`
}

// ListPublicSharesOutput returns the share list (token omitted).
type ListPublicSharesOutput struct {
	Body struct {
		Shares []PublicShareResponse `json:"shares"`
	}
}

// GetPublicShareInput fetches a single share.
type GetPublicShareInput struct {
	WsId    string `path:"wsId" doc:"Workspace public ID"`
	ShareId string `path:"shareId" doc:"Share public ID"`
}

// GetPublicShareOutput returns the share plus its published events.
type GetPublicShareOutput struct {
	Body struct {
		Share  PublicShareResponse  `json:"share"`
		Events []ShareEventResponse `json:"events"`
	}
}

// PatchPublicShareInput updates mutable share fields. Setting
// clearExpiresAt=true drops expires_at to NULL; combine with expiresAt to
// set a new value. clearShowHolidaysCountry=true drops the country; set
// showHolidaysCountry explicitly to change it.
type PatchPublicShareInput struct {
	WsId    string `path:"wsId" doc:"Workspace public ID"`
	ShareId string `path:"shareId" doc:"Share public ID"`
	Body    struct {
		Title                    *string `json:"title,omitempty" required:"false" minLength:"1" maxLength:"255"`
		Description              *string `json:"description,omitempty" required:"false"`
		IconUrl                  *string `json:"iconUrl,omitempty" required:"false" maxLength:"2048"`
		CoverUrl                 *string `json:"coverUrl,omitempty" required:"false" maxLength:"2048"`
		Timezone                 *string `json:"timezone,omitempty" required:"false"`
		ShowHolidaysCountry      *string `json:"showHolidaysCountry,omitempty" required:"false" minLength:"2" maxLength:"2"`
		ClearShowHolidaysCountry bool    `json:"clearShowHolidaysCountry,omitempty" required:"false"`
		ExpiresAt                *int64  `json:"expiresAt,omitempty" required:"false"`
		ClearExpiresAt           bool    `json:"clearExpiresAt,omitempty" required:"false"`
		SortWeight               *int32  `json:"sortWeight,omitempty" required:"false"`
	}
}

// PatchPublicShareOutput returns the updated share.
type PatchPublicShareOutput struct {
	Body PublicShareResponse
}

// RotatePublicShareTokenInput rotates the URL token.
type RotatePublicShareTokenInput struct {
	WsId    string `path:"wsId" doc:"Workspace public ID"`
	ShareId string `path:"shareId" doc:"Share public ID"`
}

// RotatePublicShareTokenOutput returns the share with the new plaintext token.
type RotatePublicShareTokenOutput struct {
	Body PublicShareResponse
}

// DeletePublicShareInput soft-deletes a share. Admin/owner only.
type DeletePublicShareInput struct {
	WsId    string `path:"wsId" doc:"Workspace public ID"`
	ShareId string `path:"shareId" doc:"Share public ID"`
}

// DeletePublicShareOutput confirms deletion.
type DeletePublicShareOutput struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

// AttachEventsToShareInput bulk-adds events to a share.
type AttachEventsToShareInput struct {
	WsId    string `path:"wsId" doc:"Workspace public ID"`
	ShareId string `path:"shareId" doc:"Share public ID"`
	Body    struct {
		EventIds []string `json:"eventIds" minItems:"1" maxItems:"500" doc:"Event public IDs to attach; confidential events are rejected"`
	}
}

// AttachEventsToShareOutput reports how many links were created vs skipped.
type AttachEventsToShareOutput struct {
	Body struct {
		Attached int `json:"attached"`
		Skipped  int `json:"skipped"`
	}
}

// ReorderShareEventsInput batch-reorders the events published on a share
// by supplying the complete new ordering of link public IDs. The array
// must be a permutation of the share's current links — no partial
// reorders.
type ReorderShareEventsInput struct {
	WsId    string `path:"wsId" doc:"Workspace public ID"`
	ShareId string `path:"shareId" doc:"Share public ID"`
	Body    struct {
		LinkPublicIDs []string `json:"linkPublicIds" minItems:"0" maxItems:"500" doc:"Complete new ordering of share-event link public IDs; must be a permutation of the share's current links"`
	}
}

// ReorderShareEventsOutput confirms the reorder applied.
type ReorderShareEventsOutput struct {
	Body struct {
		Reordered bool `json:"reordered"`
	}
}

// DetachEventFromShareInput removes a single event from a share.
type DetachEventFromShareInput struct {
	WsId    string `path:"wsId" doc:"Workspace public ID"`
	ShareId string `path:"shareId" doc:"Share public ID"`
	EvtId   string `path:"evtId" doc:"Event public ID"`
}

// DetachEventFromShareOutput confirms the link was removed.
type DetachEventFromShareOutput struct {
	Body struct {
		Removed bool `json:"removed"`
	}
}

// --- Handlers ---

// CreatePublicShare mints a new workspace-owned share page and returns
// the plaintext token exactly once. Any non-guest workspace member can
// call this.
func CreatePublicShare(deps Deps) func(context.Context, *CreatePublicShareInput) (*CreatePublicShareOutput, error) {
	return func(ctx context.Context, input *CreatePublicShareInput) (*CreatePublicShareOutput, error) {
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}

		token, tokenHash, err := mintShareToken()
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}

		tz := ""
		if input.Body.Timezone != nil {
			tz = *input.Body.Timezone
		}
		if tz == "" {
			if ws, err := deps.Queries.FindWorkspaceTimezoneCountryById(ctx, wsID); err == nil {
				tz = ws.Timezone
			}
		}
		if tz == "" {
			tz = region.DefaultTimezone
		}
		if err := region.ValidateTimezone(tz); err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}

		publicID := types.New()
		params := generated.CreatePublicShareParams{
			PublicID:            publicID,
			WorkspaceID:         wsID,
			CreatedByUserID:     sql.NullInt32{Int32: int32(actorID), Valid: true},
			TokenHash:           tokenHash,
			Title:               input.Body.Title,
			Description:         nullStringFromPtr(input.Body.Description),
			IconUrl:             nullStringFromPtr(input.Body.IconUrl),
			CoverUrl:            nullStringFromPtr(input.Body.CoverUrl),
			Timezone:            tz,
			ShowHolidaysCountry: nullStringFromPtr(input.Body.ShowHolidaysCountry),
			ExpiresAt:           nullTimeFromUnixPtr(input.Body.ExpiresAt),
		}
		if _, err := deps.Queries.CreatePublicShare(ctx, params); err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}

		row, err := deps.Queries.FindPublicShareByPublicId(ctx, generated.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    publicID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "public_share.created", &actorID, map[string]any{
			"shareId": publicID.String(),
			"title":   input.Body.Title,
		})

		out := &CreatePublicShareOutput{}
		out.Body = publicShareFromRow(row, 0)
		out.Body.Token = token
		return out, nil
	}
}

// ListPublicShares returns every enabled share in the workspace.
func ListPublicShares(deps Deps) func(context.Context, *ListPublicSharesInput) (*ListPublicSharesOutput, error) {
	return func(ctx context.Context, input *ListPublicSharesInput) (*ListPublicSharesOutput, error) {
		wsID, _, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		rows, err := deps.Queries.ListPublicShares(ctx, wsID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}
		out := &ListPublicSharesOutput{}
		out.Body.Shares = make([]PublicShareResponse, len(rows))
		for i, r := range rows {
			out.Body.Shares[i] = publicShareFromListRow(r)
		}
		return out, nil
	}
}

// GetPublicShare returns the share and its published events for the editor UI.
func GetPublicShare(deps Deps) func(context.Context, *GetPublicShareInput) (*GetPublicShareOutput, error) {
	return func(ctx context.Context, input *GetPublicShareInput) (*GetPublicShareOutput, error) {
		wsID, _, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareId)
		if err != nil {
			return nil, errShareNotFound
		}
		row, err := deps.Queries.FindPublicShareByPublicId(ctx, generated.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errShareNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}
		events, err := deps.Queries.ListPublicShareEventsForEditor(ctx, generated.ListPublicShareEventsForEditorParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}
		out := &GetPublicShareOutput{}
		out.Body.Share = publicShareFromRow(row, int64(len(events)))
		out.Body.Events = make([]ShareEventResponse, len(events))
		for i, e := range events {
			out.Body.Events[i] = shareEventFromEditorRow(e)
		}
		return out, nil
	}
}

// PatchPublicShare updates mutable share fields.
func PatchPublicShare(deps Deps) func(context.Context, *PatchPublicShareInput) (*PatchPublicShareOutput, error) {
	return func(ctx context.Context, input *PatchPublicShareInput) (*PatchPublicShareOutput, error) {
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareId)
		if err != nil {
			return nil, errShareNotFound
		}

		if input.Body.Timezone != nil && *input.Body.Timezone != "" {
			if err := region.ValidateTimezone(*input.Body.Timezone); err != nil {
				return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
			}
		}

		params := generated.PatchPublicShareParams{
			WorkspaceID:         wsID,
			PublicID:            sharePID,
			Title:               nullStringFromPtr(input.Body.Title),
			Description:         nullStringFromPtr(input.Body.Description),
			IconUrl:             nullStringFromPtr(input.Body.IconUrl),
			CoverUrl:            nullStringFromPtr(input.Body.CoverUrl),
			Timezone:            nullStringFromPtr(input.Body.Timezone),
			ShowHolidaysCountry: nullStringFromPtr(input.Body.ShowHolidaysCountry),
			ExpiresAt:           nullTimeFromUnixPtr(input.Body.ExpiresAt),
			SortWeight:          nullInt32FromPtr(input.Body.SortWeight),
		}
		if err := deps.Queries.PatchPublicShare(ctx, params); err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}
		if input.Body.ClearExpiresAt {
			if err := deps.Queries.ClearPublicShareExpiresAt(ctx, generated.ClearPublicShareExpiresAtParams{
				WorkspaceID: wsID,
				PublicID:    sharePID,
			}); err != nil {
				return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
			}
		}

		row, err := deps.Queries.FindPublicShareByPublicId(ctx, generated.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errShareNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "public_share.updated", &actorID, map[string]any{
			"shareId": input.ShareId,
		})

		out := &PatchPublicShareOutput{}
		out.Body = publicShareFromRow(row, 0)
		return out, nil
	}
}

// RotatePublicShareToken regenerates the URL token; any previously
// distributed URL is invalidated. The new plaintext token is returned
// exactly once.
func RotatePublicShareToken(deps Deps) func(context.Context, *RotatePublicShareTokenInput) (*RotatePublicShareTokenOutput, error) {
	return func(ctx context.Context, input *RotatePublicShareTokenInput) (*RotatePublicShareTokenOutput, error) {
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareId)
		if err != nil {
			return nil, errShareNotFound
		}
		token, tokenHash, err := mintShareToken()
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}
		if err := deps.Queries.RotatePublicShareToken(ctx, generated.RotatePublicShareTokenParams{
			TokenHash:   tokenHash,
			WorkspaceID: wsID,
			PublicID:    sharePID,
		}); err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}
		row, err := deps.Queries.FindPublicShareByPublicId(ctx, generated.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errShareNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "public_share.rotated", &actorID, map[string]any{
			"shareId": input.ShareId,
		})

		out := &RotatePublicShareTokenOutput{}
		out.Body = publicShareFromRow(row, 0)
		out.Body.Token = token
		return out, nil
	}
}

// DeletePublicShare soft-deletes a share. Admin/owner only — share URLs
// survive member removal, and the plan deliberately restricts delete to
// workspace admins to prevent accidental URL loss.
func DeletePublicShare(deps Deps) func(context.Context, *DeletePublicShareInput) (*DeletePublicShareOutput, error) {
	return func(ctx context.Context, input *DeletePublicShareInput) (*DeletePublicShareOutput, error) {
		wsID, actorID, err := resolveWorkspaceAdmin(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareId)
		if err != nil {
			return nil, errShareNotFound
		}
		if err := deps.Queries.DisablePublicShare(ctx, generated.DisablePublicShareParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		}); err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreDeleteInterrupted)
		}
		_ = eventbus.Append(ctx, deps.DB, wsID, "public_share.deleted", &actorID, map[string]any{
			"shareId": input.ShareId,
		})
		out := &DeletePublicShareOutput{}
		out.Body.Deleted = true
		return out, nil
	}
}

// AttachEventsToShare bulk-publishes events on a share. Events marked
// confidential are rejected with SHARE.SHARE_EVENT.EVENT_NOT_VISIBLE for
// that specific event, but the rest of the batch still applies.
func AttachEventsToShare(deps Deps) func(context.Context, *AttachEventsToShareInput) (*AttachEventsToShareOutput, error) {
	return func(ctx context.Context, input *AttachEventsToShareInput) (*AttachEventsToShareOutput, error) {
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareId)
		if err != nil {
			return nil, errShareNotFound
		}
		share, err := deps.Queries.FindPublicShareByPublicId(ctx, generated.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errShareNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		attached := 0
		skipped := 0
		for _, raw := range input.Body.EventIds {
			pid, err := parsePublicID(raw)
			if err != nil {
				skipped++
				continue
			}
			evt, err := deps.Queries.FindEventIDAndVisibility(ctx, generated.FindEventIDAndVisibilityParams{
				WorkspaceID: wsID,
				PublicID:    pid,
			})
			if err != nil {
				skipped++
				continue
			}
			if evt.Visibility == generated.CalendarEventsVisibilityConfidential {
				skipped++
				continue
			}
			if _, err := deps.Queries.AttachEventToShare(ctx, generated.AttachEventToShareParams{
				PublicID:    types.New(),
				WorkspaceID: wsID,
				ShareID:     share.ID,
				EventID:     evt.ID,
				SortWeight:  0,
			}); err != nil {
				skipped++
				continue
			}
			attached++
		}

		if attached > 0 {
			_ = eventbus.Append(ctx, deps.DB, wsID, "public_share.events_attached", &actorID, map[string]any{
				"shareId":  input.ShareId,
				"attached": attached,
				"skipped":  skipped,
			})
		}

		out := &AttachEventsToShareOutput{}
		out.Body.Attached = attached
		out.Body.Skipped = skipped
		return out, nil
	}
}

// ReorderShareEvents atomically rewrites sort_weight for every share-event
// link on a share. The caller must supply the complete new ordering — the
// input array must be a permutation of the share's current links, else the
// request is rejected with SHARE.SHARE_EVENT.REORDER_INVALID and nothing
// is persisted. Authorised for any non-guest workspace member (same as
// AttachEventsToShare).
func ReorderShareEvents(deps Deps) func(context.Context, *ReorderShareEventsInput) (*ReorderShareEventsOutput, error) {
	return func(ctx context.Context, input *ReorderShareEventsInput) (*ReorderShareEventsOutput, error) {
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareId)
		if err != nil {
			return nil, errShareNotFound
		}
		share, err := deps.Queries.FindPublicShareByPublicId(ctx, generated.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errShareNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		// Parse and de-duplicate the requested ordering up front so a
		// repeated public ID is treated as the permutation invariant
		// violation it is, rather than silently collapsing.
		requested := make([]types.PublicID, len(input.Body.LinkPublicIDs))
		requestedSet := make(map[types.PublicID]struct{}, len(input.Body.LinkPublicIDs))
		for i, raw := range input.Body.LinkPublicIDs {
			pid, err := parsePublicID(raw)
			if err != nil {
				return nil, httpErr(apierrors.ShareShareEventReorderInvalid)
			}
			if _, dup := requestedSet[pid]; dup {
				return nil, httpErr(apierrors.ShareShareEventReorderInvalid)
			}
			requestedSet[pid] = struct{}{}
			requested[i] = pid
		}

		// Load the current set of links for this share and verify the
		// input is a permutation. sqlc's :exec directive does not expose
		// RowsAffected, so we validate up front and trust the tx.
		existing, err := deps.Queries.ListPublicShareEventsForEditor(ctx, generated.ListPublicShareEventsForEditorParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}
		if len(existing) != len(requested) {
			return nil, httpErr(apierrors.ShareShareEventReorderInvalid)
		}
		for _, e := range existing {
			if _, ok := requestedSet[e.LinkPublicID]; !ok {
				return nil, httpErr(apierrors.ShareShareEventReorderInvalid)
			}
		}

		// Short-circuit empty reorder: nothing to do, no tx needed.
		if len(requested) == 0 {
			out := &ReorderShareEventsOutput{}
			out.Body.Reordered = true
			return out, nil
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}
		defer func() { _ = tx.Rollback() }()
		qtx := deps.Queries.WithTx(tx)

		for i, pid := range requested {
			if err := qtx.UpdateShareEventSortWeight(ctx, generated.UpdateShareEventSortWeightParams{
				SortWeight: int32(i),
				ShareID:    share.ID,
				PublicID:   pid,
			}); err != nil {
				return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
			}
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "public_share.events_reordered", &actorID, map[string]any{
			"shareId": input.ShareId,
			"count":   len(requested),
		})

		out := &ReorderShareEventsOutput{}
		out.Body.Reordered = true
		return out, nil
	}
}

// DetachEventFromShare soft-disables one share↔event link.
func DetachEventFromShare(deps Deps) func(context.Context, *DetachEventFromShareInput) (*DetachEventFromShareOutput, error) {
	return func(ctx context.Context, input *DetachEventFromShareInput) (*DetachEventFromShareOutput, error) {
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareId)
		if err != nil {
			return nil, errShareNotFound
		}
		share, err := deps.Queries.FindPublicShareByPublicId(ctx, generated.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errShareNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}
		evtPID, err := parsePublicID(input.EvtId)
		if err != nil {
			return nil, errEventNotFound
		}
		evt, err := deps.Queries.FindEventIDAndVisibility(ctx, generated.FindEventIDAndVisibilityParams{
			WorkspaceID: wsID,
			PublicID:    evtPID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errEventNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}
		if err := deps.Queries.DetachEventFromShare(ctx, generated.DetachEventFromShareParams{
			ShareID: share.ID,
			EventID: evt.ID,
		}); err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}
		_ = eventbus.Append(ctx, deps.DB, wsID, "public_share.event_detached", &actorID, map[string]any{
			"shareId": input.ShareId,
			"eventId": input.EvtId,
		})
		out := &DetachEventFromShareOutput{}
		out.Body.Removed = true
		return out, nil
	}
}

// --- Helpers ---

var errShareNotFound = httpErr(apierrors.ShareShareNotFound)
var errShareDeleteForbidden = httpErr(apierrors.ShareShareDeleteForbidden)

// resolveWorkspaceNonGuest verifies the actor is a ws member and not a guest.
// Guests (read-only role) cannot touch share pages.
func resolveWorkspaceNonGuest(ctx context.Context, q *generated.Queries, wsIDStr string) (uint32, uint32, error) {
	wsID, actorID, err := resolveWorkspace(ctx, q, wsIDStr)
	if err != nil {
		return 0, 0, err
	}
	member, err := q.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
		WorkspaceID: wsID,
		UserID:      actorID,
	})
	if err != nil {
		return 0, 0, errAccessDenied
	}
	if member.Role == generated.WorkspaceMembersRoleGuest {
		return 0, 0, errAccessDenied
	}
	return wsID, actorID, nil
}

// resolveWorkspaceAdmin restricts to workspace admin/owner.
func resolveWorkspaceAdmin(ctx context.Context, q *generated.Queries, wsIDStr string) (uint32, uint32, error) {
	wsID, actorID, err := resolveWorkspace(ctx, q, wsIDStr)
	if err != nil {
		return 0, 0, err
	}
	member, err := q.FindWorkspaceMemberByUserId(ctx, generated.FindWorkspaceMemberByUserIdParams{
		WorkspaceID: wsID,
		UserID:      actorID,
	})
	if err != nil {
		return 0, 0, errAccessDenied
	}
	if member.Role != generated.WorkspaceMembersRoleAdmin && member.Role != generated.WorkspaceMembersRoleOwner {
		return 0, 0, errShareDeleteForbidden
	}
	return wsID, actorID, nil
}

// parsePublicID parses a UUID-v7 string into the DB public id type.
func parsePublicID(s string) (types.PublicID, error) {
	uid, err := uuid.Parse(s)
	if err != nil {
		return types.PublicID{}, err
	}
	return types.FromUUID(uid), nil
}

// mintShareToken generates a 32-char URL-safe token and returns
// (plaintext, hex(SHA-256(plaintext))).
func mintShareToken() (string, string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func nullStringFromPtr(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func nullTimeFromUnixPtr(p *int64) sql.NullTime {
	if p == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: time.Unix(*p, 0).UTC(), Valid: true}
}

func nullInt32FromPtr(p *int32) sql.NullInt32 {
	if p == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: *p, Valid: true}
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

// --- Row → DTO mappers ---

func publicShareFromRow(r generated.FindPublicShareByPublicIdRow, eventCount int64) PublicShareResponse {
	resp := PublicShareResponse{
		ID:                  r.PublicID.String(),
		Title:               r.Title,
		Description:         nullStringPtr(r.Description),
		IconUrl:             nullStringPtr(r.IconUrl),
		CoverUrl:            nullStringPtr(r.CoverUrl),
		Timezone:            r.Timezone,
		ShowHolidaysCountry: nullStringPtr(r.ShowHolidaysCountry),
		ExpiresAt:           nullTimeUnixPtr(r.ExpiresAt),
		SortWeight:          r.SortWeight,
		EventCount:          eventCount,
		UpdatedAt:           nullTimeUnixPtr(r.UpdatedAt),
		CreatedAt:           r.CreatedAt.Unix(),
	}
	return resp
}

func publicShareFromListRow(r generated.ListPublicSharesRow) PublicShareResponse {
	resp := PublicShareResponse{
		ID:                  r.PublicID.String(),
		Title:               r.Title,
		Description:         nullStringPtr(r.Description),
		IconUrl:             nullStringPtr(r.IconUrl),
		CoverUrl:            nullStringPtr(r.CoverUrl),
		Timezone:            r.Timezone,
		ShowHolidaysCountry: nullStringPtr(r.ShowHolidaysCountry),
		ExpiresAt:           nullTimeUnixPtr(r.ExpiresAt),
		SortWeight:          r.SortWeight,
		EventCount:          r.EventCount,
		CreatorDisplayName:  nullStringPtr(r.CreatorDisplayName),
		UpdatedAt:           nullTimeUnixPtr(r.UpdatedAt),
		CreatedAt:           r.CreatedAt.Unix(),
	}
	if r.CreatedByUserID.Valid {
		id := r.CreatorPublicID.String()
		resp.CreatorID = &id
	}
	return resp
}

func shareEventFromEditorRow(r generated.ListPublicShareEventsForEditorRow) ShareEventResponse {
	return ShareEventResponse{
		LinkID:         r.LinkPublicID.String(),
		EventID:        r.EventPublicID.String(),
		Title:          r.EventTitle,
		StartAt:        nullTimeUnixPtr(r.StartAt),
		EndAt:          nullTimeUnixPtr(r.EndAt),
		AllDay:         r.AllDay,
		Timezone:       r.EventTimezone,
		Location:       nullStringPtr(r.Location),
		Visibility:     string(r.Visibility),
		CalendarID:     r.CalendarPublicID.String(),
		CalendarName:   r.CalendarName,
		LinkSortWeight: r.LinkSortWeight,
		LinkCreatedAt:  r.LinkCreatedAt.Unix(),
	}
}
