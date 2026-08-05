package calendars

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// --- Input/Output types ---

// ListCalendarsInput is the input for the list calendars endpoint.
type ListCalendarsInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
}

// CalendarResponse is the JSON representation of a calendar with subscription info.
type CalendarResponse struct {
	ID                     string  `json:"id"`
	Kind                   string  `json:"kind"`
	Name                   string  `json:"name"`
	Description            *string `json:"description,omitempty"`
	Color                  string  `json:"color"`
	CoverURL               *string `json:"coverUrl,omitempty"`
	SystemSlug             *string `json:"systemSlug,omitempty"`
	Role                   string  `json:"role"`
	MemberColor            string  `json:"memberColor"`
	DisplayColor           string  `json:"displayColor"`
	Visible                bool    `json:"visible"`
	SubscriptionSortWeight int32   `json:"subscriptionSortWeight"`
	UpdatedAt              *int64  `json:"updatedAt,omitempty"`
	CreatedAt              int64   `json:"createdAt"`
}

// ListCalendarsOutput is the response for the list calendars endpoint.
type ListCalendarsOutput struct {
	Body struct {
		Calendars []CalendarResponse `json:"calendars"`
	}
}

// CreateCalendarInput is the input for the create calendar endpoint.
type CreateCalendarInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
	Body struct {
		Kind        string  `json:"kind" enum:"personal,system" doc:"Calendar kind"`
		Name        string  `json:"name" minLength:"1" maxLength:"255" doc:"Calendar name"`
		Description *string `json:"description,omitempty" required:"false" doc:"Calendar description"`
		Color       string  `json:"color" minLength:"1" maxLength:"7" doc:"Display color (hex)"`
		CoverURL    *string `json:"coverUrl,omitempty" required:"false" doc:"Cover image URL"`
		SystemSlug  *string `json:"systemSlug,omitempty" required:"false" doc:"System calendar slug"`
	}
}

// CreateCalendarOutput is the response for the create calendar endpoint.
type CreateCalendarOutput struct {
	Body CalendarResponse
}

// GetCalendarInput is the input for the get calendar endpoint.
type GetCalendarInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
}

// GetCalendarOutput is the response for the get calendar endpoint.
type GetCalendarOutput struct {
	Body CalendarResponse
}

// PatchCalendarInput is the input for the patch calendar endpoint.
type PatchCalendarInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		Name        *string `json:"name,omitempty" required:"false" doc:"Calendar name"`
		Description *string `json:"description,omitempty" required:"false" doc:"Calendar description"`
		Color       *string `json:"color,omitempty" required:"false" doc:"Display color"`
		CoverURL    *string `json:"coverUrl,omitempty" required:"false" doc:"Cover image URL"`
	}
}

// PatchCalendarOutput is the response for the patch calendar endpoint.
type PatchCalendarOutput struct {
	Body CalendarResponse
}

// DeleteCalendarInput is the input for the delete calendar endpoint.
type DeleteCalendarInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
}

// DeleteCalendarOutput is the response for the delete calendar endpoint.
type DeleteCalendarOutput struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

// --- Handlers ---

// ListCalendars returns all calendars the authenticated user subscribes to
// within the given workspace.
func ListCalendars(deps Deps) func(context.Context, *ListCalendarsInput) (*ListCalendarsOutput, error) {
	return func(ctx context.Context, input *ListCalendarsInput) (*ListCalendarsOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}

		// Lazily create personal + system calendars on first access.
		ensureDefaults(ctx, deps.Queries, deps.CalendarQueries, wsID, actorID)

		rows, err := deps.CalendarQueries.ListCalendarsForUser(ctx, calendar.ListCalendarsForUserParams{
			UserID:      actorID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarListQueryInterrupted)
		}
		out := &ListCalendarsOutput{}
		out.Body.Calendars = make([]CalendarResponse, len(rows))
		for i, r := range rows {
			out.Body.Calendars[i] = calendarFromListRow(r)
		}
		return out, nil
	}
}

// CreateCalendar creates a new calendar in the workspace and automatically
// subscribes the creator as owner.
func CreateCalendar(deps Deps) func(context.Context, *CreateCalendarInput) (*CreateCalendarOutput, error) {
	return func(ctx context.Context, input *CreateCalendarInput) (*CreateCalendarOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}

		calPublicID := types.New()

		desc := sql.NullString{}
		if input.Body.Description != nil {
			desc = sql.NullString{String: *input.Body.Description, Valid: true}
		}
		coverURL := sql.NullString{}
		if input.Body.CoverURL != nil {
			coverURL = sql.NullString{String: *input.Body.CoverURL, Valid: true}
		}
		systemSlug := sql.NullString{}
		if input.Body.SystemSlug != nil {
			systemSlug = sql.NullString{String: *input.Body.SystemSlug, Valid: true}
		}

		calID, err := deps.CalendarQueries.CreateCalendar(ctx, calendar.CreateCalendarParams{
			PublicID:    calPublicID,
			WorkspaceID: wsID,
			Kind:        calendar.CalendarsKind(input.Body.Kind),
			Name:        input.Body.Name,
			Description: desc,
			Color:       input.Body.Color,
			CoverUrl:    coverURL,
			OwnerUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
			SystemSlug:  systemSlug,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}

		// The creator becomes the calendar's owner member. Without this row
		// the calendar would exist with nobody able to reach it: access is
		// calendar_members, and every resolution helper reads it.
		_, err = deps.CalendarQueries.UpsertCalendarMember(ctx, calendar.UpsertCalendarMemberParams{
			PublicID:    types.New(),
			WorkspaceID: wsID,
			CalendarID:  uint32(calID), //#nosec G115 -- LastInsertId for calendars.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
			UserID:      actorID,
			Role:        calendar.CalendarMembersRoleOwner,
			MemberColor: input.Body.Color,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}

		out := &CreateCalendarOutput{}
		out.Body = CalendarResponse{
			ID:                     calPublicID.String(),
			Kind:                   input.Body.Kind,
			Name:                   input.Body.Name,
			Description:            input.Body.Description,
			Color:                  input.Body.Color,
			CoverURL:               input.Body.CoverURL,
			SystemSlug:             input.Body.SystemSlug,
			Role:                   string(calendar.CalendarMembersRoleOwner),
			MemberColor:            input.Body.Color,
			DisplayColor:           input.Body.Color,
			Visible:                true,
			SubscriptionSortWeight: 0,
			CreatedAt:              handlerutil.NowUnix(),
		}

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.created", &actorID, map[string]any{
			"calendarId": calPublicID.String(),
			"name":       input.Body.Name,
			"kind":       input.Body.Kind,
		})

		return out, nil
	}
}

// GetCalendar returns a single calendar with the actor's subscription info.
func GetCalendar(deps Deps) func(context.Context, *GetCalendarInput) (*GetCalendarOutput, error) {
	return func(ctx context.Context, input *GetCalendarInput) (*GetCalendarOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, member, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		out := &GetCalendarOutput{}
		out.Body = calendarFromRow(cal, member, findSubscription(ctx, deps.CalendarQueries, cal.ID, actorID))
		return out, nil
	}
}

// PatchCalendar updates mutable calendar fields. Only owners and managers can
// perform this operation.
func PatchCalendar(deps Deps) func(context.Context, *PatchCalendarInput) (*PatchCalendarOutput, error) {
	return func(ctx context.Context, input *PatchCalendarInput) (*PatchCalendarOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		// Editing the calendar itself — its name, colour, cover — is
		// administration rather than use, so an editor who may add events
		// still may not rename the calendar out from under everyone.
		cal, member, err := resolveCalendarAdmin(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		patchName := sql.NullString{}
		if input.Body.Name != nil {
			patchName = sql.NullString{String: *input.Body.Name, Valid: true}
		}
		patchDesc := sql.NullString{}
		if input.Body.Description != nil {
			patchDesc = sql.NullString{String: *input.Body.Description, Valid: true}
		}
		patchColor := sql.NullString{}
		if input.Body.Color != nil {
			patchColor = sql.NullString{String: *input.Body.Color, Valid: true}
		}
		patchCover := sql.NullString{}
		if input.Body.CoverURL != nil {
			patchCover = sql.NullString{String: *input.Body.CoverURL, Valid: true}
		}

		calUID, _ := uuid.Parse(input.CalID)
		err = deps.CalendarQueries.PatchCalendar(ctx, calendar.PatchCalendarParams{
			Name:        patchName,
			Description: patchDesc,
			Color:       patchColor,
			CoverUrl:    patchCover,
			PublicID:    types.FromUUID(calUID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreWriteInterrupted)
		}

		// Re-read the updated calendar.
		cal, err = deps.CalendarQueries.FindCalendarByPublicId(ctx, calendar.FindCalendarByPublicIdParams{
			PublicID:    types.FromUUID(calUID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}
		out := &PatchCalendarOutput{}
		out.Body = calendarFromRow(cal, member, findSubscription(ctx, deps.CalendarQueries, cal.ID, actorID))

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.updated", &actorID, map[string]any{
			"calendarId": input.CalID,
		})

		return out, nil
	}
}

// DeleteCalendar soft-deletes a calendar. Only owners can perform this.
func DeleteCalendar(deps Deps) func(context.Context, *DeleteCalendarInput) (*DeleteCalendarOutput, error) {
	return func(ctx context.Context, input *DeleteCalendarInput) (*DeleteCalendarOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		// Deleting the calendar is the one action a manager does not get:
		// managers curate membership and content, owners decide whether the
		// calendar exists.
		cal, _, err := resolveCalendarAtLeast(
			ctx, deps.CalendarQueries, wsID, actorID, input.CalID, calendar.CalendarMembersRoleOwner,
		)
		if err != nil {
			return nil, err
		}
		_ = cal

		calUID, _ := uuid.Parse(input.CalID)
		err = deps.CalendarQueries.DisableCalendar(ctx, calendar.DisableCalendarParams{
			PublicID:    types.FromUUID(calUID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarStoreDeleteInterrupted)
		}

		out := &DeleteCalendarOutput{}
		out.Body.Deleted = true

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.deleted", &actorID, map[string]any{
			"calendarId": input.CalID,
		})

		return out, nil
	}
}

// --- Mapping helpers ---

func calendarFromListRow(r calendar.ListCalendarsForUserRow) CalendarResponse {
	resp := CalendarResponse{
		ID:                     r.PublicID.String(),
		Kind:                   string(r.Kind),
		Name:                   r.Name,
		Color:                  r.Color,
		Role:                   string(r.Role),
		MemberColor:            r.MemberColor,
		DisplayColor:           r.DisplayColor,
		Visible:                r.Visible,
		SubscriptionSortWeight: r.SubscriptionSortWeight,
		CreatedAt:              r.CreatedAt.Unix(),
	}
	resp.Description = dbtype.PtrFromNullString(r.Description)
	resp.CoverURL = dbtype.PtrFromNullString(r.CoverUrl)
	resp.SystemSlug = dbtype.PtrFromNullString(r.SystemSlug)
	resp.UpdatedAt = dbtype.UnixSecondsFromNullTime(r.UpdatedAt)
	return resp
}

// calendarFromRow renders a calendar for a single member. sub is nil when
// the member has never set display preferences for it, in which case the
// membership supplies the defaults — the same values the list query
// COALESCEs to, so the two endpoints cannot disagree.
func calendarFromRow(
	c calendar.FindCalendarByPublicIdRow,
	m calendar.FindCalendarMemberRow,
	sub *calendar.FindCalendarSubscriptionRow,
) CalendarResponse {
	displayColor := m.MemberColor
	visible := true
	var sortWeight int32
	if sub != nil {
		displayColor = sub.DisplayColor
		visible = sub.Visible
		sortWeight = sub.SortWeight
	}
	resp := CalendarResponse{
		ID:                     c.PublicID.String(),
		Kind:                   string(c.Kind),
		Name:                   c.Name,
		Color:                  c.Color,
		Role:                   string(m.Role),
		MemberColor:            m.MemberColor,
		DisplayColor:           displayColor,
		Visible:                visible,
		SubscriptionSortWeight: sortWeight,
		CreatedAt:              c.CreatedAt.Unix(),
	}
	resp.Description = dbtype.PtrFromNullString(c.Description)
	resp.CoverURL = dbtype.PtrFromNullString(c.CoverUrl)
	resp.SystemSlug = dbtype.PtrFromNullString(c.SystemSlug)
	resp.UpdatedAt = dbtype.UnixSecondsFromNullTime(c.UpdatedAt)
	return resp
}
