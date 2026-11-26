package calendars

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	sharedbus "github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// UpdateMemberColorInput is the input for changing a member's colour.
type UpdateMemberColorInput struct {
	WsID   string `path:"wsId" doc:"Workspace public ID"`
	CalID  string `path:"calId" doc:"Calendar public ID"`
	UserID string `path:"userId" doc:"User public ID"`
	Body   struct {
		Color string `json:"color" maxLength:"7" pattern:"^#[0-9A-Fa-f]{6}$" doc:"Member colour as #RRGGBB"`
	}
}

// UpdateMemberColorOutput is the response for the member colour endpoint.
type UpdateMemberColorOutput struct {
	Body MemberResponse
}

// resolveMemberColorWriter resolves the calendar and the member whose colour
// the caller wants to change. A member may always pick their own colour, at
// any role and on any calendar kind: the colour is how they see the calendar,
// not a write to its contents. Changing someone else's needs manager or above.
func resolveMemberColorWriter(
	ctx context.Context,
	deps Deps,
	wsID, actorID uint32,
	calIDStr, userIDStr string,
) (calendar.FindCalendarByPublicIdRow, calendar.FindCalendarMemberRow, error) {
	cal, actor, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, calIDStr)
	if err != nil {
		return cal, actor, err
	}
	targetUID, err := uuid.Parse(userIDStr)
	if err != nil {
		return cal, actor, httpErr(apierrors.CalendarMemberUserIdMalformed)
	}
	targetUserID, err := deps.Queries.FindUserInternalIdByPublicId(ctx, types.FromUUID(targetUID))
	if err != nil {
		return cal, actor, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarMemberNotFound, apierrors.CalendarMemberStoreReadInterrupted))
	}
	if targetUserID != actorID && roleRank(actor.Role) < roleRank(calendar.CalendarMembersRoleManager) {
		return cal, actor, errRoleFloor(calendar.CalendarMembersRoleManager)
	}
	target := actor
	if targetUserID != actorID {
		target, err = deps.CalendarQueries.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
			CalendarID: cal.ID,
			UserID:     targetUserID,
		})
		if err != nil {
			return cal, target, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarMemberNotFound, apierrors.CalendarMemberStoreReadInterrupted))
		}
	}
	return cal, target, nil
}

// UpdateMemberColor sets a member's colour as sent. Sending the colour the
// member already has answers 200 without writing or recording anything.
func UpdateMemberColor(deps Deps) func(context.Context, *UpdateMemberColorInput) (*UpdateMemberColorOutput, error) {
	return func(ctx context.Context, input *UpdateMemberColorInput) (*UpdateMemberColorOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, target, err := resolveMemberColorWriter(ctx, deps, wsID, actorID, input.CalID, input.UserID)
		if err != nil {
			return nil, err
		}

		changed := target.MemberColor != input.Body.Color
		if changed {
			if err := deps.CalendarQueries.UpdateCalendarMemberColor(ctx, calendar.UpdateCalendarMemberColorParams{
				MemberColor: input.Body.Color,
				CalendarID:  cal.ID,
				UserID:      target.UserID,
			}); err != nil {
				return nil, httpErr(apierrors.CalendarMemberStoreWriteInterrupted)
			}
		}

		rows, err := deps.CalendarQueries.ListCalendarMembers(ctx, calendar.ListCalendarMembersParams{
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberListQueryInterrupted)
		}
		var resp MemberResponse
		found := false
		for _, r := range rows {
			if strings.EqualFold(r.UserPublicID.String(), input.UserID) {
				resp, found = memberFromRow(r), true
				break
			}
		}
		if !found {
			return nil, httpErr(apierrors.CalendarMemberNotFound)
		}

		if changed {
			recordCalendarChange(ctx, deps, wsID, cal.ID, actorID, mutationlog.Mutation{
				EventType:    sharedbus.CalMemberUpdated,
				AuditAction:  "calendar.member.color.update",
				ResourceType: "calendar.member",
				ResourceID:   resp.UserID,
				Payload: map[string]any{
					"calendarId": input.CalID,
					"userId":     resp.UserID,
					"summary":    resp.DisplayName,
				},
				CallSite: "calendars.UpdateMemberColor",
			})
		}
		return &UpdateMemberColorOutput{Body: resp}, nil
	}
}
