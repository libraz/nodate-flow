package sqlviews

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/eventacl"
)

// The row-level half of calendar event visibility has to live in the
// query, because a confidential event that is filtered out after the
// fact still contributed to whatever the query counted. That makes the
// filter something a new list endpoint can forget, which is exactly how
// eventacl ended up defined, tested and never called while confidential
// events were readable by every calendar member.
//
// So the fragment is a constant in eventacl and this test checks the
// query file still contains it, verbatim, in every list query over
// calendar_events. A copy that drifts is the failure mode; asserting on
// the shared constant is what makes drift detectable.

var queryNamePattern = regexp.MustCompile(`(?m)^-- name: (\w+) :`)

// detailMarkerForm is the machine-readable exemption from the detail half
// alone, written as a comment above the statement.
//
// It says the statement selects no field the detail rule governs, so
// attendance has nothing to unlock and projecting is_attendee would add a
// column no caller can read. It is spelled like the affected-rows marker in
// tests/affectedrows so a reader who knows one knows this one.
//
// The reason is mandatory and has to read as prose. An escape hatch that
// costs nothing to add is one that gets added without thinking, and this is
// the same rule the affected-rows marker holds for the same reason.
const detailMarkerForm = "detail-visibility: not-applicable — <which fields the statement selects instead>"

// detailMarkerPattern matches detailMarkerForm. Requiring the reason to
// start and end with a letter is what stops a mention of the marker from
// acting as one.
var detailMarkerPattern = regexp.MustCompile("detail-visibility:[ \t]*not-applicable[ \t]*—[ \t]*[A-Za-z][^\n]*[A-Za-z]")

// headerComment returns the comment block between a statement's `-- name:`
// header and its first line of SQL. Only that block can carry a marker: a
// comment further down sits inside the statement, and one below it belongs
// to whatever comes next.
func headerComment(body string) string {
	var out []string
	for i, line := range strings.Split(body, "\n") {
		if i == 0 {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func calendarEventQueries(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "sql", "queries", "calendars", "events.sql")
	raw, err := os.ReadFile(path) //#nosec G304 -- fixed repository path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)

	matches := queryNamePattern.FindAllStringSubmatchIndex(src, -1)
	if len(matches) == 0 {
		t.Fatalf("no queries found in %s", path)
	}
	out := make(map[string]string, len(matches))
	for i, m := range matches {
		name := src[m[2]:m[3]]
		end := len(src)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		out[name] = src[m[0]:end]
	}
	return out
}

// TestEventListQueriesCarryVisibilityFilter holds the two halves of event
// visibility over every list query on calendar_events, as two independent
// assertions:
//
//   - The row filter, eventacl.RowVisibilitySQL, decides which rows the
//     viewer may know exist. It is mandatory and takes no exemption:
//     attendance is deliberately not an exception to it, so there is no
//     statement for which the question does not arise.
//   - The detail filter, eventacl.AttendeeExistsSQL projected as
//     is_attendee, decides whether a private event's free-text fields —
//     title, location, memo, url — are the viewer's to read. It answers a
//     question only a statement that selects those fields asks.
//
// A statement that selects none of them can be exempted from the detail
// half alone, with detailMarkerForm above it. That is the whole of when the
// marker is legitimate: the fields the rule governs are absent, so
// attendance has nothing to unlock and the projected column would reach no
// mapper. It is not an exemption for a statement that finds the join
// expensive, or for one whose caller happens not to read the column today.
//
// The marker cannot reach the row half. The row assertion runs before it is
// consulted and never reads it, so no comment on a statement can make a
// confidential event listable.
func TestEventListQueriesCarryVisibilityFilter(t *testing.T) {
	t.Parallel()

	queries := calendarEventQueries(t)
	listed := 0
	for name, body := range queries {
		if !strings.HasPrefix(name, "List") {
			continue
		}
		listed++
		if !strings.Contains(body, eventacl.RowVisibilitySQL) {
			t.Errorf("%s does not AND in eventacl.RowVisibilitySQL; a confidential event would be listed to a co-member", name)
		}

		marked := detailMarkerPattern.MatchString(headerComment(body))
		switch {
		case strings.Contains(body, eventacl.AttendeeExistsSQL):
			if marked {
				t.Errorf("%s projects eventacl.AttendeeExistsSQL and carries a detail-visibility marker, "+
					"so the marker exempts nothing and a reader takes the statement for one that "+
					"selects no detail field; drop the marker", name)
			}
		case !marked:
			t.Errorf("%s does not project eventacl.AttendeeExistsSQL as is_attendee; private details "+
				"would be withheld from the people invited. Project it, or say above the statement "+
				"which fields it selects instead: %s", name, detailMarkerForm)
		}
	}
	if listed == 0 {
		t.Fatal("no List queries found; the guard is not looking at the file it thinks it is")
	}
	t.Logf("checked %d List queries over calendar_events", listed)
}

// TestDetailVisibilityMarkerNeedsAReason pins the rule that makes the
// marker worth reading: the exemption is the reason, so the token on its
// own is not one.
func TestDetailVisibilityMarkerNeedsAReason(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		comment string
		want    bool
	}{
		{"reason", "-- detail-visibility: not-applicable — the statement selects two internal ids.", true},
		{"bare", "-- detail-visibility: not-applicable", false},
		{"empty reason", "-- detail-visibility: not-applicable — ", false},
		{"hyphen instead of the dash", "-- detail-visibility: not-applicable - two ids.", false},
		{"mention", "-- see the detail-visibility rule in tests/sql", false},
	} {
		if got := detailMarkerPattern.MatchString(tc.comment); got != tc.want {
			t.Errorf("%s: matched=%v, want %v for %q", tc.name, got, tc.want, tc.comment)
		}
	}
}

// TestDetailVisibilityMarkerIsReadOnlyFromTheHeader pins where a marker
// counts. A comment inside the statement, or one trailing it, belongs to
// the SQL around it rather than to the statement's own contract, and
// reading the whole body would let either act as an exemption.
func TestDetailVisibilityMarkerIsReadOnlyFromTheHeader(t *testing.T) {
	t.Parallel()

	const body = `-- name: ListSomething :many
-- detail-visibility: not-applicable — the statement selects two internal ids.
SELECT a, b
FROM t
-- detail-visibility: not-applicable — this one sits inside the statement.
WHERE c = ?;`

	comment := headerComment(body)
	if !detailMarkerPattern.MatchString(comment) {
		t.Errorf("the header block was not read as the marker's home: %q", comment)
	}
	if strings.Contains(comment, "inside the statement") {
		t.Errorf("the comment block ran past the first line of SQL: %q", comment)
	}
}

// TestPublicShareRenderJoinsCalendars pins the other half of the same
// idea for the unauthenticated share page: an event whose calendar was
// deleted must stop being served. The editor query has always filtered
// on it, so when the render query did not, deleting a calendar left its
// events public and simultaneously removed them from the only screen
// that could unpublish them.
func TestPublicShareRenderJoinsCalendars(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "..", "..", "sql", "queries", "calendars", "public_shares.sql")
	raw, err := os.ReadFile(path) //#nosec G304 -- fixed repository path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)

	matches := queryNamePattern.FindAllStringSubmatchIndex(src, -1)
	bodies := map[string]string{}
	for i, m := range matches {
		end := len(src)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		bodies[src[m[2]:m[3]]] = src[m[0]:end]
	}

	const calendarJoin = "INNER JOIN calendars c ON c.id = ce.calendar_id AND c.enabled = TRUE"
	for _, name := range []string{"ListPublicShareEventsByTokenHash"} {
		body, ok := bodies[name]
		if !ok {
			t.Fatalf("%s not found in %s", name, path)
		}
		if !strings.Contains(body, calendarJoin) {
			t.Errorf("%s must join calendars on enabled = TRUE so a deleted calendar's events stop rendering", name)
		}
	}
}
