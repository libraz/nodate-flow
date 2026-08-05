package calendars

import (
	"encoding/json"
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
//   - bySetPos, when present, must be in [1, 5].
//   - until, when present, must parse as RFC 3339 or YYYY-MM-DD.
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
	if rule.BySetPos != nil && (*rule.BySetPos < 1 || *rule.BySetPos > 5) {
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
