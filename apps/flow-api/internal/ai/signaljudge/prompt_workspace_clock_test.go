package signaljudge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// The judge decides whether something is overdue or has just happened by
// comparing dates against the "Now" line in its prompt. A workspace nine
// hours ahead of UTC judged against a UTC clock gets answers that read
// exactly like correct ones, so the only thing that separates a prompt
// built in the workspace's own frame of reference from one built in the
// fallback is the offset the timestamp carries — and, when the clock
// could not be read, the gap the builder records beside it.
//
// Every assertion below is on that offset or on that gap. Asserting that
// the timestamp is merely non-empty would pass against the UTC fallback,
// which is the state these tests exist to detect.

// fakeWorkspaceTimezone answers the timezone column the clock adapter
// reads, or fails the way the database would.
type fakeWorkspaceTimezone struct {
	tz  string
	err error
}

func (f fakeWorkspaceTimezone) FindWorkspaceTimezoneCountryById(_ context.Context, _ uint32) (generated.FindWorkspaceTimezoneCountryByIdRow, error) { //nolint:revive // method name mirrors the sqlc-generated Querier; renaming breaks interface satisfaction
	if f.err != nil {
		return generated.FindWorkspaceTimezoneCountryByIdRow{}, f.err
	}
	return generated.FindWorkspaceTimezoneCountryByIdRow{Timezone: f.tz}, nil
}

// offsetOf parses an RFC3339 timestamp and returns the zone offset it
// carries, in seconds east of UTC.
func offsetOf(t *testing.T, ts string) int {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("the clock produced %q, which is not the RFC3339 shape the "+
			"builder's own fallback produces: %v", ts, err)
	}
	_, off := parsed.Zone()
	return off
}

// TestWorkspaceNowCarriesTheWorkspaceOffset covers the two cases that are
// indistinguishable by string length: a workspace in a zone ahead of UTC,
// and one whose zone genuinely is UTC.
//
// The UTC workspace is the case worth stating: its timestamp is byte-wise
// the same as the fallback, so the only thing separating a configured UTC
// workspace from a clock that failed is the absence of a gap. A lookup
// that returned nothing for it would make the gap marker meaningless,
// because the default configuration would carry one.
func TestWorkspaceNowCarriesTheWorkspaceOffset(t *testing.T) {
	t.Parallel()

	// Both zones are fixed-offset, so the expected offset does not depend
	// on when the suite runs.
	cases := []string{"Asia/Tokyo", "Asia/Kolkata", "UTC"}

	for _, tz := range cases {
		t.Run(tz, func(t *testing.T) {
			t.Parallel()

			loc, err := time.LoadLocation(tz)
			if err != nil {
				t.Fatalf("the test environment has no zoneinfo entry for %q, so nothing "+
					"here is being measured: %v", tz, err)
			}
			_, want := time.Now().In(loc).Zone()

			deps := PromptDeps{WorkspaceNow: NewSQLWorkspaceNow(fakeWorkspaceTimezone{tz: tz})}
			pc, err := BuildPromptContext(context.Background(), deps, calendarEventSignal())
			if err != nil {
				t.Fatalf("a workspace whose timezone reads cleanly failed the build: %v", err)
			}
			if len(pc.ContextGaps) != 0 {
				t.Fatalf("the clock answered and the prompt still declares %v; a judge told its "+
					"frame of reference is missing caps its own confidence over nothing",
					pc.ContextGaps)
			}
			if got := offsetOf(t, pc.Now); got != want {
				t.Errorf("Now = %q, whose offset is %ds; the workspace is at %s (%ds), so the "+
					"judge is reasoning about due dates in the wrong frame",
					pc.Now, got, tz, want)
			}
		})
	}
}

// TestWorkspaceNowFailureIsWrittenDownNotSubstituted covers the three
// ways the lookup can fail to produce a workspace clock.
//
// UTC is a correct substitute and the run proceeds on it, but the
// substitution has to be legible: the prompt lands in
// ai_invocations.prompt_redacted, and an operator reading a verdict back
// cannot otherwise tell a timestamp the workspace chose from one the
// builder supplied for it.
func TestWorkspaceNowFailureIsWrittenDownNotSubstituted(t *testing.T) {
	t.Parallel()

	cases := map[string]WorkspaceTimezoneLoader{
		// The workspace row could not be read at all.
		"the row could not be read": fakeWorkspaceTimezone{err: errLookupFailed},
		// A stored zone name the zoneinfo database does not know. This is
		// the case the adapter must not resolve to UTC on its own: doing so
		// would leave a workspace with a broken timezone column looking
		// exactly like one that chose UTC.
		"the stored zone is unknown": fakeWorkspaceTimezone{tz: "Not/AZone"},
		// Wired with nothing behind it.
		"no loader": nil,
	}

	for name, loader := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			clock := NewSQLWorkspaceNow(loader)

			// The adapter's own contract first: an empty string with a nil
			// error would be read by the builder as a failure anyway, but
			// silently, and no caller could tell the two apart.
			ts, err := clock(context.Background(), 1)
			if err == nil {
				t.Fatalf("the clock reported success and returned %q for a workspace whose "+
					"timezone it never read", ts)
			}
			if ts != "" {
				t.Errorf("the clock returned %q alongside an error; a caller that ignores the "+
					"error renders a timestamp nothing stands behind", ts)
			}

			deps := PromptDeps{WorkspaceNow: clock}
			pc, buildErr := BuildPromptContext(context.Background(), deps, calendarEventSignal())
			if buildErr != nil {
				t.Fatalf("the workspace clock carries no evidence and has a correct substitute, "+
					"so its failure must not fail the run: %v", buildErr)
			}
			if len(pc.ContextGaps) != 1 || pc.ContextGaps[0] != ContextGapWorkspaceClock {
				t.Fatalf("the substitution was not recorded: %v", pc.ContextGaps)
			}
			if off := offsetOf(t, pc.Now); off != 0 {
				t.Errorf("the fallback timestamp %q carries offset %ds; the fallback is UTC and "+
					"a reader comparing it against a workspace-local one relies on that",
					pc.Now, off)
			}
		})
	}
}

// TestWorkspaceNowWithoutALoaderNamesTheMisWiring separates a clock that
// was never wired from one wired to nothing.
//
// A nil [PromptDeps.WorkspaceNow] is a deployment's choice and produces
// no gap; a constructed clock with no loader behind it is a mis-wiring,
// and it reports one on every run rather than resolving quietly to the
// same UTC the unwired case produces.
func TestWorkspaceNowWithoutALoaderNamesTheMisWiring(t *testing.T) {
	t.Parallel()

	_, err := NewSQLWorkspaceNow(nil)(context.Background(), 1)
	if !errors.Is(err, errNoWorkspaceTimezoneLoader) {
		t.Fatalf("a clock with no loader failed with %v, which does not say what is missing", err)
	}

	pc, err := BuildPromptContext(context.Background(), PromptDeps{}, calendarEventSignal())
	if err != nil {
		t.Fatalf("an unwired clock is not a failure: %v", err)
	}
	if len(pc.ContextGaps) != 0 {
		t.Errorf("a clock nobody wired is reported as a gap: %v", pc.ContextGaps)
	}
	if off := offsetOf(t, pc.Now); off != 0 {
		t.Errorf("the unwired build produced %q at offset %ds rather than UTC", pc.Now, off)
	}
}
