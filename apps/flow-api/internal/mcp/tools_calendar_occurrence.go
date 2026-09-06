package mcp

import (
	"context"
	"database/sql"
	stderrors "errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
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

	// A task-linked event cannot be reached here: the projection guard
	// forbids a recurrence rule on a projected row, and the refusal above
	// requires one. So none of the itemkit propagation the series path
	// performs applies, and the write below is the whole change.
	originalStart := handlerutil.UnixToTime(in.OccurrenceStart)
	var written types.PublicID
	var scopeErr error
	if scope == scopeOccurrence {
		written, scopeErr = patchCalendarEventOccurrence(ctx, deps, s, calendarID, evt, in, originalStart)
	} else {
		written, scopeErr = patchCalendarEventFollowing(ctx, deps, s, calendarID, evt, in, originalStart)
	}
	if scopeErr != nil {
		return nil, scopeErr
	}

	calID64 := int64(calendarID)
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.CalEventUpdated,
		AuditAction:  "calendar.event.update",
		ResourceType: "calendar.event",
		ResourceID:   eventPub.String(),
		CalendarID:   &calID64,
		Payload: map[string]any{
			"eventId":         eventPub.String(),
			"scope":           string(scope),
			"occurrenceStart": in.OccurrenceStart,
			"writtenEventId":  written.String(),
			"via":             "mcp",
		},
		CallSite: "mcp.update_calendar_event",
	})
	return map[string]any{"success": true}, nil
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
	var partErr error
	if scope == scopeOccurrence {
		partErr = deleteCalendarEventOccurrence(ctx, deps, s, calendarID, evt, start)
	} else {
		partErr = deleteCalendarEventFollowing(ctx, deps, s, calendarID, evt, start)
	}
	if partErr != nil {
		return nil, partErr
	}

	calID64 := int64(calendarID)
	// The event kind is the update one because that is what happened to
	// the row named: the master survives with an exception or an earlier
	// end, and a deleted-kind event carrying its id would tell every
	// subscriber to drop a row that is still there. The scope and the
	// occurrence say which part of the series went. The audit action
	// stays the delete one, because a delete is what the caller asked
	// for and what an administrator searches the log by.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.CalEventUpdated,
		AuditAction:  "calendar.event.delete",
		ResourceType: "calendar.event",
		ResourceID:   eventPub.String(),
		CalendarID:   &calID64,
		Payload: map[string]any{
			"eventId":         eventPub.String(),
			"scope":           string(scope),
			"occurrenceStart": occurrenceStart,
			"via":             "mcp",
		},
		CallSite: "mcp.delete_calendar_event",
	})
	return map[string]any{"success": true}, nil
}

// occurrenceWindowArg resolves one end of the window an update names,
// from either the unix-seconds argument or the all-day date argument,
// and reports whether the caller named it at all.
//
// This is the shape only these tools have: two arguments for one bound,
// where REST carries a single instant. The shared merge takes an instant
// already decided, so the decision is made here and nothing downstream
// has to know which of the two the caller sent.
//
// field names the date argument in the refusal, so a caller that sent an
// unparseable date knows which one to correct.
func occurrenceWindowArg(at *int64, date *string, field string) (time.Time, bool, error) {
	if at != nil {
		return time.Unix(*at, 0).UTC(), true, nil
	}
	if date != nil && *date != "" {
		t, err := parseDayBoundary(*date)
		if err != nil {
			return time.Time{}, false, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid %s", field)
		}
		return t, true, nil
	}
	return time.Time{}, false, nil
}

// occurrencePatch reads the arguments an update call sends into the
// shape the shared merge takes.
//
// It is the whole of what these tools contribute to the merge: which
// argument stands for which column, and that either end of the window
// may be sent on its own — the other is then taken from the occurrence
// the write falls back to.
//
// No clear flags are set, because no tool argument asks for one: a
// nullable column the call did not mention keeps whatever the base
// carries. Nor is there an allDay, timezone, url or notificationOffset
// argument, so those columns are the base's too.
func occurrencePatch(in *updateCalendarEventArgs) (calendaroccurrence.Patch, error) {
	p := calendaroccurrence.Patch{
		Kind:        in.Kind,
		Visibility:  in.Visibility,
		ShowAs:      in.ShowAs,
		Flexibility: in.Flexibility,
		Title:       in.Title,
		Location:    in.Location,
		Memo:        in.Memo,
		BlockLabel:  in.BlockLabel,
	}
	start, startSent, err := occurrenceWindowArg(in.StartAt, in.StartDate, "startDate")
	if err != nil {
		return calendaroccurrence.Patch{}, err
	}
	end, endSent, err := occurrenceWindowArg(in.EndAt, in.EndDate, "endDate")
	if err != nil {
		return calendaroccurrence.Patch{}, err
	}
	if startSent {
		p.StartAt = &start
	}
	if endSent {
		p.EndAt = &end
	}
	return p, nil
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
	patch, err := occurrencePatch(in)
	if err != nil {
		return calendaroccurrence.Fields{}, err
	}
	fields, applyErr := patch.Apply(base)
	if applyErr != nil {
		return calendaroccurrence.Fields{}, applyErr
	}
	return fields, nil
}

// patchCalendarEventOccurrence replaces a single occurrence of a
// recurring series with an override row, and answers with that row's
// public id.
//
// The override is looked up before it is written because
// uniq_calendar_events_recurrence_override counts soft-deleted rows: an
// occurrence that was overridden, reverted to the series and then edited
// again already has a row, and inserting a second one collides.
// UpdateCalendarEventOverride sets enabled back to TRUE, which is what
// revives it.
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
) (types.PublicID, error) {
	parentID := handlerutil.NullInt32From(master.ID)
	originalStartNT := sql.NullTime{Time: originalStart, Valid: true}

	var written types.PublicID
	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.update_calendar_event.occurrence", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		cqtx := deps.CalendarQueries.WithTx(tx.RawTx())

		existing, findErr := cqtx.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
			WorkspaceID:             s.workspaceID,
			RecurrenceParentID:      parentID,
			RecurrenceOriginalStart: originalStartNT,
		})
		overridden := findErr == nil
		if !overridden && !stderrors.Is(findErr, sql.ErrNoRows) {
			answered = apierrors.New(apierrors.CalendarEventStoreReadInterrupted)
			return findErr
		}

		// What the caller's omissions fall back to is decided by the same
		// lookup that decides whether the write is an update or an
		// insert, so it is decided here rather than before the
		// transaction: an override written between the two would
		// otherwise be updated from the series' values.
		base := calendaroccurrence.MasterBase(master, originalStart)
		if overridden {
			base = calendaroccurrence.OverrideBase(existing)
		}
		fields, mergeErr := mergeOccurrenceFields(base, in)
		if mergeErr != nil {
			answered = mergeErr
			return mergeErr
		}

		if overridden {
			if _, err := cqtx.UpdateCalendarEventOverride(ctx, calendar.UpdateCalendarEventOverrideParams{
				Kind:               fields.Kind,
				Visibility:         fields.Visibility,
				ShowAs:             fields.ShowAs,
				Flexibility:        fields.Flexibility,
				Title:              fields.Title,
				AllDay:             fields.AllDay,
				StartAt:            fields.StartAt,
				EndAt:              fields.EndAt,
				Timezone:           fields.Timezone,
				Location:           fields.Location,
				Memo:               fields.Memo,
				Url:                fields.URL,
				BlockLabel:         fields.BlockLabel,
				NotificationOffset: fields.NotificationOffset,
				PublicID:           existing.PublicID,
				CalendarID:         existing.CalendarID,
				WorkspaceID:        s.workspaceID,
			}); err != nil {
				return err
			}
			written = existing.PublicID
			return nil
		}

		pub := newPublicID()
		if _, err := cqtx.CreateCalendarEventOverride(ctx, calendar.CreateCalendarEventOverrideParams{
			PublicID:                pub,
			WorkspaceID:             s.workspaceID,
			CalendarID:              calendarID,
			RecurrenceParentID:      parentID,
			RecurrenceOriginalStart: originalStartNT,
			Kind:                    fields.Kind,
			Visibility:              fields.Visibility,
			ShowAs:                  fields.ShowAs,
			Flexibility:             fields.Flexibility,
			Title:                   fields.Title,
			AllDay:                  fields.AllDay,
			StartAt:                 fields.StartAt,
			EndAt:                   fields.EndAt,
			Timezone:                fields.Timezone,
			Location:                fields.Location,
			Memo:                    fields.Memo,
			Url:                     fields.URL,
			// The occurrence stays filed under whoever the series is
			// filed under. An override is the same commitment at a
			// different moment, so moving it onto the editor would take
			// the meeting off its owner's calendar for that one week.
			OwnerUserID:        master.OwnerUserID,
			CreatedByUserID:    s.userID,
			BlockLabel:         fields.BlockLabel,
			NotificationOffset: fields.NotificationOffset,
		}); err != nil {
			return err
		}
		written = pub
		return nil
	})
	if answered != nil {
		return types.PublicID{}, answered
	}
	if txErr != nil {
		return types.PublicID{}, apierrors.New(apierrors.CalendarEventStoreWriteInterrupted)
	}
	return written, nil
}

// patchCalendarEventFollowing splits a series at an occurrence: the
// master stops before the split and a new master carries the edit and
// the remainder of the rule. It answers with the new master's public id.
//
// Where the master stops, and why that bound is recurrence_end rather
// than the rule's own until, is [calendaroccurrence.TruncationPoint] —
// the delete-side split truncates a series the same way and reads the
// same answer.
//
// The rule, the recurrence end and the exception list carry over whole,
// because no MCP tool can change them: this scope moves the boundary,
// not the pattern. An exception naming an occurrence before the split
// simply never matches what the new series produces, and deciding which
// entries to drop would mean expanding the rule to find out.
//
// Overrides at or after the split describe occurrences of the new series
// and are handed to it, soft-deleted ones included — left behind they
// would name an occurrence the truncated master no longer produces.
func patchCalendarEventFollowing(
	ctx context.Context,
	deps Deps,
	s *session,
	calendarID uint32,
	master calendar.FindCalendarEventByPublicIdRow,
	in *updateCalendarEventArgs,
	splitStart time.Time,
) (types.PublicID, error) {
	// The continuing series starts from the master alone: it is a new
	// master carrying the remainder of the rule, not an edit of an
	// occurrence, so no override row describes it.
	fields, err := mergeOccurrenceFields(calendaroccurrence.MasterBase(master, splitStart), in)
	if err != nil {
		return types.PublicID{}, err
	}

	truncateAt := calendaroccurrence.TruncationPoint(master, splitStart)

	continuingPublicID := newPublicID()
	newMaster := calendar.CreateCalendarEventParams{
		PublicID:             continuingPublicID,
		WorkspaceID:          s.workspaceID,
		CalendarID:           calendarID,
		Kind:                 fields.Kind,
		Visibility:           fields.Visibility,
		ShowAs:               fields.ShowAs,
		Flexibility:          fields.Flexibility,
		Title:                fields.Title,
		AllDay:               fields.AllDay,
		StartAt:              fields.StartAt,
		EndAt:                fields.EndAt,
		Timezone:             fields.Timezone,
		Location:             fields.Location,
		Memo:                 fields.Memo,
		Url:                  fields.URL,
		OwnerUserID:          master.OwnerUserID,
		CreatedByUserID:      s.userID,
		BlockLabel:           fields.BlockLabel,
		RecurrenceRule:       master.RecurrenceRule,
		RecurrenceEnd:        master.RecurrenceEnd,
		RecurrenceExceptions: master.RecurrenceExceptions,
		NotificationOffset:   fields.NotificationOffset,
	}

	txErr := dbretry.InTx(ctx, deps.DB, "mcp.update_calendar_event.following", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		cqtx := deps.CalendarQueries.WithTx(tx.RawTx())

		if _, err := cqtx.PatchCalendarEvent(ctx, calendar.PatchCalendarEventParams{
			RecurrenceEnd: sql.NullTime{Time: truncateAt, Valid: true},
			PublicID:      master.PublicID,
			CalendarID:    calendarID,
			WorkspaceID:   s.workspaceID,
		}); err != nil {
			return err
		}

		newID, err := cqtx.CreateCalendarEvent(ctx, newMaster)
		if err != nil {
			return err
		}

		if _, err := cqtx.ReparentCalendarEventOverridesFromStart(ctx, calendar.ReparentCalendarEventOverridesFromStartParams{
			// LAST_INSERT_ID answers as int64; the column behind the
			// parameter is INT UNSIGNED and sqlc emits sql.NullInt32 for
			// it, the same narrowing every internal id here goes through.
			NewParentID: sql.NullInt32{Int32: int32(newID), Valid: true}, //#nosec G115 -- internal row id, bounded by realistic workspace size
			WorkspaceID: s.workspaceID,
			OldParentID: handlerutil.NullInt32From(master.ID),
			SplitStart:  sql.NullTime{Time: splitStart, Valid: true},
		}); err != nil {
			return err
		}
		return nil
	})
	if txErr != nil {
		return types.PublicID{}, apierrors.New(apierrors.CalendarEventStoreWriteInterrupted)
	}
	return continuingPublicID, nil
}

// deleteCalendarEventOccurrence cancels one occurrence of a series.
//
// Cancellation is an entry in the master's recurrence_exceptions: the
// expanders subtract it and the occurrence stops being produced. When an
// override already stands in for that occurrence it is disabled in the
// same transaction — the override is a row of its own that the
// non-recurring range queries select directly, so an exception alone
// would cancel the occurrence and leave the copy the user had moved
// sitting on the calendar.
func deleteCalendarEventOccurrence(
	ctx context.Context,
	deps Deps,
	s *session,
	calendarID uint32,
	master calendar.FindCalendarEventByPublicIdRow,
	occurrenceStart time.Time,
) error {
	exceptions, changed, err := calendaroccurrence.AppendException(master.RecurrenceExceptions, occurrenceStart)
	if err != nil {
		return apierrors.New(apierrors.CalendarEventStoreReadInterrupted)
	}

	parentID := handlerutil.NullInt32From(master.ID)
	originalStart := sql.NullTime{Time: occurrenceStart, Valid: true}

	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.delete_calendar_event.occurrence", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		cqtx := deps.CalendarQueries.WithTx(tx.RawTx())

		if changed {
			if _, err := cqtx.PatchCalendarEvent(ctx, calendar.PatchCalendarEventParams{
				RecurrenceExceptions: exceptions,
				PublicID:             master.PublicID,
				CalendarID:           calendarID,
				WorkspaceID:          s.workspaceID,
			}); err != nil {
				return err
			}
		}

		override, findErr := cqtx.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
			WorkspaceID:             s.workspaceID,
			RecurrenceParentID:      parentID,
			RecurrenceOriginalStart: originalStart,
		})
		switch {
		case stderrors.Is(findErr, sql.ErrNoRows):
			return nil
		case findErr != nil:
			answered = apierrors.New(apierrors.CalendarEventStoreReadInterrupted)
			return findErr
		}
		if !override.Enabled {
			return nil
		}
		// affected-rows: not-applicable — this only runs on an override
		// the two statements above read as live in this same transaction,
		// and what the caller asked to cancel is the occurrence, which the
		// master's exception removes whether or not an override stood in
		// for it.
		_, err := cqtx.DisableCalendarEvent(ctx, calendar.DisableCalendarEventParams{
			PublicID:    override.PublicID,
			CalendarID:  override.CalendarID,
			WorkspaceID: s.workspaceID,
		})
		return err
	})
	if answered != nil {
		return answered
	}
	if txErr != nil {
		return apierrors.New(apierrors.CalendarEventStoreDeleteInterrupted)
	}
	return nil
}

// deleteCalendarEventFollowing stops a series at an occurrence: the
// master keeps the occurrences before the split and produces none from it
// onwards.
//
// The overrides at or after the split go with them. They describe
// occurrences the truncated master no longer produces, and each is a row
// the non-recurring range queries select on its own, so left enabled they
// outlive the part of the series they belong to.
func deleteCalendarEventFollowing(
	ctx context.Context,
	deps Deps,
	s *session,
	calendarID uint32,
	master calendar.FindCalendarEventByPublicIdRow,
	splitStart time.Time,
) error {
	truncateAt := calendaroccurrence.TruncationPoint(master, splitStart)
	parentID := handlerutil.NullInt32From(master.ID)

	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.delete_calendar_event.following", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		cqtx := deps.CalendarQueries.WithTx(tx.RawTx())

		if _, err := cqtx.PatchCalendarEvent(ctx, calendar.PatchCalendarEventParams{
			RecurrenceEnd: sql.NullTime{Time: truncateAt, Valid: true},
			PublicID:      master.PublicID,
			CalendarID:    calendarID,
			WorkspaceID:   s.workspaceID,
		}); err != nil {
			return err
		}

		// DisableCalendarEventOverridesByParent takes the whole series and
		// cannot be given a range, and the overrides before the split
		// describe occurrences the master still produces. So the rows are
		// listed and disabled one at a time: the list is already confined
		// to this parent's live overrides, and a series carries few enough
		// of them that the loop is bounded by the same runaway limit the
		// expander reads them under.
		//
		// The list is scoped to the acting user, so a confidential
		// override belonging to somebody else stays enabled past the
		// truncation and keeps rendering to its owner. That is the same
		// row the actor is not served anywhere else; disabling one they
		// cannot see would remove an occurrence from a calendar they have
		// no view of.
		starts, err := cqtx.ListCalendarEventOverriddenStarts(ctx, calendar.ListCalendarEventOverriddenStartsParams{
			WorkspaceID:  s.workspaceID,
			ParentIds:    []sql.NullInt32{parentID},
			ViewerUserID: s.userID,
		})
		if err != nil {
			answered = apierrors.New(apierrors.CalendarEventStoreReadInterrupted)
			return err
		}
		for _, st := range starts {
			if !st.RecurrenceOriginalStart.Valid || st.RecurrenceOriginalStart.Time.Before(splitStart) {
				continue
			}
			override, findErr := cqtx.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
				WorkspaceID:             s.workspaceID,
				RecurrenceParentID:      parentID,
				RecurrenceOriginalStart: st.RecurrenceOriginalStart,
			})
			if findErr != nil {
				answered = apierrors.New(apierrors.CalendarEventStoreReadInterrupted)
				return findErr
			}
			// affected-rows: not-applicable — the loop walks the live
			// overrides listed in this transaction and disables each one it
			// re-read by key, so a zero says another writer got there first
			// rather than that anything was missing. The caller named the
			// series, and truncating the master is what removes the
			// occurrences these describe.
			if _, err := cqtx.DisableCalendarEvent(ctx, calendar.DisableCalendarEventParams{
				PublicID:    override.PublicID,
				CalendarID:  override.CalendarID,
				WorkspaceID: s.workspaceID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if answered != nil {
		return answered
	}
	if txErr != nil {
		return apierrors.New(apierrors.CalendarEventStoreDeleteInterrupted)
	}
	return nil
}
