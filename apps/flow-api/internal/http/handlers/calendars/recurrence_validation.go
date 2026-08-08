package calendars

import (
	"encoding/json"
	"strings"
	"time"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// recurrenceRulePayload is the wire shape of a stored recurrence rule. It
// mirrors packages/ui/src/calendar/types.ts (RecurrenceRule) so the
// server-side validator accepts exactly what the client emits and the
// expander consumes. freq is the canonical lowercase set; the client's
// presetToRRule emits these values directly.
type recurrenceRulePayload struct {
	Freq       string   `json:"freq"`
	Interval   *int     `json:"interval"`
	ByDay      []string `json:"byDay"`
	ByMonthDay []int    `json:"byMonthDay"`
	BySetPos   *int     `json:"bySetPos"`
	Until      *string  `json:"until"`
	Count      *int     `json:"count"`
}

// validWeekdays is the closed set of RFC 5545 weekday tokens accepted in a
// recurrence rule's byDay list.
var validWeekdays = map[string]bool{
	"SU": true, "MO": true, "TU": true, "WE": true, "TH": true, "FR": true, "SA": true,
}

// validateRecurrenceRule rejects malformed recurrence rules before the raw
// JSON is persisted, so a typo cannot silently produce an event that never
// appears (unknown freq) or that the client-side expander would refuse to
// expand. It returns nil when there is no rule (nil, empty, or JSON null).
//
// The server stores the rule verbatim and does not expand it; clients expand
// concrete instances from the stored rule. Validation therefore only checks
// well-formedness against the canonical grammar shared with the expander
// (packages/ui/src/calendar/types.ts):
//
//   - freq must be one of daily / weekly / monthly / yearly (lowercase).
//   - interval, when present, must be in [1, 999].
//   - count, when present, must be in [1, 1000].
//   - byDay tokens must be valid two-letter weekday codes.
//   - byMonthDay values must be in [1, 31].
//   - bySetPos is refused outright; see below.
//   - until, when present, must parse as RFC 3339 or YYYY-MM-DD.
//
// bySetPos is the "nth matching day of the period" selector — the one that
// expresses "second Monday of the month". No expander implements it: not
// the client-side one in packages/ui/src/calendar/recurrence.ts, not the
// server-side one in packages/go-shared/recurrence. A rule carrying it was
// nonetheless accepted as long as the value was in [1, 5], stored, and then
// expanded with the selector dropped, so a series saved as "second Monday"
// came back as every Monday and nothing reported a problem. Refusing the
// field says the same thing the expanders do, at the point where the answer
// is still actionable. Implementing it means teaching both expanders first;
// until then the honest answer is no.
//
// On failure it returns apierrors.ValidationBodyFieldInvalid (422). There is
// no dedicated recurrence error code today; if one is wanted, a
// CALENDAR.EVENT.RECURRENCE_RULE_INVALID spec would be the natural home.
func validateRecurrenceRule(data *json.RawMessage) *apierrors.Spec {
	if data == nil || len(*data) == 0 || string(*data) == "null" {
		return nil
	}
	var rule recurrenceRulePayload
	if err := json.Unmarshal(*data, &rule); err != nil {
		return apierrors.ValidationBodyFieldInvalid
	}
	switch rule.Freq {
	case "daily", "weekly", "monthly", "yearly":
	default:
		return apierrors.ValidationBodyFieldInvalid
	}
	if rule.Interval != nil && (*rule.Interval < 1 || *rule.Interval > 999) {
		return apierrors.ValidationBodyFieldInvalid
	}
	if rule.Count != nil && (*rule.Count < 1 || *rule.Count > 1000) {
		return apierrors.ValidationBodyFieldInvalid
	}
	if rule.BySetPos != nil {
		return apierrors.ValidationBodyFieldInvalid
	}
	for _, d := range rule.ByDay {
		if !validWeekdays[d] {
			return apierrors.ValidationBodyFieldInvalid
		}
	}
	for _, md := range rule.ByMonthDay {
		if md < 1 || md > 31 {
			return apierrors.ValidationBodyFieldInvalid
		}
	}
	if rule.Until != nil && *rule.Until != "" {
		if _, e1 := time.Parse(time.RFC3339, *rule.Until); e1 != nil {
			if _, e2 := time.Parse("2006-01-02", *rule.Until); e2 != nil {
				return apierrors.ValidationBodyFieldInvalid
			}
		}
	}
	return nil
}

// validateRecurrenceExceptions rejects an exception list the expanders
// cannot act on. It returns nil when there is no list (nil, empty, or JSON
// null).
//
// The stored value is a JSON array of strings, each naming either one
// occurrence or a whole local day. buildExceptions in
// packages/go-shared/recurrence recognises three spellings and silently
// skips anything else:
//
//   - YYYY-MM-DD                — the local day
//   - RFC 3339                  — one instant
//   - YYYY-MM-DDTHH:MM:SS       — one instant, in the event's timezone
//
// Skipping is the problem: an exception written in any other form was
// accepted with a 200 and then ignored, so the client reported the
// occurrence as deleted and the calendar kept showing it. Validating here
// makes a spelling the expander cannot use a rejected request instead of a
// silently absent deletion.
func validateRecurrenceExceptions(data *json.RawMessage) *apierrors.Spec {
	if data == nil || len(*data) == 0 || string(*data) == "null" {
		return nil
	}
	var values []string
	if err := json.Unmarshal(*data, &values); err != nil {
		return apierrors.ValidationBodyFieldInvalid
	}
	for _, v := range values {
		if !validRecurrenceException(v) {
			return apierrors.ValidationBodyFieldInvalid
		}
	}
	return nil
}

// validRecurrenceException reports whether one exception entry is in a
// form the expanders resolve. The layouts are the ones buildExceptions
// parses, in the order it tries them.
func validRecurrenceException(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	// The date-only branch is length-checked as well as parsed, because
	// buildExceptions gates it on a ten-character string and Go's parser
	// would otherwise accept "2026-1-2" here and skip it there.
	if len(v) == len("2006-01-02") {
		if _, err := time.Parse("2006-01-02", v); err == nil {
			return true
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05"} {
		if _, err := time.Parse(layout, v); err == nil {
			return true
		}
	}
	return false
}
