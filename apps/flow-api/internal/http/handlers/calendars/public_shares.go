package calendars

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	generated "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
	sharedtoken "github.com/libraz/nodate-flow/packages/go-shared/token"
)

// --- Input/Output types ---

// PublicShareResponse is the editor-facing shape of a share page when
// the plaintext token is NOT exposed — list / get / patch endpoints
// return this variant. The token-bearing variants
// (PublicShareCreateResponse, PublicShareRotateResponse) are returned
// only by the two endpoints that mint a new token.
//
// Splitting the schema (rather than relying on `omitempty`) keeps the
// generated OpenAPI surface honest: SDK clients see a `Token` field
// only on the operations that actually return one, so accidental
// destructuring of `share.token` on a list response now fails at type
// check time instead of silently shipping `undefined`.
type PublicShareResponse struct {
	ID                  string  `json:"id"`
	Title               string  `json:"title"`
	Description         *string `json:"description,omitempty"`
	IconURL             *string `json:"iconUrl,omitempty"`
	CoverURL            *string `json:"coverUrl,omitempty"`
	Timezone            string  `json:"timezone"`
	ShowHolidaysCountry *string `json:"showHolidaysCountry,omitempty"`
	ExpiresAt           *int64  `json:"expiresAt,omitempty"`
	SortWeight          int32   `json:"sortWeight"`
	EventCount          int64   `json:"eventCount"`
	CreatorID           *string `json:"creatorId,omitempty"`
	CreatorDisplayName  *string `json:"creatorDisplayName,omitempty"`
	UpdatedAt           *int64  `json:"updatedAt,omitempty"`
	CreatedAt           int64   `json:"createdAt"`
}

// PublicShareCreateResponse extends PublicShareResponse with the
// plaintext token returned exactly once at creation time. Subsequent
// reads strip the token field by returning PublicShareResponse instead.
type PublicShareCreateResponse struct {
	PublicShareResponse
	Token string `json:"token"`
}

// PublicShareRotateResponse mirrors PublicShareCreateResponse but is a
// distinct type so the generated OpenAPI clearly distinguishes the
// "minted on create" and "rotated existing" code paths even though the
// payload shape coincides today.
type PublicShareRotateResponse struct {
	PublicShareResponse
	Token string `json:"token"`
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
	WsID string `path:"wsId" doc:"Workspace public ID"`
	Body struct {
		Title               string  `json:"title" minLength:"1" maxLength:"255" doc:"Public-facing title"`
		Description         *string `json:"description,omitempty" required:"false" maxLength:"10000" doc:"Markdown description"`
		IconURL             *string `json:"iconUrl,omitempty" required:"false" maxLength:"2048" doc:"Icon image URL"`
		CoverURL            *string `json:"coverUrl,omitempty" required:"false" maxLength:"2048" doc:"Cover image URL"`
		Timezone            *string `json:"timezone,omitempty" required:"false" maxLength:"64" doc:"IANA timezone; defaults to workspace tz"`
		ShowHolidaysCountry *string `json:"showHolidaysCountry,omitempty" required:"false" minLength:"2" maxLength:"2" doc:"ISO 3166-1 alpha-2 country code; enables holiday overlay"`
		ExpiresAt           *int64  `json:"expiresAt,omitempty" required:"false" doc:"Unix seconds; omit for no expiry"`
	}
}

// CreatePublicShareOutput returns the new share with the plaintext token.
type CreatePublicShareOutput struct {
	Body PublicShareCreateResponse
}

// ListPublicSharesInput lists every workspace share.
type ListPublicSharesInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
}

// ListPublicSharesOutput returns the share list (token omitted).
type ListPublicSharesOutput struct {
	Body struct {
		Shares []PublicShareResponse `json:"shares"`
	}
}

// GetPublicShareInput fetches a single share.
type GetPublicShareInput struct {
	WsID    string `path:"wsId" doc:"Workspace public ID"`
	ShareID string `path:"shareId" doc:"Share public ID"`
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
	WsID    string `path:"wsId" doc:"Workspace public ID"`
	ShareID string `path:"shareId" doc:"Share public ID"`
	Body    struct {
		Title                    *string `json:"title,omitempty" required:"false" minLength:"1" maxLength:"255"`
		Description              *string `json:"description,omitempty" required:"false" maxLength:"10000"`
		IconURL                  *string `json:"iconUrl,omitempty" required:"false" maxLength:"2048"`
		CoverURL                 *string `json:"coverUrl,omitempty" required:"false" maxLength:"2048"`
		Timezone                 *string `json:"timezone,omitempty" required:"false" maxLength:"64"`
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
	WsID    string `path:"wsId" doc:"Workspace public ID"`
	ShareID string `path:"shareId" doc:"Share public ID"`
}

// RotatePublicShareTokenOutput returns the share with the new plaintext token.
type RotatePublicShareTokenOutput struct {
	Body PublicShareRotateResponse
}

// DeletePublicShareInput soft-deletes a share. Admin/owner only.
type DeletePublicShareInput struct {
	WsID    string `path:"wsId" doc:"Workspace public ID"`
	ShareID string `path:"shareId" doc:"Share public ID"`
}

// DeletePublicShareOutput confirms deletion.
type DeletePublicShareOutput struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

// AttachEventsToShareInput bulk-adds events to a share.
type AttachEventsToShareInput struct {
	WsID    string `path:"wsId" doc:"Workspace public ID"`
	ShareID string `path:"shareId" doc:"Share public ID"`
	Body    struct {
		EventIDs []string `json:"eventIds" minItems:"1" maxItems:"500" doc:"Event public IDs to attach; confidential events are rejected"`
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
	WsID    string `path:"wsId" doc:"Workspace public ID"`
	ShareID string `path:"shareId" doc:"Share public ID"`
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
	WsID    string `path:"wsId" doc:"Workspace public ID"`
	ShareID string `path:"shareId" doc:"Share public ID"`
	EvtID   string `path:"evtId" doc:"Event public ID"`
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
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsID)
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
		if err := requireValidTimezone("timezone", tz); err != nil {
			return nil, err
		}

		publicID := types.New()
		params := calendar.CreatePublicShareParams{
			PublicID:            publicID,
			WorkspaceID:         wsID,
			CreatedByUserID:     sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
			TokenHash:           tokenHash,
			Title:               input.Body.Title,
			Description:         nullStringFromPtr(input.Body.Description),
			IconUrl:             nullStringFromPtr(input.Body.IconURL),
			CoverUrl:            nullStringFromPtr(input.Body.CoverURL),
			Timezone:            tz,
			ShowHolidaysCountry: nullStringFromPtr(input.Body.ShowHolidaysCountry),
			ExpiresAt:           nullTimeFromUnixPtr(input.Body.ExpiresAt),
		}
		if _, err := deps.CalendarQueries.CreatePublicShare(ctx, params); err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}

		row, err := deps.CalendarQueries.FindPublicShareByPublicId(ctx, calendar.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    publicID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, noOwningCalendar, eventbus.PublicShareCreated, &actorID, map[string]any{
			"shareId": publicID.String(),
			"title":   input.Body.Title,
		}, "calendars.CreatePublicShare")

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.share.create",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.share",
			ResourceID:   publicID.String(),
			Metadata: map[string]any{
				"title": input.Body.Title,
			},
		})

		out := &CreatePublicShareOutput{}
		out.Body = PublicShareCreateResponse{
			PublicShareResponse: publicShareFromRow(row, 0),
			Token:               token,
		}
		return out, nil
	}
}

// ListPublicShares returns every enabled share in the workspace.
func ListPublicShares(deps Deps) func(context.Context, *ListPublicSharesInput) (*ListPublicSharesOutput, error) {
	return func(ctx context.Context, input *ListPublicSharesInput) (*ListPublicSharesOutput, error) {
		wsID, _, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		rows, err := deps.CalendarQueries.ListPublicShares(ctx, wsID)
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
		wsID, _, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareID)
		if err != nil {
			return nil, errShareNotFound
		}
		row, err := deps.CalendarQueries.FindPublicShareByPublicId(ctx, calendar.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errShareNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}
		events, err := deps.CalendarQueries.ListPublicShareEventsForEditor(ctx, calendar.ListPublicShareEventsForEditorParams{
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
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareID)
		if err != nil {
			return nil, errShareNotFound
		}

		if input.Body.Timezone != nil && *input.Body.Timezone != "" {
			if err := requireValidTimezone("timezone", *input.Body.Timezone); err != nil {
				return nil, err
			}
		}

		params := calendar.PatchPublicShareParams{
			WorkspaceID:         wsID,
			PublicID:            sharePID,
			Title:               nullStringFromPtr(input.Body.Title),
			Description:         nullStringFromPtr(input.Body.Description),
			IconUrl:             nullStringFromPtr(input.Body.IconURL),
			CoverUrl:            nullStringFromPtr(input.Body.CoverURL),
			Timezone:            nullStringFromPtr(input.Body.Timezone),
			ShowHolidaysCountry: nullStringFromPtr(input.Body.ShowHolidaysCountry),
			ExpiresAt:           nullTimeFromUnixPtr(input.Body.ExpiresAt),
			SortWeight:          nullInt32FromPtr(input.Body.SortWeight),
		}
		// Not an existence check: a PATCH carrying the values the share
		// already has changes nothing and MySQL counts zero. The share
		// was resolved above.
		if _, err := deps.CalendarQueries.PatchPublicShare(ctx, params); err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}
		if input.Body.ClearExpiresAt {
			// Same: clearing an expiry that is already NULL counts zero.
			if _, err := deps.CalendarQueries.ClearPublicShareExpiresAt(ctx, calendar.ClearPublicShareExpiresAtParams{
				WorkspaceID: wsID,
				PublicID:    sharePID,
			}); err != nil {
				return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
			}
		}

		row, err := deps.CalendarQueries.FindPublicShareByPublicId(ctx, calendar.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errShareNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, noOwningCalendar, eventbus.PublicShareUpdated, &actorID, map[string]any{
			"shareId": input.ShareID,
		}, "calendars.PatchPublicShare")

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.share.update",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.share",
			ResourceID:   input.ShareID,
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
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareID)
		if err != nil {
			return nil, errShareNotFound
		}
		token, tokenHash, err := mintShareToken()
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}
		// A freshly minted hash never equals the stored one, so zero rows
		// here does mean "no such share" -- and answering ok would hand
		// the caller a token that unlocks nothing.
		rotated, err := deps.CalendarQueries.RotatePublicShareToken(ctx, calendar.RotatePublicShareTokenParams{
			TokenHash:   tokenHash,
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}
		if rotated == 0 {
			return nil, errShareNotFound
		}
		row, err := deps.CalendarQueries.FindPublicShareByPublicId(ctx, calendar.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errShareNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, noOwningCalendar, eventbus.PublicShareRotated, &actorID, map[string]any{
			"shareId": input.ShareID,
		}, "calendars.RotatePublicShareToken")

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.share.rotate",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.share",
			ResourceID:   input.ShareID,
		})

		out := &RotatePublicShareTokenOutput{}
		out.Body = PublicShareRotateResponse{
			PublicShareResponse: publicShareFromRow(row, 0),
			Token:               token,
		}
		return out, nil
	}
}

// DeletePublicShare soft-deletes a share. Admin/owner only — share URLs
// survive member removal, and the plan deliberately restricts delete to
// workspace admins to prevent accidental URL loss.
func DeletePublicShare(deps Deps) func(context.Context, *DeletePublicShareInput) (*DeletePublicShareOutput, error) {
	return func(ctx context.Context, input *DeletePublicShareInput) (*DeletePublicShareOutput, error) {
		wsID, actorID, err := resolveWorkspaceAdmin(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareID)
		if err != nil {
			return nil, errShareNotFound
		}
		// Only matches a share that is still live, so zero rows means
		// nothing was unpublished. Answering ok here told the caller a
		// public URL had been taken down while it stayed reachable.
		rows, err := deps.CalendarQueries.DisablePublicShare(ctx, calendar.DisablePublicShareParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreDeleteInterrupted)
		}
		if rows == 0 {
			return nil, errShareNotFound
		}
		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, noOwningCalendar, eventbus.PublicShareDeleted, &actorID, map[string]any{
			"shareId": input.ShareID,
		}, "calendars.DeletePublicShare")
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.share.delete",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.share",
			ResourceID:   input.ShareID,
		})
		out := &DeletePublicShareOutput{}
		out.Body.Deleted = true
		return out, nil
	}
}

// AttachEventsToShare bulk-publishes events on a share.
//
// Publishing is per-event rather than per-share, so the authorization is
// per-event too: the actor must hold editor or better on the calendar each
// event lives in. Workspace membership is not enough and never was — a
// workspace holds calendars whose audiences do not coincide, and this
// endpoint's output is a URL anyone on the internet can open, so "may not
// read it" has to imply "may not publish it". Confidential events are
// refused outright, at any role.
//
// An event the actor cannot publish is counted as skipped rather than
// failing the batch, matching how the confidential case has always
// behaved: the request is a list of candidates, not a transaction.
func AttachEventsToShare(deps Deps) func(context.Context, *AttachEventsToShareInput) (*AttachEventsToShareOutput, error) {
	return func(ctx context.Context, input *AttachEventsToShareInput) (*AttachEventsToShareOutput, error) {
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareID)
		if err != nil {
			return nil, errShareNotFound
		}
		share, err := deps.CalendarQueries.FindPublicShareByPublicId(ctx, calendar.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errShareNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		// One membership lookup per distinct calendar, not per event: a
		// 500-event batch is usually drawn from a handful of calendars,
		// and the answer cannot change inside one request.
		publishable := make(map[uint32]bool)
		mayPublishFrom := func(calID uint32) (bool, error) {
			if ok, seen := publishable[calID]; seen {
				return ok, nil
			}
			member, err := deps.CalendarQueries.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
				CalendarID: calID,
				UserID:     actorID,
			})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					publishable[calID] = false
					return false, nil
				}
				return false, err
			}
			ok := roleRank(member.Role) >= roleRank(calendar.CalendarMembersRoleEditor)
			publishable[calID] = ok
			return ok, nil
		}

		attached := 0
		skipped := 0
		for _, raw := range input.Body.EventIDs {
			pid, err := parsePublicID(raw)
			if err != nil {
				skipped++
				continue
			}
			evt, err := deps.CalendarQueries.FindEventIDAndVisibility(ctx, calendar.FindEventIDAndVisibilityParams{
				WorkspaceID: wsID,
				PublicID:    pid,
			})
			if err != nil {
				skipped++
				continue
			}
			if evt.Visibility == calendar.CalendarEventsVisibilityConfidential {
				skipped++
				continue
			}
			allowed, err := mayPublishFrom(evt.CalendarID)
			if err != nil {
				return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
			}
			if !allowed {
				skipped++
				continue
			}
			if _, err := deps.CalendarQueries.AttachEventToShare(ctx, calendar.AttachEventToShareParams{
				PublicID:    types.New(),
				WorkspaceID: wsID,
				ShareID:     share.ID,
				EventID:     evt.ID,
				SortWeight:  0,
			}); err != nil {
				// A duplicate here means a live link already exists, so
				// the state the caller asked for holds and the honest
				// count is attached. Reporting it as skipped told the
				// caller the event was not published while it was —
				// the worst of the two ways to be wrong about a page
				// anyone on the internet can open.
				if handlerutil.IsDuplicateEntry(err) {
					attached++
					continue
				}
				skipped++
				continue
			}
			attached++
		}

		if attached > 0 {
			appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, noOwningCalendar, eventbus.PublicShareEventsAttached, &actorID, map[string]any{
				"shareId":  input.ShareID,
				"attached": attached,
				"skipped":  skipped,
			}, "calendars.AttachEventsToShare")
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "calendar.share.events_attach",
				ActorID:      actorID,
				WorkspaceID:  wsID,
				ResourceType: "calendar.share",
				ResourceID:   input.ShareID,
				Metadata: map[string]any{
					"attached": attached,
					"skipped":  skipped,
				},
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
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareID)
		if err != nil {
			return nil, errShareNotFound
		}
		share, err := deps.CalendarQueries.FindPublicShareByPublicId(ctx, calendar.FindPublicShareByPublicIdParams{
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
		existing, err := deps.CalendarQueries.ListPublicShareEventsForEditor(ctx, calendar.ListPublicShareEventsForEditorParams{
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
		cqtx := deps.CalendarQueries.WithTx(tx)

		for i, pid := range requested {
			// Not an existence check: an event already at position i keeps
			// its sort_weight and MySQL counts zero. The caller's list is
			// validated against the share's current events before this
			// loop, which is what rejects an event that is not on it.
			if _, err := cqtx.UpdateShareEventSortWeight(ctx, calendar.UpdateShareEventSortWeightParams{
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

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, noOwningCalendar, eventbus.PublicShareEventsReordered, &actorID, map[string]any{
			"shareId": input.ShareID,
			"count":   len(requested),
		}, "calendars.ReorderShareEvents")

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.share.events_reorder",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.share",
			ResourceID:   input.ShareID,
			Metadata: map[string]any{
				"count": len(requested),
			},
		})

		out := &ReorderShareEventsOutput{}
		out.Body.Reordered = true
		return out, nil
	}
}

// DetachEventFromShare soft-disables one share↔event link.
func DetachEventFromShare(deps Deps) func(context.Context, *DetachEventFromShareInput) (*DetachEventFromShareOutput, error) {
	return func(ctx context.Context, input *DetachEventFromShareInput) (*DetachEventFromShareOutput, error) {
		wsID, actorID, err := resolveWorkspaceNonGuest(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		sharePID, err := parsePublicID(input.ShareID)
		if err != nil {
			return nil, errShareNotFound
		}
		share, err := deps.CalendarQueries.FindPublicShareByPublicId(ctx, calendar.FindPublicShareByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    sharePID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errShareNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}
		evtPID, err := parsePublicID(input.EvtID)
		if err != nil {
			return nil, errEventNotFound
		}
		evt, err := deps.CalendarQueries.FindEventIDAndVisibility(ctx, calendar.FindEventIDAndVisibilityParams{
			WorkspaceID: wsID,
			PublicID:    evtPID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errEventNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}
		// Only matches a link that is still live, so zero rows means the
		// event was never on this share -- or somebody else detached it
		// first. Reporting success for that told the caller a page had
		// stopped showing an event it was still publishing.
		rows, err := deps.CalendarQueries.DetachEventFromShare(ctx, calendar.DetachEventFromShareParams{
			ShareID: share.ID,
			EventID: evt.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}
		if rows == 0 {
			return nil, httpErr(apierrors.ShareShareEventNotFound)
		}
		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, evt.CalendarID, eventbus.PublicShareEventDetached, &actorID, map[string]any{
			"shareId": input.ShareID,
			"eventId": input.EvtID,
		}, "calendars.DetachEventFromShare")
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.share.event_detach",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.share",
			ResourceID:   input.ShareID,
			Metadata: map[string]any{
				"eventId": input.EvtID,
			},
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

// mintShareToken delegates to the shared token package so the share
// minting logic stays in lockstep with the invite minting logic. The
// share + invite columns both store hex(SHA-256(plaintext)).
func mintShareToken() (string, string, error) {
	return sharedtoken.MintToken()
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
	return sql.NullTime{Time: handlerutil.UnixToTime(*p), Valid: true}
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

func publicShareFromRow(r calendar.FindPublicShareByPublicIdRow, eventCount int64) PublicShareResponse {
	resp := PublicShareResponse{
		ID:                  r.PublicID.String(),
		Title:               r.Title,
		Description:         nullStringPtr(r.Description),
		IconURL:             nullStringPtr(r.IconUrl),
		CoverURL:            nullStringPtr(r.CoverUrl),
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

func publicShareFromListRow(r calendar.ListPublicSharesRow) PublicShareResponse {
	resp := PublicShareResponse{
		ID:                  r.PublicID.String(),
		Title:               r.Title,
		Description:         nullStringPtr(r.Description),
		IconURL:             nullStringPtr(r.IconUrl),
		CoverURL:            nullStringPtr(r.CoverUrl),
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

func shareEventFromEditorRow(r calendar.ListPublicShareEventsForEditorRow) ShareEventResponse {
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
