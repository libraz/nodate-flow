package calendars

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

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

// attachRefusedEveryCandidate reports whether a publish batch had
// candidates and published none of them.
//
// It is the complement of the attached > 0 test, and the two are
// disjoint on purpose: one batch leaves one record, and which of the two
// it is says whether anything reached the public URL. A request naming no
// events asked for nothing and leaves neither.
func attachRefusedEveryCandidate(attached, skipped int) bool {
	return attached == 0 && skipped > 0
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
			recordCalendarChange(ctx, deps, wsID, noOwningCalendar, actorID, mutationlog.Mutation{
				EventType:    eventbus.PublicShareEventsAttached,
				AuditAction:  "calendar.share.events_attach",
				ResourceType: "calendar.share",
				ResourceID:   input.ShareID,
				Payload: map[string]any{
					"shareId":  input.ShareID,
					"attached": attached,
					"skipped":  skipped,
				},
				CallSite: "calendars.AttachEventsToShare",
			})
		}

		// A batch that published nothing still happened. Every candidate
		// was refused — confidential, or drawn from a calendar the actor
		// holds less than editor on — and this endpoint's output is a URL
		// anyone on the internet can open, so the attempt is precisely
		// what an administrator investigating an exposure looks for.
		//
		// Audit only, and no writer owns an event for it: nothing was
		// published, so nothing belongs on the timeline.
		if attachRefusedEveryCandidate(attached, skipped) {
			recordCalendarAudit(ctx, deps, wsID, actorID, mutationlog.Mutation{
				AuditAction:  "calendar.share.events_attach_refused",
				ResourceType: "calendar.share",
				ResourceID:   input.ShareID,
				Payload: map[string]any{
					"shareId":   input.ShareID,
					"skipped":   skipped,
					"requested": len(input.Body.EventIDs),
				},
				CallSite: "calendars.AttachEventsToShare",
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

		recordCalendarChange(ctx, deps, wsID, noOwningCalendar, actorID, mutationlog.Mutation{
			EventType:    eventbus.PublicShareEventsReordered,
			AuditAction:  "calendar.share.events_reorder",
			ResourceType: "calendar.share",
			ResourceID:   input.ShareID,
			Payload: map[string]any{
				"shareId": input.ShareID,
				"count":   len(requested),
			},
			CallSite: "calendars.ReorderShareEvents",
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
		// Unlike the share's own lifecycle above, this one names a
		// calendar: the event being withdrawn belongs to exactly one, so
		// the change shows up on that calendar's activity feed.
		recordCalendarChange(ctx, deps, wsID, evt.CalendarID, actorID, mutationlog.Mutation{
			EventType:    eventbus.PublicShareEventDetached,
			AuditAction:  "calendar.share.event_detach",
			ResourceType: "calendar.share",
			ResourceID:   input.ShareID,
			Payload: map[string]any{
				"shareId": input.ShareID,
				"eventId": input.EvtID,
			},
			CallSite: "calendars.DetachEventFromShare",
		})
		out := &DetachEventFromShareOutput{}
		out.Body.Removed = true
		return out, nil
	}
}
