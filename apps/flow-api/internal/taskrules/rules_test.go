package taskrules

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func day(t *testing.T, s string) sql.NullTime {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return sql.NullTime{Time: parsed, Valid: true}
}

// TestNewTitle pins what counts as empty, because that is the whole rule:
// a title made of whitespace is the case a caller reaches by accident
// and the one an untrimmed check lets through.
func TestNewTitle(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		err  error
	}{
		{name: "plain", raw: "write the spec", want: "write the spec"},
		{name: "padded", raw: "  write the spec  ", want: "write the spec"},
		{name: "empty", raw: "", err: ErrTitleEmpty},
		{name: "spaces only", raw: "   ", err: ErrTitleEmpty},
		{name: "tabs and newlines only", raw: "\t\n ", err: ErrTitleEmpty},
		{name: "inner spaces kept", raw: " a  b ", want: "a  b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewTitle(tc.raw)
			if !errors.Is(err, tc.err) {
				t.Fatalf("NewTitle(%q) error = %v, want %v", tc.raw, err, tc.err)
			}
			if got.String() != tc.want {
				t.Fatalf("NewTitle(%q) = %q, want %q", tc.raw, got.String(), tc.want)
			}
		})
	}
}

// TestPatchTitle separates "the body said nothing" from "the body said
// nothing useful": the first keeps the stored title, the second is a
// refusal, and a pointer is the only thing that tells them apart.
func TestPatchTitle(t *testing.T) {
	empty := ""
	blank := "   "
	padded := "  renamed  "

	cases := []struct {
		name    string
		current string
		in      *string
		want    string
		err     error
	}{
		{name: "nil keeps current", current: "kept", in: nil, want: "kept"},
		{name: "nil keeps an untrimmed current verbatim", current: "  kept  ", in: nil, want: "  kept  "},
		{name: "pointer to empty is refused", current: "kept", in: &empty, err: ErrTitleEmpty},
		{name: "pointer to whitespace is refused", current: "kept", in: &blank, err: ErrTitleEmpty},
		{name: "pointer is trimmed", current: "kept", in: &padded, want: "renamed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PatchTitle(tc.current, tc.in)
			if !errors.Is(err, tc.err) {
				t.Fatalf("PatchTitle error = %v, want %v", err, tc.err)
			}
			if got.String() != tc.want {
				t.Fatalf("PatchTitle = %q, want %q", got.String(), tc.want)
			}
		})
	}
}

// TestDateOrder pins the boundaries the rule is defined by: equal dates
// pass, and a missing side is unconstrained rather than a violation.
func TestDateOrder(t *testing.T) {
	null := sql.NullTime{}

	cases := []struct {
		name    string
		due     sql.NullTime
		started sql.NullTime
		err     error
	}{
		{name: "due after start", due: day(t, "2026-09-10"), started: day(t, "2026-09-01")},
		{name: "equal dates", due: day(t, "2026-09-01"), started: day(t, "2026-09-01")},
		{name: "due before start", due: day(t, "2026-08-31"), started: day(t, "2026-09-01"), err: ErrDueBeforeStart},
		{name: "start null", due: day(t, "2026-09-01"), started: null},
		{name: "due null", due: null, started: day(t, "2026-09-01")},
		{name: "both null", due: null, started: null},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := DateOrder(tc.due, tc.started); !errors.Is(err, tc.err) {
				t.Fatalf("DateOrder error = %v, want %v", err, tc.err)
			}
		})
	}
}

// TestDateOrderComparesInstantsNotDays pins that two values on one
// calendar day are ordered by their clock time rather than read as
// equal. Every caller parses a date column to midnight, so the pair
// below cannot arrive through any current path; the case exists so the
// comparison's granularity is stated somewhere rather than inferred
// from a column type.
func TestDateOrderComparesInstantsNotDays(t *testing.T) {
	base := day(t, "2026-09-01")
	later := sql.NullTime{Time: base.Time.Add(6 * time.Hour), Valid: true}
	if err := DateOrder(base, later); !errors.Is(err, ErrDueBeforeStart) {
		t.Fatalf("DateOrder with a due instant before start = %v, want %v", err, ErrDueBeforeStart)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Violation
	}{
		{name: "nil", err: nil, want: ViolationNone},
		{name: "title", err: ErrTitleEmpty, want: ViolationTitleEmpty},
		{name: "dates", err: ErrDueBeforeStart, want: ViolationDueBeforeStart},
		{name: "wrapped title", err: fmtWrap(ErrTitleEmpty), want: ViolationTitleEmpty},
		{name: "foreign", err: errors.New("connection refused"), want: ViolationOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.err); got != tc.want {
				t.Fatalf("Classify(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func fmtWrap(err error) error {
	return errors.Join(errors.New("saving the task"), err)
}

// TestZeroTitleIsEmpty states what a field nobody filled in reads as.
// The write funnels refuse it on that basis, so it has to stay empty
// rather than becoming a plausible-looking title.
func TestZeroTitleIsEmpty(t *testing.T) {
	var zero Title
	if zero.String() != "" {
		t.Fatalf("zero Title = %q, want empty", zero.String())
	}
}

// TestTitleMarshalsAsString guards the event and audit payloads, which
// are map[string]any and would encode an unexported payload as {}. The
// rows are append-only, so a title recorded that way stays wrong.
func TestTitleMarshalsAsString(t *testing.T) {
	title, err := NewTitle("  ship it  ")
	if err != nil {
		t.Fatalf("NewTitle: %v", err)
	}
	payload, err := json.Marshal(map[string]any{"title": title})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got, want := string(payload), `{"title":"ship it"}`; got != want {
		t.Fatalf("payload = %s, want %s", got, want)
	}
}
