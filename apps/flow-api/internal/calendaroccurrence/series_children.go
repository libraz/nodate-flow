package calendaroccurrence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// carrySeriesChildren gives the continuing master of a split what the
// series carried: its attendees, the live invites addressed to them, its
// share publications, its checklist and its attachments.
//
// Attendees, publications, checklist items and attachments are copied,
// because the truncated master's occurrences still happened with them.
// Invites move, because an invite is a single token-bound grant per
// attendee and the link its recipient holds should answer for the
// occurrences still to come.
func carrySeriesChildren(ctx context.Context, q Queries, workspaceID, calendarID, oldID, newID uint32) error {
	attendees, err := q.Calendar.ListCalendarEventAttendees(ctx, calendar.ListCalendarEventAttendeesParams{
		EventID:     nullID(oldID),
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return err
	}
	attendeeOf := make(map[uint32]uint32, len(attendees))
	for _, a := range attendees {
		// Only an attendee who still qualifies is copied; a filtered one's
		// invite stays with the truncated master.
		if _, err := q.Core.FindWorkspaceMemberUserInternalIdByPublicId(ctx, generated.FindWorkspaceMemberUserInternalIdByPublicIdParams{
			WorkspaceID: workspaceID,
			PublicID:    a.UserPublicID,
		}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if _, err := q.Calendar.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
			CalendarID: calendarID,
			UserID:     a.UserID,
		}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		lastID, err := q.Calendar.CreateCalendarEventAttendee(ctx, calendar.CreateCalendarEventAttendeeParams{
			PublicID:    types.New(),
			WorkspaceID: workspaceID,
			EventID:     nullID(newID),
			UserID:      a.UserID,
			Rsvp:        a.Rsvp,
			CanEdit:     a.CanEdit,
		})
		if err != nil {
			return err
		}
		attendeeOf[a.ID] = rowID(lastID)
	}

	// Every page is read before any invite moves: a moved invite leaves
	// the old master's list, and paging over a shrinking list skips rows.
	var invites []calendar.ListCalendarEventInvitesForEventRow
	for offset := int32(0); ; offset += childPage {
		page, err := q.Calendar.ListCalendarEventInvitesForEvent(ctx, calendar.ListCalendarEventInvitesForEventParams{
			EventID: nullID(oldID),
			Limit:   childPage,
			Offset:  offset,
		})
		if err != nil {
			return err
		}
		invites = append(invites, page...)
		if len(page) < childPage {
			break
		}
	}
	for _, inv := range invites {
		if !inv.AttendeeID.Valid {
			continue
		}
		newAttendee, ok := attendeeOf[uint32(inv.AttendeeID.Int32)] //#nosec G115 -- internal row id read back from an INT UNSIGNED column
		if !ok {
			continue
		}
		if err := q.Calendar.ReassignCalendarEventInvite(ctx, calendar.ReassignCalendarEventInviteParams{
			NewEventID:    nullID(newID),
			NewAttendeeID: nullID(newAttendee),
			ID:            inv.ID,
			WorkspaceID:   workspaceID,
		}); err != nil {
			return err
		}
	}

	links, err := q.Calendar.ListCalendarEventShareLinks(ctx, calendar.ListCalendarEventShareLinksParams{
		WorkspaceID: workspaceID,
		EventID:     oldID,
	})
	if err != nil {
		return err
	}
	for _, l := range links {
		if _, err := q.Calendar.AttachEventToShare(ctx, calendar.AttachEventToShareParams{
			PublicID:    types.New(),
			WorkspaceID: workspaceID,
			ShareID:     l.ShareID,
			EventID:     newID,
			SortWeight:  l.SortWeight,
		}); err != nil {
			return err
		}
	}

	for offset := int32(0); ; offset += childPage {
		items, err := q.Calendar.ListCalendarChecklistItems(ctx, calendar.ListCalendarChecklistItemsParams{
			EventID:     oldID,
			WorkspaceID: workspaceID,
			Limit:       childPage,
			Offset:      offset,
		})
		if err != nil {
			return err
		}
		for _, it := range items {
			if err := q.Calendar.CopyCalendarChecklistItem(ctx, calendar.CopyCalendarChecklistItemParams{
				PublicID:        types.New(),
				WorkspaceID:     workspaceID,
				EventID:         newID,
				CreatedByUserID: it.CreatedByUserID,
				Title:           it.Title,
				Done:            it.Done,
				SortWeight:      it.SortWeight,
			}); err != nil {
				return err
			}
		}
		if len(items) < childPage {
			break
		}
	}

	refs, err := q.Calendar.ListCalendarEventAttachmentRefs(ctx, calendar.ListCalendarEventAttachmentRefsParams{
		WorkspaceID: workspaceID,
		EventID:     nullID(oldID),
	})
	if err != nil {
		return err
	}
	for _, r := range refs {
		if _, err := q.Calendar.CreateCalendarEventAttachment(ctx, calendar.CreateCalendarEventAttachmentParams{
			PublicID:        types.New(),
			WorkspaceID:     workspaceID,
			EventID:         nullID(newID),
			UploaderID:      r.UploaderID,
			StorageObjectID: r.StorageObjectID,
			Filename:        r.Filename,
		}); err != nil {
			return err
		}
		// Each copy is another referencing row; the blob outlives the
		// first of them to be deleted only if the count says so.
		if err := q.Core.IncrementStorageObjectRefCount(ctx, r.StorageObjectID); err != nil {
			return err
		}
	}
	return nil
}

// childPage is the page size the child copies read the paginated lists
// in.
const childPage = 500

// carryReminderClaims copies the reminder claims one row holds from a
// point onwards onto another row, for the instants keep accepts.
func carryReminderClaims(
	ctx context.Context,
	q Queries,
	workspaceID, fromID, toID uint32,
	from time.Time,
	keep func(time.Time) bool,
) error {
	claims, err := q.Calendar.ListCalendarEventReminderClaims(ctx, calendar.ListCalendarEventReminderClaimsParams{
		WorkspaceID: workspaceID,
		EventID:     fromID,
		FromStart:   from,
	})
	if err != nil {
		return err
	}
	for _, at := range claims {
		if !keep(at) {
			continue
		}
		if err := q.Calendar.CopyCalendarEventReminderClaim(ctx, calendar.CopyCalendarEventReminderClaimParams{
			WorkspaceID:     workspaceID,
			EventID:         toID,
			OccurrenceStart: at,
		}); err != nil {
			return err
		}
	}
	return nil
}

// OwnAttendees gives an override attendee rows of its own, so that a change
// to who attends one occurrence replaces the series' list instead of
// standing alone on it: an override with any attendee row stops reading
// its series'. When it has none yet, the series' enabled attendees are
// copied onto it as enabled rows; one that already has rows is left alone.
func OwnAttendees(ctx context.Context, q *calendar.Queries, workspaceID, overrideID, seriesID uint32) error {
	has, err := q.HasCalendarEventAttendeeRows(ctx, nullID(overrideID))
	if err != nil || has {
		return err
	}
	series, err := q.ListCalendarEventAttendees(ctx, calendar.ListCalendarEventAttendeesParams{
		EventID:     nullID(seriesID),
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return err
	}
	for _, a := range series {
		if _, err := q.CreateCalendarEventAttendee(ctx, calendar.CreateCalendarEventAttendeeParams{
			PublicID:    types.New(),
			WorkspaceID: workspaceID,
			EventID:     nullID(overrideID),
			UserID:      a.UserID,
			Rsvp:        a.Rsvp,
			CanEdit:     a.CanEdit,
		}); err != nil {
			return err
		}
	}
	return nil
}
