package calendars

import (
	"encoding/json"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
)

// The recurrence grammar lives in packages/go-shared/recurrence, next to
// the expander that acts on it, and the functions here only carry its
// verdict onto the REST error surface. They check nothing themselves: a
// second copy of the rules would be free to drift, and a rule accepted on
// one route and refused on another is a rule every reader downstream has
// to treat as the worst case it could be.
//
// A rejected rule surfaces as apierrors.ValidationBodyFieldInvalid (422).
// The catalog has no recurrence-specific code, and the failure is exactly
// what that one describes: a body field outside the range its schema
// allows.

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
		return apierrors.ValidationBodyFieldInvalid
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
