package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
)

// The recurrence grammar lives in packages/go-shared/recurrence, next to
// the expander that acts on it, and the functions here only carry its
// verdict onto the REST error surface. They check nothing themselves: a
// second copy of the rules would be free to drift, and a rule accepted on
// one route and refused on another is a rule every reader downstream has
// to treat as the worst case it could be.
//
// A rejected rule surfaces as CALENDAR.EVENT.RECURRENCE_RULE_INVALID, the
// catalog's code for it; a rejected exception list as
// VALIDATION.BODY.FIELD_INVALID, which has no more specific code.

// recurrenceRuleBytes unwraps the raw body member. A nil pointer and an
// empty value both mean the field was not sent, which the grammar reads
// as not recurring rather than as an error.
func recurrenceRuleBytes(data *json.RawMessage) []byte {
	if data == nil {
		return nil
	}
	return *data
}

// validateRecurrenceRule refuses a recurrence rule the expanders cannot
// act on, before the raw JSON is persisted.
func validateRecurrenceRule(data *json.RawMessage) *apierrors.Spec {
	if err := recurrence.ValidateRule(recurrenceRuleBytes(data)); err != nil {
		return apierrors.CalendarEventRecurrenceRuleInvalid
	}
	return nil
}

// validateRecurrenceExceptions refuses an exception list the expanders
// cannot act on. An entry in an unresolvable spelling would otherwise be
// stored, skipped during expansion, and reported to the caller as a
// deleted occurrence that the calendar keeps drawing.
func validateRecurrenceExceptions(data *json.RawMessage) *apierrors.Spec {
	if err := recurrence.ValidateExceptions(recurrenceRuleBytes(data)); err != nil {
		return apierrors.ValidationBodyFieldInvalid
	}
	return nil
}

// earliestOverrideStart precedes every occurrence key a row can hold.
var earliestOverrideStart = time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)

// seriesNamesOccurrences reports whether a series carries anything that
// names one of its occurrences: an exception entry or an override row,
// soft-deleted ones included. Those are keyed by date on an all-day series
// and by instant on a timed one, so the series cannot switch form under
// them.
func seriesNamesOccurrences(
	ctx context.Context,
	q *calendar.Queries,
	workspaceID uint32,
	master calendar.FindCalendarEventByPublicIdRow,
) (bool, error) {
	if len(recurrence.ParseExceptions(master.RecurrenceExceptions)) > 0 {
		return true, nil
	}
	overrides, err := q.ListCalendarEventOverridesFromStart(ctx, calendar.ListCalendarEventOverridesFromStartParams{
		WorkspaceID: workspaceID,
		ParentID:    handlerutil.NullInt32From(master.ID),
		FromStart:   sql.NullTime{Time: earliestOverrideStart, Valid: true},
	})
	if err != nil {
		return false, err
	}
	return len(overrides) > 0, nil
}
