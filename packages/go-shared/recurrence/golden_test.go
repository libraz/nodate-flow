package recurrence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// testdata/recurrence_golden.json at the repository root is the one
// description of what a stored rule expands to. Every expander in the
// product reads it, so a case added there constrains all of them at once
// and a divergence surfaces as a failure rather than as two calendars
// that disagree.
//
// The browser expander (packages/ui/src/calendar/recurrence.ts) draws the
// calendar and the public share page from the same fixture. This package
// answers the agent surface and the notification scheduler, where nobody
// is looking at the result, which is exactly why it has to be pinned to
// the same expectations rather than to a suite of its own.

// goldenFixture is one case of the shared fixture file.
type goldenFixture struct {
	Name  string `json:"name"`
	Event struct {
		StartAt              string   `json:"startAt"`
		EndAt                string   `json:"endAt"`
		Timezone             string   `json:"timezone"`
		RecurrenceRule       *Rule    `json:"recurrenceRule"`
		RecurrenceExceptions []string `json:"recurrenceExceptions"`
		RecurrenceEnd        string   `json:"recurrenceEnd"`
	} `json:"event"`
	RangeStart      string   `json:"rangeStart"`
	RangeEnd        string   `json:"rangeEnd"`
	ExpectedStartAt []string `json:"expectedStartAt"`
}

// loadGoldenFixtures reads the shared fixture, located by walking up from
// this source file so the test does not depend on the working directory.
func loadGoldenFixtures(t *testing.T) []goldenFixture {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source file")
	}
	dir := filepath.Dir(file)
	for {
		candidate := filepath.Join(dir, "testdata", "recurrence_golden.json")
		if raw, err := os.ReadFile(candidate); err == nil { //#nosec G304 -- repository path
			var out []goldenFixture
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("decode %s: %v", candidate, err)
			}
			if len(out) == 0 {
				t.Fatalf("%s holds no cases; the expanders would be unconstrained", candidate)
			}
			return out
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find testdata/recurrence_golden.json above the package directory")
		}
		dir = parent
	}
}

func mustParseTime(t *testing.T, field, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %s %q: %v", field, value, err)
	}
	return parsed
}

// TestExpandMatchesSharedGoldenFixtures runs this expander against the
// fixture the browser expander is held to.
func TestExpandMatchesSharedGoldenFixtures(t *testing.T) {
	t.Parallel()

	for _, fixture := range loadGoldenFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()

			evt := Event{
				StartAt:    mustParseTime(t, "startAt", fixture.Event.StartAt),
				EndAt:      mustParseTime(t, "endAt", fixture.Event.EndAt),
				Timezone:   fixture.Event.Timezone,
				Rule:       fixture.Event.RecurrenceRule,
				Exceptions: fixture.Event.RecurrenceExceptions,
			}
			if fixture.Event.RecurrenceEnd != "" {
				end := mustParseTime(t, "recurrenceEnd", fixture.Event.RecurrenceEnd)
				evt.RecurrenceEnd = &end
			}

			occ := Expand(evt,
				mustParseTime(t, "rangeStart", fixture.RangeStart),
				mustParseTime(t, "rangeEnd", fixture.RangeEnd))

			got := make([]string, 0, len(occ))
			for _, o := range occ {
				got = append(got, o.StartAt.UTC().Format(time.RFC3339))
			}
			if len(got) != len(fixture.ExpectedStartAt) {
				t.Fatalf("occurrence count = %d, want %d\n got: %v\nwant: %v",
					len(got), len(fixture.ExpectedStartAt), got, fixture.ExpectedStartAt)
			}
			for i := range got {
				if got[i] != fixture.ExpectedStartAt[i] {
					t.Errorf("occurrence %d = %s, want %s", i, got[i], fixture.ExpectedStartAt[i])
				}
			}
		})
	}
}
