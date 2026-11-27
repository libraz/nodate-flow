package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarlabels"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/packages/go-shared/eventacl"
)

// PatchEventInput is the input for the patch event endpoint.
//
// The enum-backed fields carry the same value sets as CreateEventInput.
// They are the column's own sets, and stating them here is what refuses an
// unrecognised value with a validation error naming the field. Without the
// constraint the only thing left refusing it is the ENUM itself, which
// answers as a write failure the caller cannot act on — and every reader
// downstream has to treat a value it does not know as the safest one it
// can, because it arrived through a route that never checked.
//
// The textual fields carry the same maxLength values as CreateEventInput,
// for the same reason: a bound that only one of the two write routes
// states is not a bound on the column.
//
// Title also carries minLength, because an omitted field is already how
// this route says "leave it alone" — so an explicit "" is a value, and
// without the constraint it is written to a NOT NULL column that create
// refuses to leave empty, leaving an event nothing can name.
type PatchEventInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	Body  struct {
		Kind                 *string          `json:"kind,omitempty" required:"false" enum:"event,block,free,milestone" doc:"Event kind"`
		Visibility           *string          `json:"visibility,omitempty" required:"false" enum:"default,public,private,confidential" doc:"Visibility"`
		ShowAs               *string          `json:"showAs,omitempty" required:"false" enum:"busy,free,tentative,oof" doc:"Show-as status"`
		Flexibility          *string          `json:"flexibility,omitempty" required:"false" enum:"fixed,negotiable,conditional" doc:"Whether the commitment can be moved; independent of showAs, which only says whether the time reads as taken"`
		Title                *string          `json:"title,omitempty" required:"false" minLength:"1" maxLength:"500" doc:"Event title"`
		AllDay               *bool            `json:"allDay,omitempty" required:"false" doc:"All-day flag"`
		StartAt              *int64           `json:"startAt,omitempty" required:"false" doc:"Start time as unix seconds (UTC). Not accepted for an all-day event."`
		EndAt                *int64           `json:"endAt,omitempty" required:"false" doc:"End time as unix seconds (UTC). Not accepted for an all-day event."`
		StartDate            *string          `json:"startDate,omitempty" required:"false" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"First day of an all-day event (YYYY-MM-DD). Only for an all-day event; switching allDay needs both dates."`
		EndDate              *string          `json:"endDate,omitempty" required:"false" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"Day after the last day of an all-day event (YYYY-MM-DD, exclusive); equal to startDate for a milestone."`
		Timezone             *string          `json:"timezone,omitempty" required:"false" maxLength:"64" doc:"IANA timezone"`
		Location             *string          `json:"location,omitempty" required:"false" maxLength:"500" doc:"Location"`
		Memo                 *string          `json:"memo,omitempty" required:"false" maxLength:"10000" doc:"Memo"`
		URL                  *string          `json:"url,omitempty" required:"false" maxLength:"2048" doc:"Related URL"`
		BlockLabel           *string          `json:"blockLabel,omitempty" required:"false" maxLength:"100" doc:"Block label"`
		RecurrenceRule       *json.RawMessage `json:"recurrenceRule,omitempty" required:"false" doc:"Recurrence rule"`
		RecurrenceEnd        *int64           `json:"recurrenceEnd,omitempty" required:"false" doc:"Recurrence end as unix seconds (UTC)"`
		RecurrenceExceptions *json.RawMessage `json:"recurrenceExceptions,omitempty" required:"false" doc:"Array of ISO 8601 dates/times to exclude from recurrence"`
		NotificationOffset   *int32           `json:"notificationOffset,omitempty" required:"false" doc:"Notification offset"`
		LabelID              *string          `json:"labelId,omitempty" required:"false" doc:"Calendar label public ID to file the event under"`
		// Clear names the nullable fields to set back to nothing.
		//
		// A PATCH that carries only values cannot express removal: the
		// field the caller left out and the field they want emptied
		// arrive identically. So every nullable column was write-once
		// through this route — the dialog's "no repeat" saved
		// successfully and the meeting kept recurring, and a location
		// typed by mistake stayed for good.
		//
		// Clearing recurrenceRule clears recurrenceEnd and
		// recurrenceExceptions with it: they describe a series, and
		// leaving them on a row that no longer has one keeps state
		// nothing reads and the next writer has to reason about.
		Clear []string `json:"clear,omitempty" required:"false" enum:"location,memo,url,blockLabel,recurrenceRule,recurrenceEnd,recurrenceExceptions,notificationOffset,labelId" doc:"Nullable fields to clear. Takes precedence over a value sent for the same field in this request."`
		// Scope says which occurrences of a recurring series the patch
		// reaches, and OccurrenceStart says which occurrence it starts
		// from.
		//
		// An omitted Scope is the whole series, which is what this route
		// has always done: a body that carries neither member patches the
		// master and every occurrence its rule produces.
		Scope           *string `json:"scope,omitempty" required:"false" enum:"series,occurrence,thisAndFollowing" doc:"Which occurrences of a recurring series this patch reaches. Omitted means the whole series."`
		OccurrenceStart *int64  `json:"occurrenceStart,omitempty" required:"false" doc:"The occurrence's start under the series rule, as unix seconds (UTC). Required when scope is not series; identifies the occurrence even after the edit moves it."`
	}
}

// clearableEventFields maps the API names accepted in PatchEventInput's
// Clear list to the flags the patch query takes.
type clearableEventFields struct {
	location             int64
	memo                 int64
	url                  int64
	blockLabel           int64
	recurrenceRule       int64
	recurrenceEnd        int64
	recurrenceExceptions int64
	notificationOffset   int64
	labelID              int64
}

// parseClearFields reads the Clear list. An unknown name is rejected
// rather than ignored: a caller who misspells a field would otherwise
// get a success response for a removal that did not happen, which is the
// failure this whole mechanism exists to remove.
func parseClearFields(names []string) (clearableEventFields, error) {
	var out clearableEventFields
	for _, name := range names {
		switch name {
		case "location":
			out.location = 1
		case "memo":
			out.memo = 1
		case "url":
			out.url = 1
		case "blockLabel":
			out.blockLabel = 1
		case "recurrenceRule":
			out.recurrenceRule = 1
		case "recurrenceEnd":
			out.recurrenceEnd = 1
		case "recurrenceExceptions":
			out.recurrenceExceptions = 1
		case "notificationOffset":
			out.notificationOffset = 1
		case "labelId":
			out.labelID = 1
		default:
			return clearableEventFields{}, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
	}
	return out, nil
}

// PatchEventOutput is the response for the patch event endpoint.
type PatchEventOutput struct {
	Body EventResponse
}

// PatchEvent updates mutable event fields. Requires edit permission.
// When the event is task-linked (task_id set), title changes are routed
// through itemkit.RenameItem and start/end changes through
// itemkit.RescheduleEvent so the linked task stays in lockstep. Other
// fields (location, memo, visibility, etc.) are applied via the plain
// sqlc PATCH inside the same transaction.
func PatchEvent(deps Deps) func(context.Context, *PatchEventInput) (*PatchEventOutput, error) {
	return func(ctx context.Context, input *PatchEventInput) (*PatchEventOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendarWrite(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		evtUID, err := uuid.Parse(input.EvtID)
		if err != nil {
			return nil, errEventNotFound
		}
		evt, err := deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
			PublicID:    types.FromUUID(evtUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errEventNotFound
			}
			return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}

		// Check edit permission (try to find attendee record for the actor).
		attendee, attErr := findActorAttendee(ctx, deps.CalendarQueries, actorID, evt.ID, evt.RecurrenceParentID)
		if attErr != nil {
			return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}
		if !canEditEvent(actorID, evt, sub, attendee) {
			return nil, httpErr(apierrors.CalendarEventEditPermissionRequired)
		}
		patchViewer := func(row calendar.FindCalendarEventByPublicIdRow, att *calendar.FindCalendarEventAttendeeRow) eventViewer {
			return eventViewer{
				UserID:          actorID,
				IsAttendee:      att != nil,
				CalendarDefault: string(cal.DefaultEventVisibility),
				CanEdit:         canEditEvent(actorID, row, sub, att),
			}
		}

		// Pair invariant: explicit partial start_at without end_at (or
		// vice versa) is ambiguous. Undated transitions are supported,
		// but they must come through unlink first.
		if err := requireEventStartEndPair(input.Body.StartAt, input.Body.EndAt); err != nil {
			return nil, err
		}
		if err := requireEventChronology(input.Body.StartAt, input.Body.EndAt); err != nil {
			return nil, err
		}
		if err := requireDatetime("recurrenceEnd", input.Body.RecurrenceEnd); err != nil {
			return nil, err
		}
		if err := requireDatetime("occurrenceStart", input.Body.OccurrenceStart); err != nil {
			return nil, err
		}
		if err := requireNotificationOffset(input.Body.NotificationOffset); err != nil {
			return nil, err
		}
		if input.Body.Timezone != nil {
			if err := requireValidTimezone("timezone", *input.Body.Timezone); err != nil {
				return nil, err
			}
		}

		scope, err := decodeOccurrenceScope(input.Body.Scope)
		if err != nil {
			return nil, err
		}
		if err := resolvePatchAllDay(ctx, deps, wsID, evt, input, scope == scopeSeries); err != nil {
			return nil, err
		}
		// The parent link is read only when its answer can change the
		// outcome. A series-scoped patch that leaves the recurrence
		// columns alone reaches no refusal that depends on it, and asking
		// anyway would put a query on the common path for a question it
		// never has.
		var parentID sql.NullInt32
		if scope != scopeSeries || patchTouchesRecurrenceFields(input) {
			link, linkErr := deps.CalendarQueries.FindCalendarEventRecurrenceLink(ctx, calendar.FindCalendarEventRecurrenceLinkParams{
				WorkspaceID: wsID,
				PublicID:    evt.PublicID,
			})
			if linkErr != nil {
				return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
			parentID = link.RecurrenceParentID
		}
		if err := requireOccurrenceScope(scope, input, evt, parentID); err != nil {
			return nil, err
		}

		labelChange, err := resolvePatchLabel(ctx, deps, wsID, actorID, cal, evt, attendee, scope, input)
		if err != nil {
			return nil, err
		}

		if scope != scopeSeries {
			// The rule a "this and following" edit carries into the
			// continuing series is checked before any write, so a
			// malformed one is a validation error rather than a series
			// truncated for a remainder that could not be created.
			if spec := validateRecurrenceRule(input.Body.RecurrenceRule); spec != nil {
				return nil, httpErr(spec)
			}
			if spec := validateRecurrenceExceptions(input.Body.RecurrenceExceptions); spec != nil {
				return nil, httpErr(spec)
			}

			originalStart := handlerutil.UnixToTime(*input.Body.OccurrenceStart)
			var written calendar.FindCalendarEventByPublicIdRow
			var scopeErr error
			if scope == scopeOccurrence {
				written, scopeErr = patchEventOccurrence(ctx, deps, wsID, actorID, cal, evt, input, originalStart, labelChange)
			} else {
				written, scopeErr = patchEventFollowing(ctx, deps, wsID, actorID, cal, evt, input, originalStart, labelChange)
			}
			if scopeErr != nil {
				return nil, scopeErr
			}

			writtenAttendee, aerr := findActorAttendee(ctx, deps.CalendarQueries, actorID, written.ID, written.RecurrenceParentID)
			if aerr != nil {
				return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
			out := &PatchEventOutput{}
			out.Body = eventFromFullRow(written, patchViewer(written, writtenAttendee))
			recordOccurrencePatch(ctx, deps, wsID, actorID, input, scope, written, cal.ID)
			return out, nil
		}

		isLinked := evt.TaskID.Valid
		titleChanged := input.Body.Title != nil && *input.Body.Title != evt.Title
		timeChanged := input.Body.StartAt != nil // pair invariant guarantees EndAt also set

		// Resolved before the transaction so a refusal is answered as a
		// validation error rather than surfacing from inside a retryable
		// unit of work.
		var renameTitle taskrules.Title
		if isLinked && titleChanged {
			var terr error
			renameTitle, terr = requireLinkedTitle("title", *input.Body.Title)
			if terr != nil {
				return nil, terr
			}
		}

		// itemkit owns the propagation of a linked event's title and
		// times to the task, so those fields drop out of the direct PATCH
		// rather than being written twice. They are decided here rather
		// than by clearing input.Body inside the transaction: the
		// transaction is retried on a deadlock and would then re-read a
		// request body its own first attempt had emptied.
		patchTitle := input.Body.Title
		patchStartAt := input.Body.StartAt
		patchEndAt := input.Body.EndAt
		if isLinked && titleChanged {
			patchTitle = nil
		}
		if isLinked && timeChanged {
			patchStartAt = nil
			patchEndAt = nil
		}

		cleared, cerr := parseClearFields(input.Body.Clear)
		if cerr != nil {
			return nil, cerr
		}
		// The row as it will stand: what the request sends or clears over
		// what is stored. An unlinked event stays dated or undated here; a
		// linked one's times move through itemkit, which keeps it dated.
		shapeKind := string(evt.Kind)
		if input.Body.Kind != nil {
			shapeKind = *input.Body.Kind
		}
		shapeRecurring := calendaroccurrence.HasRule(evt.RecurrenceRule)
		switch {
		case cleared.recurrenceRule == 1:
			shapeRecurring = false
		case input.Body.RecurrenceRule != nil:
			shapeRecurring = calendaroccurrence.HasRule(*input.Body.RecurrenceRule)
		}
		shapeReminded := evt.NotificationOffset.Valid
		switch {
		case cleared.notificationOffset == 1:
			shapeReminded = false
		case input.Body.NotificationOffset != nil:
			shapeReminded = true
		}
		if err := requireEventShape(shapeKind, input.Body.StartAt != nil || evt.StartAt.Valid,
			shapeRecurring, shapeReminded); err != nil {
			return nil, err
		}
		params := calendar.PatchCalendarEventParams{
			PublicID:    types.FromUUID(evtUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,

			ClearLocation:             cleared.location,
			ClearMemo:                 cleared.memo,
			ClearUrl:                  cleared.url,
			ClearBlockLabel:           cleared.blockLabel,
			ClearRecurrenceRule:       cleared.recurrenceRule,
			ClearRecurrenceEnd:        cleared.recurrenceEnd,
			ClearRecurrenceExceptions: cleared.recurrenceExceptions,
			ClearNotificationOffset:   cleared.notificationOffset,
			// sqlc emits one field per textual occurrence of a named
			// argument; clear_recurrence_rule appears in three SET
			// expressions because clearing the rule clears the two
			// columns that only describe a series.
		}
		if input.Body.Kind != nil {
			params.Kind = calendar.NullCalendarEventsKind{
				CalendarEventsKind: calendar.CalendarEventsKind(*input.Body.Kind),
				Valid:              true,
			}
		}
		if input.Body.Visibility != nil {
			params.Visibility = calendar.NullCalendarEventsVisibility{
				CalendarEventsVisibility: calendar.CalendarEventsVisibility(*input.Body.Visibility),
				Valid:                    true,
			}
		}
		if input.Body.ShowAs != nil {
			params.ShowAs = calendar.NullCalendarEventsShowAs{
				CalendarEventsShowAs: calendar.CalendarEventsShowAs(*input.Body.ShowAs),
				Valid:                true,
			}
		}
		if input.Body.Flexibility != nil {
			params.Flexibility = calendar.NullCalendarEventsFlexibility{
				CalendarEventsFlexibility: calendar.CalendarEventsFlexibility(*input.Body.Flexibility),
				Valid:                     true,
			}
		}
		if patchTitle != nil {
			params.Title = sql.NullString{String: *patchTitle, Valid: true}
		}
		if input.Body.AllDay != nil {
			params.AllDay = sql.NullBool{Bool: *input.Body.AllDay, Valid: true}
		}
		if patchStartAt != nil {
			params.StartAt = sql.NullTime{Time: handlerutil.UnixToTime(*patchStartAt), Valid: true}
		}
		if patchEndAt != nil {
			params.EndAt = sql.NullTime{Time: handlerutil.UnixToTime(*patchEndAt), Valid: true}
		}
		if input.Body.Timezone != nil {
			params.Timezone = sql.NullString{String: *input.Body.Timezone, Valid: true}
		}
		if input.Body.Location != nil {
			params.Location = sql.NullString{String: *input.Body.Location, Valid: true}
		}
		if input.Body.Memo != nil {
			params.Memo = sql.NullString{String: *input.Body.Memo, Valid: true}
		}
		if input.Body.URL != nil {
			params.Url = sql.NullString{String: *input.Body.URL, Valid: true}
		}
		if input.Body.BlockLabel != nil {
			params.BlockLabel = sql.NullString{String: *input.Body.BlockLabel, Valid: true}
		}
		if input.Body.RecurrenceRule != nil {
			if spec := validateRecurrenceRule(input.Body.RecurrenceRule); spec != nil {
				return nil, httpErr(spec)
			}
			// A task-linked event may not become recurring — invariant
			// enforced in itemkit. Flag here so the tx rolls back
			// deterministically.
			if isLinked {
				return nil, httpErr(apierrors.ItemItemkitRecurrenceWithTaskLink)
			}
			params.RecurrenceRule = json.RawMessage(*input.Body.RecurrenceRule)
		}
		if input.Body.RecurrenceEnd != nil {
			params.RecurrenceEnd = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.RecurrenceEnd), Valid: true}
		}
		if input.Body.RecurrenceExceptions != nil {
			if spec := validateRecurrenceExceptions(input.Body.RecurrenceExceptions); spec != nil {
				return nil, httpErr(spec)
			}
			params.RecurrenceExceptions = json.RawMessage(*input.Body.RecurrenceExceptions)
		}
		if input.Body.NotificationOffset != nil {
			params.NotificationOffset = sql.NullInt32{Int32: *input.Body.NotificationOffset, Valid: true}
		}

		// A write that moves where a series' occurrences fall — its start,
		// zone, rule or bounds — takes the overrides and exceptions naming
		// those occurrences along with it, in the same transaction. The
		// exception list stays as sent when the caller sent one.
		realignSeries := calendaroccurrence.HasRule(evt.RecurrenceRule) &&
			((params.StartAt.Valid && !params.StartAt.Time.Equal(evt.StartAt.Time)) ||
				(input.Body.Timezone != nil && *input.Body.Timezone != evt.Timezone) ||
				input.Body.RecurrenceRule != nil || cleared.recurrenceRule == 1 ||
				input.Body.RecurrenceEnd != nil || cleared.recurrenceEnd == 1)
		keepExceptions := input.Body.RecurrenceExceptions != nil || cleared.recurrenceExceptions == 1

		// updated carries the post-write row out of the transaction; evt
		// stays the pre-write snapshot the decisions above were made from.
		updated := evt
		// answered holds a response-shaped error decided inside the
		// transaction, so an itemkit invariant is not reported as a
		// generic write failure.
		var answered error
		// labelChanged says whether the label link moved in the committed attempt.
		var labelChanged bool
		// renamed carries the linked task's text out of the transaction
		// so its search embedding is refreshed once the rename has
		// committed.
		var renamed itemkit.RenamedTask
		txErr := dbretry.InTx(ctx, deps.DB, "calendars.PatchEvent", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			labelChanged = false
			renamed = itemkit.RenamedTask{}
			cqtx := deps.CalendarQueries.WithTx(tx.RawTx())

			// Linked title change → itemkit.RenameItem (propagates to task + siblings)
			if isLinked && titleChanged {
				var renameErr error
				renamed, renameErr = itemkit.RenameItem(ctx, tx, itemkit.RenameItemArgs{
					WorkspaceID: wsID,
					ActorUserID: actorID,
					NewTitle:    renameTitle,
					EventID:     evt.ID,
				})
				if renameErr != nil {
					answered = translateItemkitError(ctx, "itemkit.RenameItem", renameErr)
					return renameErr
				}
			}
			// Linked time change → itemkit.RescheduleEvent (propagates to task date)
			if isLinked && timeChanged {
				if err := itemkit.RescheduleEvent(ctx, tx, itemkit.RescheduleEventArgs{
					WorkspaceID: wsID,
					EventID:     evt.ID,
					ActorUserID: actorID,
					StartAt:     handlerutil.UnixToTime(*input.Body.StartAt),
					EndAt:       handlerutil.UnixToTime(*input.Body.EndAt),
				}); err != nil {
					answered = translateItemkitError(ctx, "itemkit.RescheduleEvent", err)
					return err
				}
			}

			// The series columns as they stand before this write, read
			// under the row lock so the realignment below starts from what
			// is committed rather than from the snapshot taken before the
			// transaction.
			var before calendaroccurrence.SeriesState
			if realignSeries {
				locked, lerr := cqtx.LockCalendarEventSeries(ctx, calendar.LockCalendarEventSeriesParams{
					ID:          evt.ID,
					WorkspaceID: wsID,
				})
				if lerr != nil {
					answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
					return lerr
				}
				before = calendaroccurrence.SeriesStateOf(evt)
				before.Rule = locked.RecurrenceRule
				before.End = locked.RecurrenceEnd
				before.Exceptions = locked.RecurrenceExceptions
			}

			// Not an existence check: MySQL counts changed rows, so a PATCH
			// that re-sends the event's current values reports zero. The
			// event is re-read below and that read is what would fail.
			if _, err := cqtx.PatchCalendarEvent(ctx, params); err != nil {
				return err
			}

			if labelChange.Mode != calendarlabels.ChangeKeep {
				changed, refusal, lerr := calendarlabels.ApplyToSeries(ctx, cqtx, wsID, evt.ID, labelChange)
				if refusal != nil {
					answered = handlerutil.HTTPErrFromAPIError(refusal)
					return refusal
				}
				if lerr != nil {
					return lerr
				}
				labelChanged = changed
			}

			// Re-read inside the tx so the response reflects what itemkit
			// + sqlc jointly wrote.
			var rerr error
			updated, rerr = cqtx.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
				PublicID:    types.FromUUID(evtUID),
				CalendarID:  cal.ID,
				WorkspaceID: wsID,
			})
			if rerr != nil {
				answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
				return rerr
			}

			if realignSeries {
				exceptions, err := calendaroccurrence.RealignSeries(ctx, seriesQueries(deps, tx), wsID, before, updated, keepExceptions)
				if err != nil {
					return err
				}
				if exceptions != nil {
					updated.RecurrenceExceptions = exceptions
				}
			}
			return nil
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.CalendarEventStoreWriteInterrupted)
		}

		// A linked event's title is also the task's, so renaming through
		// the event moves the text search is served from. Past the commit:
		// the write is no longer revocable, which is the only point a
		// best-effort refresh can run without a failure here costing a
		// write the caller has already been told about.
		if renamed.TaskID != 0 {
			embed.RefreshTaskAfterCommit(ctx, deps.Embedder, wsID, renamed.TaskID, renamed.Title, renamed.Description)
		}

		out := &PatchEventOutput{}
		out.Body = eventFromFullRow(updated, patchViewer(updated, attendee))

		// A request whose only content is a label that is already in place
		// writes and records nothing.
		if labelChange.Mode != calendarlabels.ChangeKeep && !labelChanged && patchIsLabelOnly(input) {
			return out, nil
		}

		payload := map[string]any{
			"eventId":    input.EvtID,
			"calendarId": input.CalID,
		}
		// itemkit emits the update event for a title or time change; a
		// label change on its own has to record it here.
		itemkitEmitted := isLinked && (titleChanged || timeChanged)
		if isLinked && (!labelChanged || itemkitEmitted) {
			// itemkit fired item.rescheduled / item.renamed inside the
			// transaction above and dual-emitted the legacy
			// calendar.event.updated kind with it, so the event row is
			// already there; appending a second one would show the edit
			// on the timeline twice.
			recordCalendarAudit(ctx, deps, wsID, actorID, mutationlog.Mutation{
				AuditAction:  "calendar.event.update",
				ResourceType: "calendar.event",
				ResourceID:   input.EvtID,
				Payload:      payload,
				CallSite:     "calendars.PatchEvent",
			})
		} else {
			// An unlinked event has no itemkit write behind it (nor does a
			// linked event's label-only change), so the existing kind is
			// emitted here to keep webhook subscribers working.
			recordCalendarEventChange(ctx, deps, wsID, cal.ID, actorID, updated.PublicID, updated.Title, mutationlog.Mutation{
				EventType:    eventbus.CalEventUpdated,
				AuditAction:  "calendar.event.update",
				ResourceType: "calendar.event",
				ResourceID:   input.EvtID,
				Payload:      payload,
				CallSite:     "calendars.PatchEvent",
			})
		}

		return out, nil
	}
}

// patchIsLabelOnly reports whether the request carries nothing but a label
// change: no other member, and no clear entry other than labelId.
func patchIsLabelOnly(input *PatchEventInput) bool {
	body := input.Body
	body.LabelID = nil
	body.Scope = nil
	body.OccurrenceStart = nil
	body.Clear = nil
	for _, name := range input.Body.Clear {
		if name != "labelId" {
			return false
		}
	}
	return reflect.ValueOf(body).IsZero()
}

// patchTouchesDetails reports whether the request writes or clears location,
// memo or url.
func patchTouchesDetails(input *PatchEventInput) bool {
	body := input.Body
	return body.Location != nil || body.Memo != nil || body.URL != nil ||
		slices.ContainsFunc(body.Clear, func(name string) bool {
			return name == "location" || name == "memo" || name == "url"
		})
}

// resolvePatchLabel enforces the details-visibility rule and turns the
// request's label members into a Change, in the order the MCP tools use: a
// write to location, memo, url or the label needs a viewer who can see the
// target row's details, then the label must belong to the calendar. Clear
// wins over a value. The target is the override for an occurrence scope when
// one exists, else the series.
func resolvePatchLabel(
	ctx context.Context,
	deps Deps,
	wsID, actorID uint32,
	cal calendar.FindCalendarByPublicIdRow,
	evt calendar.FindCalendarEventByPublicIdRow,
	evtAttendee *calendar.FindCalendarEventAttendeeRow,
	scope occurrenceScope,
	input *PatchEventInput,
) (calendarlabels.Change, error) {
	clearLabel := slices.Contains(input.Body.Clear, "labelId")
	if input.Body.LabelID == nil && !clearLabel && !patchTouchesDetails(input) {
		return calendarlabels.Change{}, nil
	}
	target, targetAttendee := evt, evtAttendee
	if scope == scopeOccurrence {
		ov, err := deps.CalendarQueries.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
			WorkspaceID:             wsID,
			RecurrenceParentID:      handlerutil.NullInt32From(evt.ID),
			RecurrenceOriginalStart: sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.OccurrenceStart), Valid: true},
		})
		switch {
		case err == nil:
			target, err = deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
				PublicID:    ov.PublicID,
				CalendarID:  ov.CalendarID,
				WorkspaceID: wsID,
			})
			if err == nil {
				targetAttendee, err = findActorAttendee(ctx, deps.CalendarQueries, actorID, target.ID, target.RecurrenceParentID)
			}
			if err != nil {
				return calendarlabels.Change{}, httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
		case !errors.Is(err, sql.ErrNoRows):
			return calendarlabels.Change{}, httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}
	}
	visible := eventacl.CanSeeDetails(
		eventacl.Event{
			Visibility:      eventacl.Visibility(target.Visibility),
			OwnerUserID:     target.OwnerUserID,
			CreatedByUserID: target.CreatedByUserID,
			CalendarDefault: eventacl.Visibility(cal.DefaultEventVisibility),
		},
		eventacl.Actor{UserID: actorID, IsAttendee: targetAttendee != nil},
	)
	if refusal := calendarrules.RequireDetailsVisible(visible); refusal != nil {
		return calendarlabels.Change{}, handlerutil.HTTPErrFromAPIError(refusal)
	}
	if input.Body.LabelID == nil && !clearLabel {
		return calendarlabels.Change{}, nil
	}
	if clearLabel {
		return calendarlabels.Change{Mode: calendarlabels.ChangeClear}, nil
	}
	id, refusal, err := calendarlabels.ResolveLabel(ctx, deps.CalendarQueries, cal.ID, *input.Body.LabelID)
	if err != nil {
		return calendarlabels.Change{}, httpErr(apierrors.CalendarEventStoreReadInterrupted)
	}
	if refusal != nil {
		return calendarlabels.Change{}, handlerutil.HTTPErrFromAPIError(refusal)
	}
	return calendarlabels.Change{Mode: calendarlabels.ChangeSet, LabelID: id}, nil
}
