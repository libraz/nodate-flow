// Package calendaroccurrence holds the machinery for acting on part of a
// recurring calendar series rather than the whole of it, in the one place
// every transport that offers the choice goes through: the REST handlers
// under internal/http/handlers/calendars and the calendar tools in
// internal/mcp.
//
// A series is one master row, so an edit or a delete that says nothing
// about which occurrences it means reaches all of them. That is the right
// default and the wrong only answer: moving this week's stand-up and
// moving the stand-up are different requests. Answering the second one
// takes an override row standing in for a single occurrence, an exception
// entry cancelling one, and a recurrence_end that stops a series just
// before a split — three encodings that only make sense together, and
// none of which a caller can see.
//
// Both transports offer the same three scopes under the same names and
// refuse the same combinations under the same error codes, because it is
// the same question about the same rows: the web app offers "this
// occurrence / this and following / all events" and an agent has to be
// able to say those three things too. Written twice, they agree only for
// as long as somebody keeps reading both.
//
// What is deliberately not here is the transport's own vocabulary. The
// refusals leave as an [apierrors.Spec] and the member they are about as
// a plain name, so the REST handlers can render RFC 9457 with a field
// pointer and the tools can answer their own way, from one decision. For
// the same reason nothing in this package is typed against a request
// struct: a signature naming one transport's input is a signature the
// other cannot call, which is what kept the second copy alive.
//
// The window a scoped write stores still passes the ordinary calendar
// write rules — see [calendarrules] — because an occurrence is a
// calendar_events row like any other. [Patch.Apply] is where the two meet.
package calendaroccurrence

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// Scope names which occurrences of a recurring series a write reaches.
type Scope string

const (
	// ScopeSeries acts on the master, and with it every occurrence the
	// rule produces. An omitted scope resolves here, so a caller that does
	// not send the field keeps the behaviour it always had.
	ScopeSeries Scope = "series"
	// ScopeOccurrence acts on exactly one occurrence, through an override
	// row that stands in for it, and leaves the rest of the series alone.
	ScopeOccurrence Scope = "occurrence"
	// ScopeThisAndFollowing splits the series at an occurrence: the master
	// stops producing occurrences there, and the remainder carries on
	// separately.
	ScopeThisAndFollowing Scope = "thisAndFollowing"
)

// Scopes is the closed set both transports advertise, built from the
// constants rather than written out again so a value that is accepted and
// a value that is published cannot come apart.
var Scopes = []string{
	string(ScopeSeries),
	string(ScopeOccurrence),
	string(ScopeThisAndFollowing),
}

// ParseScope reads the scope a request names, and reports whether it is
// one this package knows.
//
// An empty string is the whole series: both transports declare the enum
// on the field as well, so an unrecognised value is normally refused
// before a handler body runs. Repeating the closed set here is what keeps
// a value the schema stops describing — or one arriving through an
// in-process call that never passed the schema — from falling through to
// the series path and rewriting every occurrence.
//
// The caller renders the refusal, because where the value arrived from is
// the part it can act on: a body member on one route, a query parameter
// on another, a tool argument on a third.
func ParseScope(raw string) (Scope, bool) {
	switch Scope(raw) {
	case "", ScopeSeries:
		return ScopeSeries, true
	case ScopeOccurrence:
		return ScopeOccurrence, true
	case ScopeThisAndFollowing:
		return ScopeThisAndFollowing, true
	}
	return "", false
}

// HasRule reports whether a stored recurrence_rule column holds a rule.
//
// A NULL column reads back as the JSON literal null through the reads
// that COALESCE it and as an empty slice through the ones that do not, so
// both spellings of "no rule" are answered here.
func HasRule(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

// ScopeRefusal answers the scope / row combinations that name no
// occurrence to act on, and returns the member the refusal is about so a
// transport that can point at one does.
//
// A nil spec means the combination is workable. The member name is
// returned alongside rather than baked in because only the caller knows
// whether it arrived in a body, a query string or a tool argument.
//
// occurrenceStart has no absent value of its own: zero stands for
// omitted, and the instant it displaces is 1970-01-01T00:00:00Z, which no
// series reachable through this API produces.
//
// None of this can be left to the database. The projection guard trigger
// inspects only the row being written and never follows a parent link, so
// it cannot see that a row is already an override, and the one rule it
// does enforce arrives as SQLSTATE 45000 — a write failure the caller
// cannot act on.
func ScopeRefusal(scope Scope, occurrenceStart int64, hasRule, isOverride bool) (*apierrors.Spec, string) {
	if scope == ScopeSeries {
		return nil, ""
	}

	// Which occurrence is singled out is the whole content of a
	// non-series scope. Without it the request names nothing.
	if occurrenceStart == 0 {
		return apierrors.CalendarEventOccurrenceStartRequired, "occurrenceStart"
	}

	// An override stands in for exactly one occurrence and produces none,
	// so it has no occurrence to single out and no series to truncate.
	// Acting on it is the series scope's job. It is also how a two-level
	// chain would be written: an override of an override inserts happily
	// and is then unreachable, because the expander subtracts the
	// occurrence the first override replaced and nothing expands an
	// override.
	if isOverride {
		return apierrors.CalendarEventAlreadyOccurrenceOverride, "scope"
	}

	// Nor is there an occurrence to single out on a row that produces one.
	if !hasRule {
		return apierrors.CalendarEventNotRecurring, "scope"
	}
	return nil, ""
}

// Fields is the whole occurrence an override row carries.
//
// Every column has a value because an override is not a delta: the row
// stands in for the occurrence entirely, and a column left unset would
// read as the absence of a value rather than as the series' own.
type Fields struct {
	Kind               calendar.CalendarEventsKind
	Visibility         calendar.CalendarEventsVisibility
	ShowAs             calendar.CalendarEventsShowAs
	Flexibility        calendar.CalendarEventsFlexibility
	Title              string
	AllDay             bool
	StartAt            sql.NullTime
	EndAt              sql.NullTime
	Timezone           string
	Location           sql.NullString
	Memo               sql.NullString
	URL                sql.NullString
	BlockLabel         sql.NullString
	NotificationOffset sql.NullInt32
}

// MasterBase is what a member the caller did not send falls back to when
// the occurrence has no override yet: the series' own values, in the slot
// the occurrence holds under the rule — its original start, for as long
// as the master's own window lasts.
func MasterBase(master calendar.FindCalendarEventByPublicIdRow, originalStart time.Time) Fields {
	var duration time.Duration
	if master.StartAt.Valid && master.EndAt.Valid {
		duration = master.EndAt.Time.Sub(master.StartAt.Time)
	}
	return Fields{
		Kind:               master.Kind,
		Visibility:         master.Visibility,
		ShowAs:             master.ShowAs,
		Flexibility:        master.Flexibility,
		Title:              master.Title,
		AllDay:             master.AllDay,
		StartAt:            sql.NullTime{Time: originalStart, Valid: true},
		EndAt:              sql.NullTime{Time: originalStart.Add(duration), Valid: true},
		Timezone:           master.Timezone,
		Location:           master.Location,
		Memo:               master.Memo,
		URL:                master.Url,
		BlockLabel:         master.BlockLabel,
		NotificationOffset: master.NotificationOffset,
	}
}

// OverrideBase is what a member the caller did not send falls back to
// once an override already stands in for the occurrence.
//
// The override row is the occurrence. It exists precisely where the
// occurrence differs from the series, so a default read off the master
// would return it to the values the override was created to leave behind:
// an occurrence moved to another time and later renamed would move back.
//
// Whether the row is enabled does not enter here. A soft-deleted override
// is still that occurrence's own last state, and the update that follows
// revives it.
func OverrideBase(existing calendar.FindCalendarEventOverrideRow) Fields {
	return Fields{
		Kind:               existing.Kind,
		Visibility:         existing.Visibility,
		ShowAs:             existing.ShowAs,
		Flexibility:        existing.Flexibility,
		Title:              existing.Title,
		AllDay:             existing.AllDay,
		StartAt:            existing.StartAt,
		EndAt:              existing.EndAt,
		Timezone:           existing.Timezone,
		Location:           existing.Location,
		Memo:               existing.Memo,
		URL:                existing.Url,
		BlockLabel:         existing.BlockLabel,
		NotificationOffset: existing.NotificationOffset,
	}
}

// Clears are the members a caller asked to be rid of.
//
// They are separate from the sent values because "set this to nothing"
// and "leave this alone" are different requests that a nil pointer cannot
// tell apart, and only the first may empty a column.
type Clears struct {
	Location           bool
	Memo               bool
	URL                bool
	BlockLabel         bool
	NotificationOffset bool
}

// Patch is the set of occurrence members a caller asked to change,
// resolved out of whatever shape the transport received them in.
//
// A nil pointer is a member the request did not mention. Instants are
// already resolved, so a transport that takes unix seconds and one that
// takes an all-day date both arrive here having decided what the caller
// meant — and the decision that is left, folding them over the values the
// occurrence falls back to, is made once.
type Patch struct {
	Kind               *string
	Visibility         *string
	ShowAs             *string
	Flexibility        *string
	Title              *string
	Timezone           *string
	AllDay             *bool
	StartAt            *time.Time
	EndAt              *time.Time
	Location           *string
	Memo               *string
	URL                *string
	BlockLabel         *string
	NotificationOffset *int32
	Clear              Clears
}

// Apply folds the patch over the values the occurrence falls back to, and
// holds the result to the calendar write rules.
//
// A clear wins over a value sent for the same member. Sending both is
// contradictory, and the destructive reading is the one that cannot
// silently leave a value the caller asked to be rid of — which is also
// the precedence PatchCalendarEvent applies to the series.
//
// One end of the window may be sent on its own, and the other is then
// taken from the base. The base for a single occurrence is that
// occurrence rather than the master, so borrowing never moves the window
// somewhere the caller did not name.
//
// The window is ordered before it is pinned. Truncating an all-day pair
// to UTC midnight is monotonic, so a pair that passes still passes
// afterwards, and checking first means an all-day request with the days
// the wrong way round is refused for the reason it is wrong rather than
// silently collapsing to one day.
func (p Patch) Apply(base Fields) (Fields, *apierrors.APIError) {
	f := base
	if p.Kind != nil {
		f.Kind = calendar.CalendarEventsKind(*p.Kind)
	}
	if p.Visibility != nil {
		f.Visibility = calendar.CalendarEventsVisibility(*p.Visibility)
	}
	if p.ShowAs != nil {
		f.ShowAs = calendar.CalendarEventsShowAs(*p.ShowAs)
	}
	if p.Flexibility != nil {
		f.Flexibility = calendar.CalendarEventsFlexibility(*p.Flexibility)
	}
	if p.Title != nil {
		f.Title = *p.Title
	}
	// The zone has no clear flag: the column cannot hold nothing.
	if p.Timezone != nil {
		f.Timezone = *p.Timezone
	}
	if p.AllDay != nil {
		f.AllDay = *p.AllDay
	}
	if p.StartAt != nil {
		f.StartAt = sql.NullTime{Time: *p.StartAt, Valid: true}
	}
	if p.EndAt != nil {
		f.EndAt = sql.NullTime{Time: *p.EndAt, Valid: true}
	}
	f.Location = mergeNullString(f.Location, p.Location, p.Clear.Location)
	f.Memo = mergeNullString(f.Memo, p.Memo, p.Clear.Memo)
	f.URL = mergeNullString(f.URL, p.URL, p.Clear.URL)
	f.BlockLabel = mergeNullString(f.BlockLabel, p.BlockLabel, p.Clear.BlockLabel)
	switch {
	case p.Clear.NotificationOffset:
		f.NotificationOffset = sql.NullInt32{}
	case p.NotificationOffset != nil:
		f.NotificationOffset = sql.NullInt32{Int32: *p.NotificationOffset, Valid: true}
	}

	if p.StartAt != nil || p.EndAt != nil {
		if err := calendarrules.RequireEventChronology(
			nullableUnix(f.StartAt), nullableUnix(f.EndAt)); err != nil {
			return Fields{}, err
		}
	}
	f.StartAt, f.EndAt = calendarrules.NormalizeAllDayBounds(f.AllDay, f.StartAt, f.EndAt)
	return f, nil
}

// mergeNullString applies one nullable text member's clear flag and sent
// value over the value it falls back to.
func mergeNullString(stored sql.NullString, sent *string, cleared bool) sql.NullString {
	if cleared {
		return sql.NullString{}
	}
	if sent != nil {
		return sql.NullString{String: *sent, Valid: true}
	}
	return stored
}

// nullableUnix narrows a nullable instant onto the optional unix seconds
// the shared calendar rules take. A NULL column is an absent bound, which
// those rules read as nothing to compare.
func nullableUnix(t sql.NullTime) *int64 {
	if !t.Valid {
		return nil
	}
	secs := t.Time.Unix()
	return &secs
}

// TruncationPoint returns the recurrence_end that stops a series just
// before a split.
//
// The master is truncated through recurrence_end rather than by rewriting
// the rule's own until. The expanders read the two as independent upper
// bounds and honour whichever is earlier, so a recurrence_end set just
// before the split truncates a rule bounded by until and a rule bounded
// by count alike; rewriting until would leave a count-bounded rule
// emitting the occurrences the split removed, and would rewrite JSON the
// caller supplied.
//
// A master that already stops earlier keeps its own bound, so this never
// extends a series that a later recurrence_end would revive.
func TruncationPoint(master calendar.FindCalendarEventByPublicIdRow, splitStart time.Time) time.Time {
	truncateAt := splitStart.Add(-time.Millisecond)
	if master.RecurrenceEnd.Valid && master.RecurrenceEnd.Time.Before(truncateAt) {
		return master.RecurrenceEnd.Time
	}
	return truncateAt
}

// AppendException adds one occurrence start to a stored exception list,
// and reports whether the list changed.
//
// The entry is written as an RFC 3339 instant in UTC, the spelling the
// expander turns into an exact skip keyed by unix seconds — so the entry
// matches the occurrence whatever timezone the series is drawn in. An
// entry already naming the same instant is left alone rather than
// repeated.
//
// A stored list that does not parse is an error rather than a fresh list:
// starting over would drop every occurrence the series had already
// cancelled, and they would all come back.
func AppendException(stored json.RawMessage, start time.Time) (json.RawMessage, bool, error) {
	var list []string
	if len(stored) > 0 && string(stored) != "null" {
		if err := json.Unmarshal(stored, &list); err != nil {
			return nil, false, err
		}
	}

	entry := start.UTC().Format(time.RFC3339)
	for _, existing := range list {
		if existing == entry {
			return nil, false, nil
		}
		if at, err := time.Parse(time.RFC3339, existing); err == nil && at.Equal(start) {
			return nil, false, nil
		}
	}

	encoded, err := json.Marshal(append(list, entry))
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}
