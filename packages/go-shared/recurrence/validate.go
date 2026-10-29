package recurrence

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// The bounds a stored rule's numeric fields have to fall inside.
//
// The lower bounds are meaning, not policy: an interval below 1 does not
// name a cadence and a count below 1 does not name a series. The upper
// bounds are cost — the expander walks candidates one step at a time, and
// both numbers end up sizing that walk — so they are deliberately loose
// enough that no calendar UI can reach them and tight enough that a
// stored value cannot make a scan unbounded.
const (
	minInterval = 1
	maxInterval = 999
	minCount    = 1
	maxCount    = 1000
	minMonthDay = 1
	maxMonthDay = 31
	maxByDay    = 7
	maxSetPos   = 5
	lastSetPos  = -1

	minUntilLength = 10
	maxUntilLength = 40
)

// InvalidRuleError names the one grammar rule a value breaks. Field is the
// JSON member at fault, or "recurrenceRule" / "recurrenceExceptions" when
// the whole value failed to decode.
//
// Transports map this onto their own error surface; the type carries no
// status code and no message catalog key, because the grammar is shared
// with callers that have neither.
type InvalidRuleError struct {
	Field  string
	Reason string
}

func (e *InvalidRuleError) Error() string {
	return fmt.Sprintf("recurrence: %s %s", e.Field, e.Reason)
}

func invalid(field, reason string) error {
	return &InvalidRuleError{Field: field, Reason: reason}
}

// isAbsent reports whether a stored JSON column holds no value: SQL NULL
// arrives as an empty slice, and a column written from a client that sent
// null holds the JSON literal.
func isAbsent(raw []byte) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

// ValidateRule reports whether a recurrence_rule value is in the
// canonical shape and inside the grammar this package expands. An absent
// value is not an error: not recurring is a legitimate state.
//
// It refuses at least everything chk_calendar_events_recurrence_shape
// refuses — an unknown or null member, a scalar where the shape has an
// array, a non-integer number, a lowercase token — so a write it accepts
// is one the CHECK stores. On top of that it refuses what the CHECK
// leaves to the applications: the members each freq may carry, and an
// UNTIL that resolves to no instant.
//
// The consequences of a rule outside the grammar still differ by side,
// and deliberately. A write is refused, because the caller is present and
// can correct it. A read tolerates whatever is already stored and expands
// what it can, because expansion runs over every row on the notification
// path, where failing one row would take the whole scan down with it.
func ValidateRule(raw []byte) error {
	if isAbsent(raw) {
		return nil
	}
	if err := validateShape(raw); err != nil {
		return err
	}
	var r Rule
	if err := json.Unmarshal(raw, &r); err != nil {
		return invalid("recurrenceRule", "is not a rule object")
	}
	return r.Validate()
}

// validateShape checks the members of a raw rule object the way the
// CHECK's JSON schema does: by key, by JSON type and by literal. A typed
// decode cannot, because it reads null as absent and 1.0 as 1.
func validateShape(raw []byte) error {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return invalid("recurrenceRule", "is not a rule object")
	}
	for key, value := range members {
		if _, ok := memberShapes[key]; !ok {
			return invalid(key, "is not a recurrence rule member")
		}
		if isAbsent(value) {
			return invalid(key, "must not be null")
		}
		if err := memberShapes[key](value); err != nil {
			return invalid(key, err.Error())
		}
	}
	if _, ok := members["freq"]; !ok {
		return invalid("freq", "is required")
	}
	return nil
}

// errShape is the reason every member check reports; the field name
// carries which member it was.
type errShape string

func (e errShape) Error() string { return string(e) }

// memberShapes is the CHECK's properties table: one check per allowed
// member. Ranges and cross-member rules are Rule.Validate's.
var memberShapes = map[string]func(json.RawMessage) error{
	"freq":       stringShape,
	"interval":   integerShape,
	"count":      integerShape,
	"byDay":      arrayShape(stringShape),
	"byMonthDay": arrayShape(integerShape),
	"bySetPos":   arrayShape(integerShape),
	"wkst":       stringShape,
	"until":      untilShape,
}

func stringShape(v json.RawMessage) error {
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return errShape("must be a string")
	}
	return nil
}

// untilShape holds UNTIL to the CHECK's length bounds, counted in
// characters as JSON schema counts them.
func untilShape(v json.RawMessage) error {
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return errShape("must be a string")
	}
	if n := utf8.RuneCountInString(s); n < minUntilLength || n > maxUntilLength {
		return errShape(fmt.Sprintf("must be %d to %d characters", minUntilLength, maxUntilLength))
	}
	return nil
}

// integerShape accepts a JSON number written as an integer. 1.0 and 1e2
// are refused because the CHECK's "integer" refuses them.
func integerShape(v json.RawMessage) error {
	lit := strings.TrimSpace(string(v))
	digits := strings.TrimPrefix(lit, "-")
	if digits == "" {
		return errShape("must be an integer")
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return errShape("must be an integer")
		}
	}
	var n int
	if err := json.Unmarshal(v, &n); err != nil {
		return errShape("must be an integer")
	}
	return nil
}

// arrayShape accepts a non-empty JSON array of unique items, each passing
// item.
func arrayShape(item func(json.RawMessage) error) func(json.RawMessage) error {
	return func(v json.RawMessage) error {
		var items []json.RawMessage
		if err := json.Unmarshal(v, &items); err != nil {
			return errShape("must be an array")
		}
		if len(items) == 0 {
			return errShape("must not be empty")
		}
		seen := map[string]bool{}
		for _, it := range items {
			if err := item(it); err != nil {
				return err
			}
			key := strings.TrimSpace(string(it))
			if seen[key] {
				return errShape("must not repeat an item")
			}
			seen[key] = true
		}
		return nil
	}
}

// Validate checks a decoded rule against the grammar: the ranges, the
// tokens, and the members each freq may carry. [ValidateRule] runs it
// after the shape check.
func (r *Rule) Validate() error {
	if r == nil {
		return nil
	}
	if !r.Freq.Valid() {
		// Spelled out rather than delegated to a set membership message,
		// because the case matters: the column stores the lowercase
		// tokens and Expand has no arm for anything else.
		return invalid("freq", "must be one of daily, weekly, monthly, yearly")
	}
	if r.Interval != nil && (*r.Interval < minInterval || *r.Interval > maxInterval) {
		return invalid("interval", fmt.Sprintf("must be between %d and %d", minInterval, maxInterval))
	}
	if r.Count != nil && (*r.Count < minCount || *r.Count > maxCount) {
		return invalid("count", fmt.Sprintf("must be between %d and %d", minCount, maxCount))
	}
	if len(r.ByDay) > maxByDay {
		return invalid("byDay", fmt.Sprintf("must hold at most %d weekdays", maxByDay))
	}
	for _, d := range r.ByDay {
		if !canonicalWeekday(d) {
			return invalid("byDay", "must hold uppercase two-letter weekday codes")
		}
	}
	if len(r.ByMonthDay) > maxMonthDay {
		return invalid("byMonthDay", fmt.Sprintf("must hold at most %d days", maxMonthDay))
	}
	for _, md := range r.ByMonthDay {
		if md < minMonthDay || md > maxMonthDay {
			return invalid("byMonthDay", fmt.Sprintf("must be between %d and %d", minMonthDay, maxMonthDay))
		}
	}
	if len(r.BySetPos) > 1 {
		return invalid("bySetPos", "must hold one position")
	}
	for _, pos := range r.BySetPos {
		if pos != lastSetPos && (pos < 1 || pos > maxSetPos) {
			return invalid("bySetPos", fmt.Sprintf("must be between 1 and %d, or %d", maxSetPos, lastSetPos))
		}
	}
	if r.Wkst != "" && r.Wkst != "MO" && r.Wkst != "SU" {
		return invalid("wkst", "must be MO or SU")
	}
	if err := r.validateMembersForFreq(); err != nil {
		return err
	}
	if r.Until != "" && r.Count != nil {
		return invalid("until", "cannot be combined with count")
	}
	// UNTIL is checked by resolving it, so what is accepted here is by
	// construction what Expand acts on. A spelling that parses to no
	// bound would otherwise be stored as an end date and then ignored,
	// leaving a series the caller ended running forever.
	if r.Until != "" && !canonicalUntil(r.Until) {
		return invalid("until", "must be a YYYY-MM-DD date or an RFC 3339 instant")
	}
	return nil
}

// validateMembersForFreq refuses the combinations the CHECK leaves to the
// applications: byDay sets the weekdays of a weekly rule or, with
// bySetPos, names the one weekday of a monthly rule; byMonthDay is
// monthly only, and never alongside byDay.
func (r *Rule) validateMembersForFreq() error {
	if len(r.BySetPos) > 0 && len(r.ByDay) == 0 {
		return invalid("bySetPos", "requires byDay")
	}
	switch r.Freq {
	case FreqDaily, FreqYearly:
		switch {
		case len(r.ByDay) > 0:
			return invalid("byDay", "is not used on a "+string(r.Freq)+" rule")
		case len(r.ByMonthDay) > 0:
			return invalid("byMonthDay", "is not used on a "+string(r.Freq)+" rule")
		}
	case FreqWeekly:
		switch {
		case len(r.ByMonthDay) > 0:
			return invalid("byMonthDay", "is not used on a weekly rule")
		case len(r.BySetPos) > 0:
			return invalid("bySetPos", "is not used on a weekly rule")
		}
	case FreqMonthly:
		switch {
		case len(r.ByDay) > 0 && len(r.ByMonthDay) > 0:
			return invalid("byDay", "cannot be combined with byMonthDay")
		case len(r.ByDay) > 0 && len(r.BySetPos) == 0:
			return invalid("byDay", "requires bySetPos on a monthly rule")
		case len(r.ByDay) > 1:
			return invalid("byDay", "must hold one weekday on a monthly rule")
		}
	}
	return nil
}

// canonicalWeekday reports whether a byDay token is one the CHECK stores:
// the uppercase code itself, with nothing around it.
func canonicalWeekday(v string) bool {
	_, ok := parseWeekday(v)
	return ok && len(v) == 2 && v == strings.ToUpper(v)
}

// canonicalUntil reports whether an UNTIL is in one of the two forms the
// shape names: a YYYY-MM-DD date, or an RFC 3339 instant carrying its
// own offset.
func canonicalUntil(v string) bool {
	if isDateOnly(v) {
		return true
	}
	_, err := time.Parse(time.RFC3339, v)
	return err == nil
}

// ValidateExceptions reports whether a recurrence_exceptions value holds
// only entries the expander resolves. An absent value is not an error.
//
// Entries are checked by parsing them with the same function expansion
// uses, so an entry accepted here is one that suppresses an occurrence.
// The alternative is what an unchecked list does: an unresolvable
// spelling is stored, skipped during expansion, and the occurrence the
// caller deleted keeps being drawn with nothing reporting a problem.
func ValidateExceptions(raw []byte) error {
	if isAbsent(raw) {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return invalid("recurrenceExceptions", "is not an array of strings")
	}
	for _, v := range values {
		if _, ok := parseExceptionEntry(v, time.UTC); !ok {
			return invalid("recurrenceExceptions", "holds an entry that is not a date or a timestamp")
		}
	}
	return nil
}
