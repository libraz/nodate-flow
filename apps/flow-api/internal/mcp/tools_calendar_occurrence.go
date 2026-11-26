package mcp

import (
	"context"
	"database/sql"
	stderrors "errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarlabels"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/eventacl"
)

// The scope vocabulary, the fallback bases, the argument merge, the
// truncation point and the exception list all live in
// [github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence],
// because the REST calendar routes offer the same three scopes over the
// same rows. What is left in this package is the part that is a tool's:
// reading the scope out of a tool argument, resolving MCP's own
// startAt-or-startDate pair into an instant, and recording the mutation
// the call made.
//
// The names are re-declared as aliases rather than spelled out at every
// call site: the tools read the same as they did when the definitions
// were here, and there is still only one definition.
type occurrenceScope = calendaroccurrence.Scope

const (
	// scopeSeries acts on the master, and with it every occurrence the
	// rule produces. An omitted scope resolves here, so a caller that
	// does not send the field keeps the behaviour these tools always had.
	scopeSeries = calendaroccurrence.ScopeSeries
	// scopeOccurrence acts on exactly one occurrence and leaves the rest
	// of the series alone.
	scopeOccurrence = calendaroccurrence.ScopeOccurrence
	// scopeThisAndFollowing splits the series at an occurrence: the
	// master stops producing occurrences there, and the remainder carries
	// on separately.
	scopeThisAndFollowing = calendaroccurrence.ScopeThisAndFollowing
)

// occurrenceScopes is the closed set the tool schemas advertise. It is
// the shared set rather than a list of its own, so a value the tools
// accept and a value they publish cannot come apart, and neither can
// come apart from what the REST routes accept.
var occurrenceScopes = calendaroccurrence.Scopes

// decodeOccurrenceScope reads the scope a call names, defaulting to the
// whole series.
//
// The refusal is this transport's own because the value arrived as a tool
// argument here; the REST routes read the same scopes out of a body or a
// query string and name the member instead.
func decodeOccurrenceScope(raw string) (occurrenceScope, error) {
	scope, ok := calendaroccurrence.ParseScope(raw)
	if !ok {
		return "", apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid scope")
	}
	return scope, nil
}

// requireOccurrenceScope refuses the scope / row combinations that name
// no occurrence to act on.
//
// Each refusal carries the code REST answers for the same combination,
// because the reason is the part a caller can act on: a generic
// bad-argument answer says the call was rejected without saying whether
// the row repeats at all, or whether it already stands in for one
// occurrence. The member the shared decision names is dropped here —
// a tool answers with a code, not with a pointer into a document.
//
// occurrenceStart has no absent value of its own: zero stands for
// omitted, the same reading the REST delete route takes.
//
// REST additionally refuses recurrence columns written onto a leaf.
// There is no counterpart here because no MCP tool takes recurrenceRule,
// recurrenceEnd or recurrenceExceptions, so no call can put one there.
func requireOccurrenceScope(
	scope occurrenceScope,
	occurrenceStart int64,
	evt calendar.FindCalendarEventByPublicIdRow,
	parentID sql.NullInt32,
) error {
	if scope != calendaroccurrence.ScopeSeries {
		if err := calendarrules.RequireDatetime("occurrenceStart", &occurrenceStart); err != nil {
			return err
		}
	}
	if spec, _ := calendaroccurrence.ScopeRefusal(
		scope, occurrenceStart,
		calendaroccurrence.HasRule(evt.RecurrenceRule), parentID.Valid); spec != nil {
		return apierrors.New(spec)
	}
	return nil
}

// eventRecurrenceParent reads whether a row is itself an override
// standing in for one occurrence of some other series, which is one of
// the answers a non-series scope has to be refused on.
//
// It is asked only where the answer can change the outcome. A
// series-scoped call reaches no refusal that depends on it, so the
// common path carries no query for a question it never has.
func eventRecurrenceParent(
	ctx context.Context,
	deps Deps,
	s *session,
	eventPub types.PublicID,
) (sql.NullInt32, error) {
	link, err := deps.CalendarQueries.FindCalendarEventRecurrenceLink(ctx, calendar.FindCalendarEventRecurrenceLinkParams{
		WorkspaceID: s.workspaceID,
		PublicID:    eventPub,
	})
	if err != nil {
		return sql.NullInt32{}, apierrors.New(apierrors.CalendarEventStoreReadInterrupted)
	}
	return link.RecurrenceParentID, nil
}

// updateCalendarEventOccurrences carries an update_calendar_event call
// that names part of a recurring series rather than the whole of it.
//
// The trail names the event the caller addressed, so it reads as one
// entry per call; the scope, the occurrence and the id of the row that
// was actually written say where the change landed.
func updateCalendarEventOccurrences(
	ctx context.Context,
	deps Deps,
	s *session,
	scope occurrenceScope,
	calendarID uint32,
	eventPub types.PublicID,
	evt calendar.FindCalendarEventByPublicIdRow,
	in *updateCalendarEventArgs,
) (any, error) {
	parentID, err := eventRecurrenceParent(ctx, deps, s, eventPub)
	if err != nil {
		return nil, err
	}
	if err := requireOccurrenceScope(scope, in.OccurrenceStart, evt, parentID); err != nil {
		return nil, err
	}
	if err := requireDetailsWritable(ctx, deps, s, scope, calendarID, evt, in); err != nil {
		return nil, err
	}
	label, err := resolveEventLabelChange(ctx, deps, calendarID, in)
	if err != nil {
		return nil, err
	}

	// A task-linked event cannot be reached here: the projection guard
	// forbids a recurrence rule on a projected row, and the refusal above
	// requires one. So none of the itemkit propagation the series path
	// performs applies, and the write below is the whole change.
	originalStart := handlerutil.UnixToTime(in.OccurrenceStart)
	calID64 := int64(calendarID)
	eventTitle := evt.Title
	if in.Title != nil {
		eventTitle = *in.Title
	}
	// record commits the trail with the write, inside its transaction.
	var writtenPub types.PublicID
	record := func(ctx context.Context, tx *dbretry.Tx, written types.PublicID) error {
		writtenPub = written
		rec := recorder(deps)
		if err := rec.RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:       eventbus.CalEventUpdated,
			SubjectPublicID: eventPub,
			AuditAction:     "calendar.event.update",
			ResourceType:    "calendar.event",
			ResourceID:      eventPub.String(),
			CalendarID:      &calID64,
			Payload: map[string]any{
				"eventId":         eventPub.String(),
				"title":           eventTitle,
				"scope":           string(scope),
				"occurrenceStart": in.OccurrenceStart,
				"writtenEventId":  written.String(),
				"via":             "mcp",
			},
			CallSite: "mcp.update_calendar_event",
		}); err != nil {
			return err
		}
		if scope != scopeThisAndFollowing {
			return nil
		}
		// A split also brings a series into existence, and a subscriber
		// that learns only of the truncation never hears of the continuing
		// master.
		return rec.RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:       eventbus.CalEventCreated,
			SubjectPublicID: written,
			AuditAction:     "calendar.event.create",
			ResourceType:    "calendar.event",
			ResourceID:      written.String(),
			CalendarID:      &calID64,
			Payload: map[string]any{
				"eventId":      written.String(),
				"title":        eventTitle,
				"splitFromId":  eventPub.String(),
				"splitStartAt": in.OccurrenceStart,
				"via":          "mcp",
			},
			CallSite: "mcp.update_calendar_event",
		})
	}
	var scopeErr error
	if scope == scopeOccurrence {
		scopeErr = patchCalendarEventOccurrence(ctx, deps, s, calendarID, evt, in, originalStart, label, record)
	} else {
		scopeErr = patchCalendarEventFollowing(ctx, deps, s, calendarID, evt, in, originalStart, label, record)
	}
	if scopeErr != nil {
		return nil, scopeErr
	}
	// eventId is the row the write landed on: the override for one
	// occurrence, the continuing series for a split.
	return map[string]any{"success": true, "eventId": writtenPub.String()}, nil
}

// requireDetailsWritable refuses an update that sets or clears location or
// memo, or changes the label, when the caller cannot see the target row's
// details. There is no url argument, so those three are the whole set. It
// runs after the edit-permission check and ahead of label resolution, as in
// REST; an update touching only other fields passes without a lookup.
//
// The target is the override for an occurrence scope when one exists, else
// the series. Visibility is decided once, here.
func requireDetailsWritable(
	ctx context.Context,
	deps Deps,
	s *session,
	scope occurrenceScope,
	calendarID uint32,
	evt calendar.FindCalendarEventByPublicIdRow,
	in *updateCalendarEventArgs,
) error {
	if in.Location == nil && in.Memo == nil && in.LabelID == nil {
		return nil
	}
	target := evt
	if scope == scopeOccurrence {
		ov, err := deps.CalendarQueries.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
			WorkspaceID:             s.workspaceID,
			RecurrenceParentID:      handlerutil.NullInt32From(evt.ID),
			RecurrenceOriginalStart: sql.NullTime{Time: handlerutil.UnixToTime(in.OccurrenceStart), Valid: true},
		})
		switch {
		case err == nil:
			target, err = deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
				PublicID:    ov.PublicID,
				CalendarID:  ov.CalendarID,
				WorkspaceID: s.workspaceID,
			})
			if err != nil {
				return apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
			}
		case !stderrors.Is(err, sql.ErrNoRows):
			return apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
	}
	attendee, err := actorIsAttendee(ctx, deps, s, target)
	if err != nil {
		return err
	}
	calDefault, err := calendarDefaultVisibility(ctx, deps, s, calendarID)
	if err != nil {
		return err
	}
	visible := eventacl.CanSeeDetails(
		eventacl.Event{
			Visibility:      eventacl.Visibility(target.Visibility),
			OwnerUserID:     target.OwnerUserID,
			CreatedByUserID: target.CreatedByUserID,
			CalendarDefault: eventacl.Visibility(calDefault),
		},
		eventacl.Actor{UserID: s.userID, IsAttendee: attendee},
	)
	if refusal := calendarrules.RequireDetailsVisible(visible); refusal != nil {
		return refusal
	}
	return nil
}

// resolveEventLabelChange turns the labelId argument into a Change: the label
// must belong to the calendar, and an empty string clears. Whether the caller
// may change the label at all is [requireDetailsWritable]'s question, asked
// before this. A call without labelId resolves to ChangeKeep.
func resolveEventLabelChange(
	ctx context.Context,
	deps Deps,
	calendarID uint32,
	in *updateCalendarEventArgs,
) (calendarlabels.Change, error) {
	if in.LabelID == nil {
		return calendarlabels.Change{}, nil
	}
	if *in.LabelID == "" {
		return calendarlabels.Change{Mode: calendarlabels.ChangeClear}, nil
	}
	id, refusal, err := calendarlabels.ResolveLabel(ctx, deps.CalendarQueries, calendarID, *in.LabelID)
	if err != nil {
		return calendarlabels.Change{}, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	if refusal != nil {
		return calendarlabels.Change{}, refusal
	}
	return calendarlabels.Change{Mode: calendarlabels.ChangeSet, LabelID: id}, nil
}

// actorIsAttendee reports whether the session user is an attendee of the
// row. An override with no attendee rows of its own reads its series'.
func actorIsAttendee(ctx context.Context, deps Deps, s *session, row calendar.FindCalendarEventByPublicIdRow) (bool, error) {
	source := handlerutil.NullInt32From(row.ID)
	if row.RecurrenceParentID.Valid {
		own, err := deps.CalendarQueries.HasCalendarEventAttendeeRows(ctx, source)
		if err != nil {
			return false, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		if !own {
			source = row.RecurrenceParentID
		}
	}
	_, err := deps.CalendarQueries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
		EventID: source,
		UserID:  s.userID,
	})
	if stderrors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return true, nil
}

// calendarDefaultVisibility reads the calendar's default event visibility,
// which an event whose own visibility is 'default' resolves to. The calendar
// is found through the caller's own calendar list, the way
// [requireCalendarWritable] finds it.
func calendarDefaultVisibility(ctx context.Context, deps Deps, s *session, calendarID uint32) (calendar.CalendarsDefaultEventVisibility, error) {
	rows, err := deps.CalendarQueries.ListCalendarsForUser(ctx, calendar.ListCalendarsForUserParams{
		UserID:      s.userID,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return "", apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	for _, row := range rows {
		if row.ID != calendarID {
			continue
		}
		cal, err := deps.CalendarQueries.FindCalendarByPublicId(ctx, calendar.FindCalendarByPublicIdParams{
			PublicID:    row.PublicID,
			WorkspaceID: s.workspaceID,
		})
		if err != nil {
			return "", apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		return cal.DefaultEventVisibility, nil
	}
	return "", apierrors.New(apierrors.CalendarEventNotFound)
}

// labelRefusal returns the label refusal a transaction failed with, nil when
// it failed for any other reason. The link write answers it from inside the
// transaction, which hands it back wrapped.
func labelRefusal(err error) *apierrors.APIError {
	var ae *apierrors.APIError
	if stderrors.As(err, &ae) && ae.Spec == apierrors.CalendarEventLabelUnknown {
		return ae
	}
	return nil
}

// deleteCalendarEventOccurrences carries a delete_calendar_event call
// that names part of a recurring series rather than the whole of it.
func deleteCalendarEventOccurrences(
	ctx context.Context,
	deps Deps,
	s *session,
	scope occurrenceScope,
	calendarID uint32,
	eventPub types.PublicID,
	evt calendar.FindCalendarEventByPublicIdRow,
	occurrenceStart int64,
) (any, error) {
	parentID, err := eventRecurrenceParent(ctx, deps, s, eventPub)
	if err != nil {
		return nil, err
	}
	if err := requireOccurrenceScope(scope, occurrenceStart, evt, parentID); err != nil {
		return nil, err
	}

	start := handlerutil.UnixToTime(occurrenceStart)
	calID64 := int64(calendarID)
	// The event kind is the update one because that is what happened to
	// the row named: the master survives with an exception or an earlier
	// end, and a deleted-kind event carrying its id would tell every
	// subscriber to drop a row that is still there. The scope and the
	// occurrence say which part of the series went. The audit action
	// stays the delete one, because a delete is what the caller asked
	// for and what an administrator searches the log by.
	record := func(ctx context.Context, tx *dbretry.Tx) error {
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:       eventbus.CalEventUpdated,
			SubjectPublicID: eventPub,
			AuditAction:     "calendar.event.delete",
			ResourceType:    "calendar.event",
			ResourceID:      eventPub.String(),
			CalendarID:      &calID64,
			Payload: map[string]any{
				"eventId":         eventPub.String(),
				"title":           evt.Title,
				"scope":           string(scope),
				"occurrenceStart": occurrenceStart,
				"via":             "mcp",
			},
			CallSite: "mcp.delete_calendar_event",
		})
	}
	var partErr error
	if scope == scopeOccurrence {
		partErr = deleteCalendarEventOccurrence(ctx, deps, s, evt, start, record)
	} else {
		partErr = deleteCalendarEventFollowing(ctx, deps, s, calendarID, evt, start, record)
	}
	if partErr != nil {
		return nil, partErr
	}
	return map[string]any{"success": true}, nil
}

// updateWindow resolves the window an update names, against the row's own
// shape, and reports each end it was sent.
//
// An all-day row is named by dates and a timed one by instants, so the
// other form is refused. A date pair keeps the endDate the caller named as
// the last day, which is what a read returns, and stores the midnight after
// it; either end may be sent on its own, and the other is then taken from
// the row the write falls back to.
func updateWindow(in *updateCalendarEventArgs, allDay bool, kind string) (start, end *time.Time, err error) {
	sent := func(d *string) bool { return d != nil && *d != "" }
	if refusal := calendarrules.RequireAllDayInput(allDay,
		in.StartAt != nil || in.EndAt != nil, sent(in.StartDate) || sent(in.EndDate)); refusal != nil {
		return nil, nil, refusal
	}
	if !allDay {
		if in.StartAt != nil {
			t := time.Unix(*in.StartAt, 0).UTC()
			start = &t
		}
		if in.EndAt != nil {
			t := time.Unix(*in.EndAt, 0).UTC()
			end = &t
		}
		return start, end, nil
	}
	if sent(in.StartDate) {
		t, perr := parseDayBoundary(*in.StartDate)
		if perr != nil {
			return nil, nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid startDate")
		}
		start = &t
	}
	if sent(in.EndDate) {
		t, perr := parseDayBoundary(*in.EndDate)
		if perr != nil {
			return nil, nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid endDate")
		}
		t = calendarrules.AllDayEndExclusive(kind, t)
		end = &t
	}
	return start, end, nil
}

// occurrencePatch reads the arguments an update call sends into the
// shape the shared merge takes.
//
// It is the whole of what these tools contribute to the merge: which
// argument stands for which column, and that either end of the window
// may be sent on its own — the other is then taken from the occurrence
// the write falls back to.
//
// An empty location, memo or blockLabel is a clear, as on the series
// path: see [eventFieldClears]. A nullable column the call did not
// mention keeps whatever the base carries. There is no allDay, timezone,
// url or notificationOffset argument, so those columns are the base's too.
func occurrencePatch(in *updateCalendarEventArgs, allDay bool, kind string) (calendaroccurrence.Patch, error) {
	clears := eventFieldClears(in)
	p := calendaroccurrence.Patch{
		Kind:        in.Kind,
		Visibility:  in.Visibility,
		ShowAs:      in.ShowAs,
		Flexibility: in.Flexibility,
		Title:       in.Title,
		Clear:       clears,
	}
	if !clears.Location {
		p.Location = in.Location
	}
	if !clears.Memo {
		p.Memo = in.Memo
	}
	if !clears.BlockLabel {
		p.BlockLabel = in.BlockLabel
	}
	if in.Kind != nil {
		kind = *in.Kind
	}
	start, end, err := updateWindow(in, allDay, kind)
	if err != nil {
		return calendaroccurrence.Patch{}, err
	}
	p.StartAt = start
	p.EndAt = end
	return p, nil
}

// eventFieldClears reads which nullable event columns an update asks to
// empty. The tool has no clear list, so an empty string is the request:
// stored as NULL, the column reads back exactly as one REST cleared.
func eventFieldClears(in *updateCalendarEventArgs) calendaroccurrence.Clears {
	empty := func(v *string) bool { return v != nil && *v == "" }
	return calendaroccurrence.Clears{
		Location:   empty(in.Location),
		Memo:       empty(in.Memo),
		BlockLabel: empty(in.BlockLabel),
	}
}

// mergeOccurrenceFields folds the arguments the caller sent over the
// values the occurrence falls back to.
//
// A refusal from the shared rules leaves unchanged: it is already the
// error shape a tool answers with, so the REST routes' rendering step has
// no counterpart here.
func mergeOccurrenceFields(
	base calendaroccurrence.Fields,
	in *updateCalendarEventArgs,
) (calendaroccurrence.Fields, error) {
	patch, err := occurrencePatch(in, base.AllDay, string(base.Kind))
	if err != nil {
		return calendaroccurrence.Fields{}, err
	}
	fields, applyErr := patch.Apply(base)
	if applyErr != nil {
		return calendaroccurrence.Fields{}, applyErr
	}
	return fields, nil
}

// seriesQueries binds both query handles to one transaction, in the shape
// the shared series transforms take.
func seriesQueries(deps Deps, tx *dbretry.Tx) calendaroccurrence.Queries {
	return calendaroccurrence.Queries{
		Calendar: deps.CalendarQueries.WithTx(tx.RawTx()),
		Core:     deps.Queries.WithTx(tx.RawTx()),
	}
}

// patchCalendarEventOccurrence replaces a single occurrence of a
// recurring series with an override row through
// [calendaroccurrence.WriteOverride], and hands that row's public id to
// record inside the same transaction.
//
// The master cannot be task-linked here — the projection guard forbids a
// rule on a projected row, and a non-series scope requires a rule — so
// none of the itemkit propagation the series path performs applies.
func patchCalendarEventOccurrence(
	ctx context.Context,
	deps Deps,
	s *session,
	calendarID uint32,
	master calendar.FindCalendarEventByPublicIdRow,
	in *updateCalendarEventArgs,
	originalStart time.Time,
	label calendarlabels.Change,
	record func(context.Context, *dbretry.Tx, types.PublicID) error,
) error {
	patch, err := occurrencePatch(in, master.AllDay, string(master.Kind))
	if err != nil {
		return err
	}
	patch.Label = label

	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.update_calendar_event.occurrence", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		row, refusal, err := calendaroccurrence.WriteOverride(ctx, seriesQueries(deps, tx), calendaroccurrence.Override{
			WorkspaceID:   s.workspaceID,
			ActorID:       s.userID,
			CalendarID:    calendarID,
			Master:        master,
			OriginalStart: originalStart,
			Patch:         patch,
		})
		if refusal != nil {
			answered = refusal
			return refusal
		}
		if err != nil {
			return err
		}
		return record(ctx, tx, row.PublicID)
	})
	if answered != nil {
		return answered
	}
	if txErr != nil {
		return apierrors.New(apierrors.CalendarEventStoreWriteInterrupted)
	}
	return nil
}

// patchCalendarEventFollowing splits a series at an occurrence through
// [calendaroccurrence.SplitSeries]: the master stops before the split and
// a new master carries the edit and the remainder of the series.
//
// No MCP tool takes a rule, a recurrence end or an exception list, so the
// continuing series' recurrence columns are always the master's, carried
// forward by the shared transform. record receives the new master's public
// id inside the same transaction.
func patchCalendarEventFollowing(
	ctx context.Context,
	deps Deps,
	s *session,
	calendarID uint32,
	master calendar.FindCalendarEventByPublicIdRow,
	in *updateCalendarEventArgs,
	splitStart time.Time,
	label calendarlabels.Change,
	record func(context.Context, *dbretry.Tx, types.PublicID) error,
) error {
	patch, err := occurrencePatch(in, master.AllDay, string(master.Kind))
	if err != nil {
		return err
	}
	patch.Label = label

	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.update_calendar_event.following", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		row, refusal, err := calendaroccurrence.SplitSeries(ctx, seriesQueries(deps, tx), calendaroccurrence.Split{
			WorkspaceID: s.workspaceID,
			ActorID:     s.userID,
			CalendarID:  calendarID,
			Master:      master,
			SplitStart:  splitStart,
			Patch:       patch,
		})
		if refusal != nil {
			answered = refusal
			return refusal
		}
		if err != nil {
			return err
		}
		return record(ctx, tx, row.PublicID)
	})
	if answered != nil {
		return answered
	}
	if txErr != nil {
		return apierrors.New(apierrors.CalendarEventStoreWriteInterrupted)
	}
	return nil
}

// deleteCalendarEventOccurrence cancels one occurrence of a series through
// [calendaroccurrence.CancelOccurrence], the function the REST delete route
// cancels one through: it refuses an occurrenceStart the series does not
// produce, re-reads the exception list under the series lock so a
// concurrent cancel is never lost, and disables any override standing in
// for the occurrence in the same transaction.
func deleteCalendarEventOccurrence(
	ctx context.Context,
	deps Deps,
	s *session,
	master calendar.FindCalendarEventByPublicIdRow,
	occurrenceStart time.Time,
	record func(context.Context, *dbretry.Tx) error,
) error {
	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.delete_calendar_event.occurrence", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		refusal, err := calendaroccurrence.CancelOccurrence(ctx, seriesQueries(deps, tx), s.workspaceID, master, occurrenceStart)
		if refusal != nil {
			answered = refusal
			return refusal
		}
		if err != nil {
			return err
		}
		return record(ctx, tx)
	})
	if answered != nil {
		return answered
	}
	if txErr != nil {
		return apierrors.New(apierrors.CalendarEventStoreDeleteInterrupted)
	}
	return nil
}

// deleteCalendarEventFollowing stops a series at an occurrence through
// [calendaroccurrence.EndSeries], the function the REST delete route ends
// a series through.
func deleteCalendarEventFollowing(
	ctx context.Context,
	deps Deps,
	s *session,
	calendarID uint32,
	master calendar.FindCalendarEventByPublicIdRow,
	splitStart time.Time,
	record func(context.Context, *dbretry.Tx) error,
) error {
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.delete_calendar_event.following", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if err := calendaroccurrence.EndSeries(ctx, seriesQueries(deps, tx), s.workspaceID, calendarID, s.userID, master, splitStart); err != nil {
			return err
		}
		return record(ctx, tx)
	})
	// A splitStart the series does not produce comes back as the refusal
	// itself, answered as REST answers it rather than as a store failure.
	var refusal *apierrors.APIError
	if stderrors.As(txErr, &refusal) {
		return refusal
	}
	if txErr != nil {
		return apierrors.New(apierrors.CalendarEventStoreDeleteInterrupted)
	}
	return nil
}
