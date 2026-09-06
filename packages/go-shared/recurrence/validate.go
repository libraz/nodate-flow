package recurrence

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
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

// ValidateRule reports whether a recurrence_rule value is inside the
// grammar this package expands. An absent value is not an error: not
// recurring is a legitimate state.
//
// This is the write-side half of the same grammar Expand reads. Keeping
// the two in one package is what stops a value from being accepted on one
// route and refused on another: the frequency set, the weekday tokens and
// the UNTIL spellings are each resolved by exactly one function, used
// both here and during expansion.
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
	var r Rule
	if err := json.Unmarshal(raw, &r); err != nil {
		return invalid("recurrenceRule", "is not a rule object")
	}
	return r.Validate()
}

// Validate checks a decoded rule against the grammar.
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
	// bySetPos is the "nth matching day of the period" selector, the one
	// that expresses "second Monday of the month". No expander in the
	// product implements it, so a rule carrying it expands as though the
	// selector were absent and a series saved as "second Monday" comes
	// back as every Monday. Refusing it says the same thing the expanders
	// do, at the point where the answer is still actionable. Implementing
	// it means teaching the expanders first; until then the answer is no.
	if !isAbsent(r.BySetPos) {
		return invalid("bySetPos", "is not supported")
	}
	for _, d := range r.ByDay {
		if _, ok := parseWeekday(d); !ok {
			return invalid("byDay", "must hold two-letter weekday codes")
		}
	}
	for _, md := range r.ByMonthDay {
		if md < minMonthDay || md > maxMonthDay {
			return invalid("byMonthDay", fmt.Sprintf("must be between %d and %d", minMonthDay, maxMonthDay))
		}
	}
	// UNTIL is checked by resolving it, so what is accepted here is by
	// construction what Expand acts on. A spelling that parses to no
	// bound would otherwise be stored as an end date and then ignored,
	// leaving a series the caller ended running forever.
	if strings.TrimSpace(r.Until) != "" && parseUntil(r.Until, time.UTC) == nil {
		return invalid("until", "must be a date or a timestamp")
	}
	return nil
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
