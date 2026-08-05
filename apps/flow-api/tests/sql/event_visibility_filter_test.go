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

// TestEventListQueriesCarryVisibilityFilter asserts every list query
// over calendar_events AND-s in the shared row filter. A new endpoint
// that lists events and forgets it fails here rather than in production.
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
		if !strings.Contains(body, eventacl.AttendeeExistsSQL) {
			t.Errorf("%s does not project eventacl.AttendeeExistsSQL as is_attendee; private details would be withheld from the people invited", name)
		}
	}
	if listed == 0 {
		t.Fatal("no List queries found; the guard is not looking at the file it thinks it is")
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
