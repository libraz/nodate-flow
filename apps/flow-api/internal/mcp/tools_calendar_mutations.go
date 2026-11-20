package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
)

func runCreateCalendarEvent(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		CalendarID  string `json:"calendarId"`
		Title       string `json:"title"`
		StartAt     *int64 `json:"startAt"`
		EndAt       *int64 `json:"endAt"`
		StartDate   string `json:"startDate"`
		EndDate     string `json:"endDate"`
		Kind        string `json:"kind"`
		ShowAs      string `json:"showAs"`
		Flexibility string `json:"flexibility"`
		Visibility  string `json:"visibility"`
		OwnerUserID string `json:"ownerUserId"`
		AllDay      *bool  `json:"allDay"`
		Location    string `json:"location"`
		Memo        string `json:"memo"`
		BlockLabel  string `json:"blockLabel"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.CalendarID == "" || in.Title == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	calID, err := resolveCalendarWrite(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	allDay := false
	if in.AllDay != nil {
		allDay = *in.AllDay
	}
	var startAt, endAt time.Time
	if allDay {
		if in.StartDate == "" || in.EndDate == "" {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "all-day events require startDate and endDate")
		}
		startAt, err = parseDayBoundary(in.StartDate)
		if err != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid startDate: %v", err)
		}
		endAt, err = parseDayBoundary(in.EndDate)
		if err != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid endDate: %v", err)
		}
	} else {
		if in.StartAt == nil || in.EndAt == nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "timed events require startAt and endAt (unix seconds)")
		}
		startAt = time.Unix(*in.StartAt, 0).UTC()
		endAt = time.Unix(*in.EndAt, 0).UTC()
	}
	// The window is checked before it is normalised, and through the same
	// rule the REST create applies: an agent that inverts it is told which
	// way round it goes, instead of being handed the CHECK constraint's
	// refusal as an unexplained execution failure.
	if rangeErr := calendarrules.RequireEventChronology(
		calendarrules.UnixSeconds(startAt), calendarrules.UnixSeconds(endAt)); rangeErr != nil {
		return nil, rangeErr
	}
	// An all-day row is stored as UTC midnight on the author's date.
	// parseDayBoundary already lands there, so this changes nothing today
	// — it is here because the canonical form is one decision for every
	// transport, and reading it off the shared rule is what stops the two
	// from drifting apart the way they did before.
	normStart, normEnd := calendarrules.NormalizeAllDayBounds(allDay,
		sql.NullTime{Time: startAt, Valid: true}, sql.NullTime{Time: endAt, Valid: true})
	startAt, endAt = normStart.Time, normEnd.Time

	kind := calendar.CalendarEventsKindEvent
	if in.Kind != "" {
		kind = calendar.CalendarEventsKind(in.Kind)
	}
	showAs := calendar.CalendarEventsShowAsBusy
	if in.ShowAs != "" {
		showAs = calendar.CalendarEventsShowAs(in.ShowAs)
	}
	// Default to 'fixed'. An agent that says nothing about movability has
	// not been told the event can move, and guessing otherwise would offer
	// up the owner's time on their behalf.
	flexibility := calendar.CalendarEventsFlexibilityFixed
	if in.Flexibility != "" {
		flexibility = calendar.CalendarEventsFlexibility(in.Flexibility)
	}
	visibility := calendar.CalendarEventsVisibilityDefault
	if in.Visibility != "" {
		visibility = calendar.CalendarEventsVisibility(in.Visibility)
	}

	ownerUserID := s.userID
	if in.OwnerUserID != "" {
		// Resolve the target user by public id, scoped to workspace
		// membership so a non-member cannot be assigned as owner.
		resolved, uerr := resolveWorkspaceUser(ctx, deps, s, in.OwnerUserID, apierrors.CalendarEventOwnerUserNotFound)
		if uerr != nil {
			return nil, uerr
		}
		// Same rule and same refusal REST gives: delegation takes a
		// manager or owner role on the calendar, and filing an event
		// under yourself takes nothing.
		allowed, aerr := canSetCalendarEventOwner(ctx, deps, s, resolved, calID)
		if aerr != nil {
			return nil, aerr
		}
		if !allowed {
			return nil, apierrors.New(apierrors.CalendarEventEditPermissionRequired)
		}
		ownerUserID = resolved
	}

	// The stored zone is the one the event's day is read in, so it is
	// resolved for the workspace rather than assumed — a row stamped UTC
	// is a day only at offset zero, and on an Asia/Tokyo grid it lands
	// nine hours from where the agent put it. The chain and the actor are
	// the ones REST resolves against: the caller, not the owner named.
	zone := resolveUserTimezone(ctx, deps, s.workspaceID, s.userID)

	pub := newPublicID()
	calID64 := int64(calID)
	if err := dbretry.InTx(ctx, deps.DB, "mcp.create_calendar_event", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, err := deps.CalendarQueries.WithTx(tx.RawTx()).CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
			PublicID:           pub,
			WorkspaceID:        s.workspaceID,
			CalendarID:         calID,
			Kind:               kind,
			Visibility:         visibility,
			ShowAs:             showAs,
			Flexibility:        flexibility,
			Title:              in.Title,
			AllDay:             allDay,
			StartAt:            sql.NullTime{Time: startAt, Valid: true},
			EndAt:              sql.NullTime{Time: endAt, Valid: true},
			Timezone:           zone.Name(),
			Location:           sql.NullString{String: in.Location, Valid: in.Location != ""},
			Memo:               sql.NullString{String: in.Memo, Valid: in.Memo != ""},
			Url:                sql.NullString{},
			OwnerUserID:        ownerUserID,
			CreatedByUserID:    s.userID,
			BlockLabel:         sql.NullString{String: in.BlockLabel, Valid: in.BlockLabel != ""},
			RecurrenceRule:     nil,
			RecurrenceEnd:      sql.NullTime{},
			NotificationOffset: sql.NullInt32{},
			TaskID:             sql.NullInt32{},
		}); err != nil {
			return err
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:       eventbus.CalEventCreated,
			SubjectPublicID: pub,
			AuditAction:     "calendar.event.create",
			ResourceType:    "calendar.event",
			ResourceID:      pub.String(),
			CalendarID:      &calID64,
			Payload: map[string]any{
				"eventId":    pub.String(),
				"calendarId": in.CalendarID,
				"title":      in.Title,
				"kind":       string(kind),
				"startAt":    startAt.Unix(),
				"endAt":      endAt.Unix(),
				"via":        "mcp",
			},
			CallSite: "mcp.create_calendar_event",
		})
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	out := map[string]any{
		"id":          pub.String(),
		"title":       in.Title,
		"kind":        string(kind),
		"showAs":      string(showAs),
		"flexibility": string(flexibility),
		"allDay":      allDay,
	}
	if allDay {
		out["startDate"] = startAt.UTC().Format("2006-01-02")
		out["endDate"] = endAt.UTC().Format("2006-01-02")
	} else {
		out["startAt"] = startAt.Unix()
		out["endAt"] = endAt.Unix()
	}
	return out, nil
}

// updateCalendarEventArgs is what update_calendar_event takes.
//
// It is a named type rather than an anonymous struct because the
// occurrence-scoped paths fold the same arguments over a different base
// row, so both readings of one call live in one declaration.
type updateCalendarEventArgs struct {
	EventID     string  `json:"eventId"`
	Title       *string `json:"title"`
	StartAt     *int64  `json:"startAt"`
	EndAt       *int64  `json:"endAt"`
	StartDate   *string `json:"startDate"`
	EndDate     *string `json:"endDate"`
	Kind        *string `json:"kind"`
	ShowAs      *string `json:"showAs"`
	Flexibility *string `json:"flexibility"`
	Visibility  *string `json:"visibility"`
	Location    *string `json:"location"`
	Memo        *string `json:"memo"`
	BlockLabel  *string `json:"blockLabel"`
	// Scope and OccurrenceStart say which occurrences of a recurring
	// series the update reaches, and which one it starts from. An empty
	// Scope is the whole series.
	Scope           string `json:"scope"`
	OccurrenceStart int64  `json:"occurrenceStart"`
}

func runUpdateCalendarEvent(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in updateCalendarEventArgs
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.EventID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	eventPub, err := types.Parse(in.EventID)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid eventId")
	}
	owner, err := deps.CalendarQueries.FindCalendarEventOwner(ctx, calendar.FindCalendarEventOwnerParams{
		PublicID:    eventPub,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.CalendarEventNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	// Resolve event internal id for permission check.
	evt, err := deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
		PublicID:    eventPub,
		CalendarID:  owner.CalendarID,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	if err := requireCalendarWritable(ctx, deps, s, owner.CalendarID); err != nil {
		return nil, err
	}
	// A failed lookup is not a decision about the caller's authority, so
	// it answers as the server failure it is; only a completed check that
	// says no is a refusal. REST refuses the same event with the same
	// spec, so the two transports disclose the same thing.
	ok, err := canEditCalendarEvent(ctx, deps, s, owner.OwnerUserID, evt.ID, owner.CalendarID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierrors.New(apierrors.CalendarEventEditPermissionRequired)
	}

	scope, err := decodeOccurrenceScope(in.Scope)
	if err != nil {
		return nil, err
	}
	if scope != scopeSeries {
		return updateCalendarEventOccurrences(ctx, deps, s, scope, owner.CalendarID, eventPub, evt, &in)
	}

	params := calendar.PatchCalendarEventParams{
		PublicID:    eventPub,
		CalendarID:  owner.CalendarID,
		WorkspaceID: s.workspaceID,
	}

	isLinked := evt.TaskID.Valid
	titleChanged := in.Title != nil && *in.Title != evt.Title

	// A linked event shares its title with the task, so the rename below
	// writes tasks.title and the title faces the task rule. Resolved
	// before the transaction so a refusal answers as bad arguments
	// rather than surfacing from inside a retryable unit of work.
	var renameTitle taskrules.Title
	if isLinked && titleChanged {
		var terr error
		renameTitle, terr = taskrules.NewTitle(*in.Title)
		if terr != nil {
			return nil, translateTaskRuleError(terr)
		}
	}

	var newStartAt, newEndAt time.Time
	timeChanged := false
	switch {
	case in.StartAt != nil:
		newStartAt = time.Unix(*in.StartAt, 0).UTC()
		timeChanged = true
	case in.StartDate != nil && *in.StartDate != "":
		t, perr := parseDayBoundary(*in.StartDate)
		if perr != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid startDate")
		}
		newStartAt = t
		timeChanged = true
	}
	switch {
	case in.EndAt != nil:
		newEndAt = time.Unix(*in.EndAt, 0).UTC()
		timeChanged = true
	case in.EndDate != nil && *in.EndDate != "":
		t, perr := parseDayBoundary(*in.EndDate)
		if perr != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid endDate")
		}
		newEndAt = t
		timeChanged = true
	}
	// When only one side of the time window arrived, fall back to the
	// stored value so itemkit / PatchCalendarEvent receive a valid pair.
	if timeChanged {
		if newStartAt.IsZero() && evt.StartAt.Valid {
			newStartAt = evt.StartAt.Time
		}
		if newEndAt.IsZero() && evt.EndAt.Valid {
			newEndAt = evt.EndAt.Time
		}
		// The fall-back above answers "the caller moved one end of a
		// window that already had two". On an undated event there is no
		// other end to borrow, and writing the half pair reached the
		// column as a zero instant the driver rejects. The REST patch
		// refuses that request by name, so this one does too.
		if pairErr := calendarrules.RequireEventStartEndPair(
			calendarrules.UnixSeconds(newStartAt), calendarrules.UnixSeconds(newEndAt)); pairErr != nil {
			return nil, pairErr
		}
		// Both branches below write this pair — the standalone patch and
		// the itemkit reschedule — so the ordering is checked once, here,
		// through the rule the REST patch applies. Without it an inverted
		// window reached chk_calendar_events_chronology and came back as
		// an execution failure that named nothing.
		if rangeErr := calendarrules.RequireEventChronology(
			calendarrules.UnixSeconds(newStartAt), calendarrules.UnixSeconds(newEndAt)); rangeErr != nil {
			return nil, rangeErr
		}
		// The stored row decides whether the pair is a date or an instant:
		// this tool takes no allDay argument, so moving an all-day event by
		// startAt would otherwise store an off-midnight instant and put the
		// event on a different square for readers in another zone.
		normStart, normEnd := calendarrules.NormalizeAllDayBounds(evt.AllDay,
			sql.NullTime{Time: newStartAt, Valid: true}, sql.NullTime{Time: newEndAt, Valid: true})
		newStartAt, newEndAt = normStart.Time, normEnd.Time
	}
	if in.Title != nil {
		params.Title = sql.NullString{String: *in.Title, Valid: true}
	}
	if timeChanged {
		params.StartAt = sql.NullTime{Time: newStartAt, Valid: true}
		params.EndAt = sql.NullTime{Time: newEndAt, Valid: true}
	}
	// The row as it will stand, refused here by the rule REST applies
	// rather than by the CHECK that would answer it as a store failure.
	shapeKind := string(evt.Kind)
	if in.Kind != nil {
		shapeKind = *in.Kind
	}
	if refusal := calendarrules.RequireEventShape(shapeKind, timeChanged || evt.StartAt.Valid,
		calendaroccurrence.HasRule(evt.RecurrenceRule), evt.NotificationOffset.Valid); refusal != nil {
		return nil, refusal
	}
	if in.Kind != nil {
		params.Kind = calendar.NullCalendarEventsKind{
			CalendarEventsKind: calendar.CalendarEventsKind(*in.Kind),
			Valid:              true,
		}
	}
	if in.ShowAs != nil {
		params.ShowAs = calendar.NullCalendarEventsShowAs{
			CalendarEventsShowAs: calendar.CalendarEventsShowAs(*in.ShowAs),
			Valid:                true,
		}
	}
	if in.Flexibility != nil {
		params.Flexibility = calendar.NullCalendarEventsFlexibility{
			CalendarEventsFlexibility: calendar.CalendarEventsFlexibility(*in.Flexibility),
			Valid:                     true,
		}
	}
	if in.Visibility != nil {
		params.Visibility = calendar.NullCalendarEventsVisibility{
			CalendarEventsVisibility: calendar.CalendarEventsVisibility(*in.Visibility),
			Valid:                    true,
		}
	}
	// An empty string clears the column to NULL, the state REST's clear
	// list reaches.
	clears := eventFieldClears(&in)
	switch {
	case clears.Location:
		params.ClearLocation = 1
	case in.Location != nil:
		params.Location = sql.NullString{String: *in.Location, Valid: true}
	}
	switch {
	case clears.Memo:
		params.ClearMemo = 1
	case in.Memo != nil:
		params.Memo = sql.NullString{String: *in.Memo, Valid: true}
	}
	switch {
	case clears.BlockLabel:
		params.ClearBlockLabel = 1
	case in.BlockLabel != nil:
		params.BlockLabel = sql.NullString{String: *in.BlockLabel, Valid: true}
	}

	// A start moved on a recurring master moves every occurrence, so the
	// overrides and exceptions naming them are realigned in the same
	// transaction, through the transform REST's series PATCH uses.
	realignSeries := calendaroccurrence.HasRule(evt.RecurrenceRule) &&
		timeChanged && !newStartAt.Equal(evt.StartAt.Time)

	if !isLinked || (!titleChanged && !timeChanged) {
		calID64 := int64(owner.CalendarID)
		eventTitle := evt.Title
		if in.Title != nil {
			eventTitle = *in.Title
		}
		if err := dbretry.InTx(ctx, deps.DB, "mcp.update_calendar_event", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if err := patchCalendarSeries(ctx, deps, tx, s, evt, params, realignSeries); err != nil {
				return err
			}
			// This branch never reaches itemkit, so nothing else appends
			// the event.
			return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
				EventType:       eventbus.CalEventUpdated,
				SubjectPublicID: eventPub,
				AuditAction:     "calendar.event.update",
				ResourceType:    "calendar.event",
				ResourceID:      eventPub.String(),
				CalendarID:      &calID64,
				Payload: map[string]any{
					"eventId": eventPub.String(),
					"title":   eventTitle,
					"via":     "mcp",
				},
				CallSite: "mcp.update_calendar_event",
			})
		}); err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		return map[string]any{"success": true}, nil
	}

	// itemkit owns the title and time columns for a linked event, so they
	// drop out of the remaining-fields patch. Decided here rather than
	// inside the transaction because the transaction is retried on a
	// deadlock and would otherwise re-read params its own first attempt
	// had already emptied.
	if titleChanged {
		params.Title = sql.NullString{}
	}
	if timeChanged {
		params.StartAt = sql.NullTime{}
		params.EndAt = sql.NullTime{}
	}
	// Only run the remaining-fields patch if anything is still set. The
	// clearable columns arrive as a bare any rather than a typed Null*,
	// because their SET expression wraps the COALESCE in an IF that
	// reads the matching clear flag; nil is the absent case.
	patchRemaining := params.Title.Valid || params.Kind.Valid || params.ShowAs.Valid ||
		params.Flexibility.Valid || params.Visibility.Valid ||
		params.Location != nil || params.Memo != nil || params.BlockLabel != nil ||
		params.ClearLocation == 1 || params.ClearMemo == 1 || params.ClearBlockLabel == 1 ||
		params.StartAt.Valid || params.EndAt.Valid

	// answered holds a response-shaped error decided inside the
	// transaction, so an itemkit invariant is not reported as a generic
	// tool-execution failure.
	var answered error
	// renamed carries the linked task's text out of the transaction so
	// its search embedding is refreshed once the rename has committed.
	var renamed itemkit.RenamedTask
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.update_calendar_event", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		renamed = itemkit.RenamedTask{}

		if titleChanged {
			var rerr error
			renamed, rerr = itemkit.RenameItem(ctx, tx, itemkit.RenameItemArgs{
				WorkspaceID: s.workspaceID,
				ActorUserID: s.userID,
				EventID:     evt.ID,
				NewTitle:    renameTitle,
			})
			if rerr != nil {
				answered = translateItemkitMCPError(rerr, apierrors.CalendarEventNotFound)
				return rerr
			}
		}
		if timeChanged {
			if err := itemkit.RescheduleEvent(ctx, tx, itemkit.RescheduleEventArgs{
				WorkspaceID: s.workspaceID,
				EventID:     evt.ID,
				ActorUserID: s.userID,
				StartAt:     newStartAt,
				EndAt:       newEndAt,
			}); err != nil {
				answered = translateItemkitMCPError(err, apierrors.CalendarEventNotFound)
				return err
			}
		}
		if patchRemaining || realignSeries {
			if err := patchCalendarSeries(ctx, deps, tx, s, evt, params, realignSeries); err != nil {
				return err
			}
		}
		return nil
	})
	if answered != nil {
		return nil, answered
	}
	if txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}
	// A linked event's title is also the task's, so renaming through the
	// event moves the text search is served from. Past the commit and
	// ahead of the audit record: the write is no longer revocable, and
	// the audit call below can leave this function.
	if renamed.TaskID != 0 {
		embed.RefreshTaskAfterCommit(ctx, deps.Embedder, s.workspaceID, renamed.TaskID, renamed.Title, renamed.Description)
	}
	// itemkit appended calendar.event.updated inside the transaction that
	// just committed, so only the audit half is left to add here.
	recorder(deps).RecordTxAudit(ctx, mutationActor(s), mutationlog.Mutation{
		AuditAction:  "calendar.event.update",
		ResourceType: "calendar.event",
		ResourceID:   eventPub.String(),
		Payload: map[string]any{
			"eventId": eventPub.String(),
			"via":     "mcp",
		},
		CallSite: "mcp.update_calendar_event",
	})
	return map[string]any{"success": true}, nil
}

// patchCalendarSeries writes a whole-series patch and, when realign is set,
// brings the master's overrides and exceptions along to where its
// occurrences now fall, through [calendaroccurrence.RealignSeries]. The
// series columns are read under the row lock first, so the realignment
// starts from what is committed.
func patchCalendarSeries(
	ctx context.Context,
	deps Deps,
	tx *dbretry.Tx,
	s *session,
	evt calendar.FindCalendarEventByPublicIdRow,
	params calendar.PatchCalendarEventParams,
	realign bool,
) error {
	q := seriesQueries(deps, tx)
	var before calendaroccurrence.SeriesState
	if realign {
		locked, err := q.Calendar.LockCalendarEventSeries(ctx, calendar.LockCalendarEventSeriesParams{
			ID:          evt.ID,
			WorkspaceID: s.workspaceID,
		})
		if err != nil {
			return err
		}
		before = calendaroccurrence.SeriesStateOf(evt)
		before.Rule = locked.RecurrenceRule
		before.End = locked.RecurrenceEnd
		before.Exceptions = locked.RecurrenceExceptions
	}
	// Not an existence check: a patch carrying the event's current values
	// reports zero. The event was read before the transaction.
	if _, err := q.Calendar.PatchCalendarEvent(ctx, params); err != nil {
		return err
	}
	if !realign {
		return nil
	}
	updated, err := q.Calendar.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
		PublicID:    evt.PublicID,
		CalendarID:  evt.CalendarID,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return err
	}
	_, err = calendaroccurrence.RealignSeries(ctx, q, s.workspaceID, before, updated, false)
	return err
}

// runDeleteCalendarEvent soft-deletes a calendar event, or the part of a
// recurring series a non-series scope names.
//
// A scoped delete patches recurrence_exceptions or recurrence_end and
// leaves every other column to COALESCE, so the tool sends no window and
// no all-day flag of its own:
//
// calendar-precondition: chronology not-applicable — this tool sends no start and no end, so it has no window to order
// calendar-precondition: all-day-bounds not-applicable — this tool sends no all-day flag and no window, so it has no bounds to pin
//
// A series delete reaches itemkit, and the task column itemkit writes is
// cleared rather than set. A task with no due date has no ordering to
// break, which is the answer taskrules.DateOrder itself gives a NULL
// side:
//
// task-precondition: date-order not-applicable — this tool only clears due_on, and a task with no due date has no pair to order
func runDeleteCalendarEvent(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		EventID string `json:"eventId"`
		// Scope and OccurrenceStart say which occurrences of a recurring
		// series the delete reaches, and which one it starts from. An
		// empty Scope is the whole series.
		Scope           string `json:"scope"`
		OccurrenceStart int64  `json:"occurrenceStart"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.EventID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	eventPub, err := types.Parse(in.EventID)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid eventId")
	}
	owner, err := deps.CalendarQueries.FindCalendarEventOwner(ctx, calendar.FindCalendarEventOwnerParams{
		PublicID:    eventPub,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.CalendarEventNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	evt, err := deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
		PublicID:    eventPub,
		CalendarID:  owner.CalendarID,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	if err := requireCalendarWritable(ctx, deps, s, owner.CalendarID); err != nil {
		return nil, err
	}
	ok, err := canEditCalendarEvent(ctx, deps, s, owner.OwnerUserID, evt.ID, owner.CalendarID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierrors.New(apierrors.CalendarEventEditPermissionRequired)
	}

	scope, err := decodeOccurrenceScope(in.Scope)
	if err != nil {
		return nil, err
	}
	if scope != scopeSeries {
		return deleteCalendarEventOccurrences(ctx, deps, s, scope, owner.CalendarID, eventPub, evt, in.OccurrenceStart)
	}

	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.delete_calendar_event", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		// Deleting a moved occurrence on its own leaves the cancellation on
		// the master, or the master produces the occurrence again at its
		// original slot.
		if err := calendaroccurrence.CancelOverride(ctx, seriesQueries(deps, tx), s.workspaceID, evt); err != nil {
			return err
		}
		if err := itemkit.DeleteEvent(ctx, tx, s.workspaceID, evt.ID, s.userID); err != nil {
			answered = translateItemkitMCPError(err, apierrors.CalendarEventNotFound)
			return err
		}
		return nil
	})
	if answered != nil {
		return nil, answered
	}
	if txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}
	// itemkit appended item.unscheduled and the legacy
	// calendar.event.deleted inside the committed transaction; the audit
	// row is the remaining half.
	recorder(deps).RecordTxAudit(ctx, mutationActor(s), mutationlog.Mutation{
		AuditAction:  "calendar.event.delete",
		ResourceType: "calendar.event",
		ResourceID:   eventPub.String(),
		Payload: map[string]any{
			"eventId": eventPub.String(),
			"via":     "mcp",
		},
		CallSite: "mcp.delete_calendar_event",
	})
	return map[string]any{"success": true}, nil
}

// workDayStartHour and workDayEndHour bound the window list_free_slots
// searches. They are a stand-in for a real per-user working-hours
// setting, which does not exist yet; naming them makes the assumption
// visible rather than leaving two literals in the middle of a function.
const (
	workDayStartHour = 9
	workDayEndHour   = 18
)

// runCreateEventFromTask projects a task onto a calendar as a timed
// event.
//
// The write goes through itemkit.ScheduleTask, the projection engine,
// because a calendar event carrying a task link is a projection row:
// calendar_events reserves task_id, task_role, title, start_at, end_at
// and enabled on such a row for the engine, and a trigger refuses any
// direct insert that sets task_id. The engine owns the columns the
// calendar write preconditions govern, so they are answered where it
// writes rather than here.
func runCreateEventFromTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID     string `json:"taskId"`
		CalendarID string `json:"calendarId"`
		StartAt    *int64 `json:"startAt"`
		EndAt      *int64 `json:"endAt"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.TaskID == "" || in.CalendarID == "" || in.StartAt == nil || in.EndAt == nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	// The created event carries calendar_events.task_id, i.e. it links itself
	// to the task. REST reaches the same end state with two calls, the second
	// of which (tasks-event-links-create) is project-editor gated, so the
	// editor floor applies here too on top of the calendar_members grant.
	taskInternal, task, err := resolveTaskRowForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	calID, err := resolveCalendarWrite(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	startAt := time.Unix(*in.StartAt, 0).UTC()
	endAt := time.Unix(*in.EndAt, 0).UTC()
	// This tool takes a window the caller chose, which the REST route it
	// answers for does not — that one derives the window from the task's
	// due date. Taking the window means owing the same refusal the event
	// routes give when it is inverted.
	if rangeErr := calendarrules.RequireEventChronology(
		calendarrules.UnixSeconds(startAt), calendarrules.UnixSeconds(endAt)); rangeErr != nil {
		return nil, rangeErr
	}

	// The projected event is read on the same grid as one created
	// directly, so its zone is resolved for the workspace rather than
	// assumed: a day built in UTC is a day only at offset zero.
	zone := resolveUserTimezone(ctx, deps, s.workspaceID, s.userID)

	// RoleDue is the only role a task-projected event may hold, and the
	// call is idempotent in it: a task already projected onto a calendar
	// is moved rather than doubled, which is what the REST counterpart
	// does too. Snap is left at its zero value, as there: this path
	// resolves no working-day configuration, so no badge is applied and
	// the window the caller chose is the window written.
	var eventPub types.PublicID
	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.create_event_from_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		pub, _, err := itemkit.ScheduleTask(ctx, tx, itemkit.ScheduleTaskArgs{
			WorkspaceID: s.workspaceID,
			TaskID:      taskInternal,
			CalendarID:  calID,
			ActorUserID: s.userID,
			Role:        itemkit.RoleDue,
			Title:       task.Title,
			StartAt:     startAt,
			EndAt:       endAt,
			Timezone:    zone.Name(),
		})
		if err != nil {
			answered = translateItemkitMCPError(err, apierrors.WsTaskNotFound)
			return err
		}
		eventPub = pub
		return nil
	})
	if answered != nil {
		return nil, answered
	}
	if txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}

	// itemkit appended item.scheduled and its legacy calendar.event.created
	// inside the transaction that just committed, so only the audit half is
	// left. No calendar id is named: the engine files through an event type
	// that carries none, so there is nothing here that a per-calendar feed
	// could be pointed at.
	recorder(deps).RecordTxAudit(ctx, mutationActor(s), mutationlog.Mutation{
		AuditAction:  "calendar.event.create",
		ResourceType: "calendar.event",
		ResourceID:   eventPub.String(),
		Payload: map[string]any{
			"eventId":    eventPub.String(),
			"calendarId": in.CalendarID,
			"taskId":     task.PublicID.String(),
			"title":      task.Title,
			"startAt":    startAt.Unix(),
			"endAt":      endAt.Unix(),
			"via":        "mcp:create_event_from_task",
		},
		CallSite: "mcp.create_event_from_task",
	})
	return map[string]any{
		"id":      eventPub.String(),
		"title":   task.Title,
		"startAt": startAt.Unix(),
		"endAt":   endAt.Unix(),
		"taskId":  task.PublicID.String(),
	}, nil
}
func runToggleCalendarMemo(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		MemoID     string `json:"memoId"`
		CalendarID string `json:"calendarId"`
		Done       *bool  `json:"done"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.MemoID == "" || in.CalendarID == "" || in.Done == nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	calID, err := resolveCalendarWrite(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	memoPub, err := types.Parse(in.MemoID)
	if err != nil {
		return nil, apierrors.New(apierrors.CalendarMemoNotFound)
	}
	// The update's row count is not an existence check: toggling a memo to
	// the done state it already holds counts zero. Resolve the memo first,
	// scoped to the calendar and workspace, as the REST patch does.
	if _, err := deps.CalendarQueries.FindCalendarMemoByPublicId(ctx, calendar.FindCalendarMemoByPublicIdParams{
		PublicID:    memoPub,
		CalendarID:  calID,
		WorkspaceID: s.workspaceID,
	}); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.CalendarMemoNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	calID64 := int64(calID)
	if err := dbretry.InTx(ctx, deps.DB, "mcp.toggle_calendar_memo", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, err := deps.CalendarQueries.WithTx(tx.RawTx()).UpdateCalendarMemo(ctx, calendar.UpdateCalendarMemoParams{
			Done:        sql.NullBool{Bool: *in.Done, Valid: true},
			PublicID:    memoPub,
			CalendarID:  calID,
			WorkspaceID: s.workspaceID,
		}); err != nil {
			return err
		}
		// Same kind and action the REST memo patch uses, so a shared memo
		// ticked off by an agent reads the same on the timeline as one
		// ticked off in the web app.
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:       eventbus.CalMemoUpdated,
			SubjectPublicID: memoPub,
			AuditAction:     "calendar.memo.update",
			ResourceType:    "calendar.memo",
			ResourceID:      in.MemoID,
			CalendarID:      &calID64,
			Payload: map[string]any{
				"memoId":     in.MemoID,
				"calendarId": in.CalendarID,
				"done":       *in.Done,
				"via":        "mcp",
			},
			CallSite: "mcp.toggle_calendar_memo",
		})
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"success": true}, nil
}
