package ai

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// freezeClock pins the boundary clock for the duration of a test. Two
// boundaries read from the wall clock agree except when the reads straddle
// a midnight, so comparing them would describe a test that is correct
// almost always — which is the kind of test that eventually gets loosened
// rather than believed.
func freezeClock(t *testing.T, at time.Time) {
	t.Helper()
	prev := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = prev })
}

// TestBudgetWindowHasOneDefinition compares the actual boundaries the
// cost queries are bounded by.
//
// Three call sites — the completion guard, the embed guard, and the
// cost-today meter — each used to compute their own: the workspace's local
// midnight, UTC midnight, and a timezone the client asked for. Every one
// of them was defensible read on its own, which is why the defect survived
// review; it exists only between them, as a workspace whose AI is refused
// while its own meter reports that nothing has been spent.
//
// So the assertion is over values, not over how the values were spelled.
// Both query builders are asked for the same workspace at the same instant
// and have to answer with the same moment, and that moment has to be the
// workspace's midnight rather than UTC's.
func TestBudgetWindowHasOneDefinition(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load Asia/Tokyo: %v", err)
	}
	// 08:00 in Tokyo is still the previous day in UTC, so a boundary taken
	// in the wrong zone lands on a different date rather than merely a
	// different hour.
	freezeClock(t, time.Date(2026, 7, 18, 8, 0, 0, 0, tokyo))

	const workspaceID uint32 = 77
	loader := fakeTZLoader{tz: "Asia/Tokyo"}
	ctx := context.Background()

	want := time.Date(2026, 7, 18, 0, 0, 0, 0, tokyo)
	utcMidnight := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)

	completion := DailyCostParams(ctx, loader, workspaceID)
	embed := DailyEmbedCostParams(ctx, loader, workspaceID)

	windows := []struct {
		name string
		at   time.Time
	}{
		{"completion budget", completion.InvokedAt},
		{"embed budget", embed.InvokedAt},
		{"WorkspaceDayStart", WorkspaceDayStart(ctx, loader, workspaceID)},
	}
	for _, w := range windows {
		if !w.at.Equal(want) {
			t.Errorf("%s window starts at %s, want the workspace's own midnight %s", w.name, w.at, want)
		}
		if w.at.Equal(utcMidnight) {
			t.Errorf("%s window starts at UTC midnight; the workspace's day is not UTC's", w.name)
		}
	}
	if !completion.InvokedAt.Equal(embed.InvokedAt) {
		t.Errorf("completion window %s and embed window %s are different moments; "+
			"the two budgets would reset hours apart and no meter would show why",
			completion.InvokedAt, embed.InvokedAt)
	}

	if completion.WorkspaceID != workspaceID || embed.WorkspaceID != workspaceID {
		t.Errorf("workspace ids = %d / %d, want %d on both", completion.WorkspaceID, embed.WorkspaceID, workspaceID)
	}
}

// TestBudgetWindowFollowsTheWorkspaceNotTheServer checks the boundary
// actually depends on the workspace it was asked about. A builder that
// ignored the loader would satisfy the comparison above by returning one
// wrong answer consistently.
func TestBudgetWindowFollowsTheWorkspaceNotTheServer(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load Asia/Tokyo: %v", err)
	}
	freezeClock(t, time.Date(2026, 7, 18, 8, 0, 0, 0, tokyo))
	ctx := context.Background()

	inTokyo := DailyCostParams(ctx, fakeTZLoader{tz: "Asia/Tokyo"}, 1).InvokedAt
	inUTC := DailyCostParams(ctx, fakeTZLoader{tz: "UTC"}, 2).InvokedAt

	if inTokyo.Equal(inUTC) {
		t.Fatalf("both workspaces got the window %s; the boundary is not reading the workspace timezone", inTokyo)
	}
	if got := inUTC.UTC(); got.Hour() != 0 {
		t.Errorf("UTC workspace window = %s, want midnight UTC", got)
	}
}

// costParamsLiterals are the composite literals that let a call site
// choose the lower bound of a daily AI-spend query for itself.
var costParamsLiterals = []string{
	"SumAiCostTodayForWorkspaceParams{",
	"SumEmbedCostTodayForWorkspaceParams{",
}

// costParamsExemptDirs are the directories allowed to name those literals:
// this package, which owns the definition of the window, and the sqlc
// output that declares the types. Paths are slash-separated and matched as
// prefixes relative to the flow-api module root.
var costParamsExemptDirs = []string{
	"internal/ai",
	"internal/db/generated",
}

// costParamsWalkSentinel is a file the walk below must have inspected.
// "No file does X" is satisfied just as well by a walk that stopped
// finding files at all, so a known former call site is checked to have
// been read.
const costParamsWalkSentinel = "internal/http/router/router.go"

// TestCostQueryParamsAreCentralized is the structural half: nothing
// outside this package may build the query parameters, so there is no
// place left to write a boundary by hand.
//
// It is deliberately a rule about where a literal appears rather than
// about what the literal contains. A check on the contents cannot tell a
// correct call site from an incorrect one — assigning the boundary to a
// local variable first is ordinary Go and reads as a violation — whereas
// the type name is either written in a file or it is not. Nothing
// legitimate gets caught, and the only way past it is to construct the
// struct somewhere else, which is exactly the thing being forbidden.
//
// Test files are exempt: a test that drives the query directly is not a
// second definition of the day.
func TestCostQueryParamsAreCentralized(t *testing.T) {
	t.Parallel()

	root := costGuardModuleRoot(t)
	var offenders []string
	var sawSentinel bool

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		for _, dir := range costParamsExemptDirs {
			if strings.HasPrefix(rel, dir+"/") {
				return nil
			}
		}
		if rel == costParamsWalkSentinel {
			sawSentinel = true
		}
		// The walk root is this repository's own source tree, supplied by
		// the test, not by anything a caller controls.
		b, readErr := os.ReadFile(path) //#nosec G122 -- walk root is the repo source tree, fixed by the test
		if readErr != nil {
			return readErr
		}
		for _, lit := range costParamsLiterals {
			if strings.Contains(string(b), lit) {
				offenders = append(offenders, rel+" ("+lit+")")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
	if !sawSentinel {
		t.Fatalf("the walk never reached %s, so it proved nothing; "+
			"the module layout moved and this guard needs its sentinel updated", costParamsWalkSentinel)
	}

	if len(offenders) > 0 {
		t.Fatalf("daily AI-cost queries must take their parameters from ai.DailyCostParams / ai.DailyEmbedCostParams "+
			"so every budget covers the same day; these files build them themselves:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestCostMeterDoesNotResolveAClientTimezone states the other half of the
// rule for the one endpoint that reports a window as well as enforcing
// one: it has no timezone of its own to resolve. Resolving one is how the
// displayed day came to disagree with the guarded day.
func TestCostMeterDoesNotResolveAClientTimezone(t *testing.T) {
	t.Parallel()

	const path = "../http/handlers/ai/cost_today.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.Contains(string(src), "time.LoadLocation(") {
		t.Errorf("%s resolves a timezone of its own; the window belongs to the workspace, not to the caller", path)
	}
}

// costGuardModuleRoot returns the apps/flow-api directory. Tests run in
// the package directory, so the module root is two levels up from
// internal/ai.
func costGuardModuleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected the flow-api module root at %s: %v", root, err)
	}
	return root
}
