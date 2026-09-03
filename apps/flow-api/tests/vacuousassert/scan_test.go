package vacuousassert

import (
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// TestNoUnprovenNegation refuses a NotContains check against a response
// body whose needle nothing in the same function shows to be
// discoverable, and no non-emptiness proof rescues.
//
// A refusal body that never carries anything and a refusal body that
// never carries the one secret worth hiding pass this assertion
// identically; only a positive counterpart — the same text found by
// Contains or Equal elsewhere in the test, or any NotEmpty/Len proof
// that the surface under test is not just empty — tells them apart.
func TestNoUnprovenNegation(t *testing.T) {
	t.Parallel()

	negations, _, _ := scanRepository(t)
	for _, n := range negations {
		if n.Marked {
			continue
		}
		t.Errorf("%s: %s asserts the response never carries %s, but nothing in the function "+
			"shows that value is discoverable at all — no Contains/Equal on it, and no "+
			"NotEmpty/Len proof that the response held anything. Add a positive read that "+
			"finds it (by the resource's rightful owner, for a tenant-isolation check), or "+
			"say here why none is needed: %s",
			n.Location(), n.Function, n.Needle, MarkerForm)
	}
}

// TestNoUnprovenLoop refuses a range loop over a database-row scan that
// asserts per element without first proving the scan found any rows.
func TestNoUnprovenLoop(t *testing.T) {
	t.Parallel()

	_, loops, _ := scanRepository(t)
	for _, l := range loops {
		if l.Marked {
			continue
		}
		t.Errorf("%s: %s ranges over %s and asserts on each element, but the scan that built "+
			"%s is never shown to have found a row. A row-scan loop with no matches runs no "+
			"iterations and this test would report the same result either way. Add "+
			"require.NotEmpty(t, %s, ...) after the scan, or say here why the loop cannot go "+
			"empty: %s",
			l.Location(), l.Function, l.Variable, l.Variable, l.Variable, MarkerForm)
	}
}

// TestNoStaleVacuousAssertMarker drops a marker that covers no finding.
//
// A marker is a claim that a specific negation or loop in this function
// needs no positive counterpart. Once that call is removed or rewritten
// to carry one, the claim is no longer about anything, and a reader who
// finds it there concludes the shape was considered and cleared.
func TestNoStaleVacuousAssertMarker(t *testing.T) {
	t.Parallel()

	_, _, stale := scanRepository(t)
	for _, m := range stale {
		t.Errorf("%s:%d: this vacuous-assert marker covers no unproven negation or loop in "+
			"its function. It exempts nothing and reads as though something was checked; "+
			"drop it", m.File, m.Line)
	}
}

// TestVacuousAssertMarkerNeedsAReason pins the rule that makes the
// marker worth reading: the exemption is the reason, so the token alone
// is not one.
func TestVacuousAssertMarkerNeedsAReason(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		comment string
		want    bool
	}{
		{"reason", "// vacuous-assert: not-applicable — the needle is a schema field name that must never appear anywhere.", true},
		{"bare", "// vacuous-assert: not-applicable", false},
		{"empty reason", "// vacuous-assert: not-applicable — ", false},
		{"hyphen instead of the dash", "// vacuous-assert: not-applicable - a reason.", false},
		{"mention", "// see the vacuous-assert rule in tests/vacuousassert", false},
	} {
		if got := MarkerPattern.MatchString(tc.comment); got != tc.want {
			t.Errorf("%s: matched=%v, want %v for %q", tc.name, got, tc.want, tc.comment)
		}
	}
}

// TestScanSeesAnUnprovenNegationAndLoop is the positive control. It
// proves the scan reports what it is meant to report — an unproven
// negation, an unproven loop, a marker that exempts one of each, and a
// marker left over — rather than the whole check passing because the
// detector quietly stopped matching.
//
// It also pins the hole a text-based version of this check fell into
// once already: a mention of the marker text inside a doc comment, or
// a needle that only appears inside a comment rather than in a Contains
// call, must not count as coverage. Both are present in the sample and
// must still be reported.
func TestScanSeesAnUnprovenNegationAndLoop(t *testing.T) {
	t.Parallel()

	const src = `package p

// TestUnproven asserts a refusal never carries "Secret Doc". A comment
// mentioning Secret Doc is not a Contains call, and must not count as
// proof that the needle is discoverable.
func TestUnproven(t *testing.T) {
	status, body := doRequest()
	requireDenied(t, status)
	require.NotContains(t, string(body), "Secret Doc", "must not leak")
}

// TestProvenBySameNeedle carries a positive Contains on the same text,
// so the negation below needs no marker.
func TestProvenBySameNeedle(t *testing.T) {
	status, body := doRequest()
	require.NotContains(t, string(body), "Secret Doc", "must not leak to the outsider")
	require.Contains(t, string(body), "Secret Doc", "must still be visible to the owner")
	_ = status
}

// TestMarkedNegation carries an exemption for its own negation.
//
// vacuous-assert: not-applicable — the needle is a schema field name that must never appear.
func TestMarkedNegation(t *testing.T) {
	body := loadBody()
	require.NotContains(t, string(body), "token_hash", "must never appear")
}

// TestStaleMarker carries a marker nothing here needs.
//
// vacuous-assert: not-applicable — this exemption covers no call below.
func TestStaleMarker(t *testing.T) {
	body := loadBody()
	require.Contains(t, string(body), "fine", "already proven, no negation here")
	_ = body
}

// TestUnprovenLoopSample scans rows into a slice and asserts on every
// element without ever proving the scan found one.
func TestUnprovenLoopSample(t *testing.T) {
	var got []string
	rows := query()
	for rows.Next() {
		var v string
		rows.Scan(&v)
		got = append(got, v)
	}
	for _, v := range got {
		require.NotContains(t, v, "token", "leaked")
	}
}

// TestMarkedLoopSample carries an exemption for its own loop.
//
// vacuous-assert: not-applicable — the join guarantees at least one row when the fixture exists.
func TestMarkedLoopSample(t *testing.T) {
	var got []string
	rows := query()
	for rows.Next() {
		var v string
		rows.Scan(&v)
		got = append(got, v)
	}
	for _, v := range got {
		require.NotContains(t, v, "token", "leaked")
	}
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample_test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}
	negations, loops, stale := scanFile(fset, file, "sample_test.go")

	var unprovenLines []int
	for _, n := range negations {
		if !n.Marked {
			unprovenLines = append(unprovenLines, n.Line)
		}
	}
	if want := []int{9}; !slices.Equal(unprovenLines, want) {
		t.Errorf("unproven negations at lines %v, want %v", unprovenLines, want)
	}

	var markedNegationLines []int
	for _, n := range negations {
		if n.Marked {
			markedNegationLines = append(markedNegationLines, n.Line)
		}
	}
	if want := []int{26}; !slices.Equal(markedNegationLines, want) {
		t.Errorf("marked negations at lines %v, want %v", markedNegationLines, want)
	}

	var unprovenLoopLines []int
	for _, l := range loops {
		if !l.Marked {
			unprovenLoopLines = append(unprovenLoopLines, l.Line)
		}
	}
	if len(unprovenLoopLines) != 1 {
		t.Errorf("unproven loops at lines %v, want exactly one", unprovenLoopLines)
	}

	var markedLoopLines []int
	for _, l := range loops {
		if l.Marked {
			markedLoopLines = append(markedLoopLines, l.Line)
		}
	}
	if len(markedLoopLines) != 1 {
		t.Errorf("marked loops at lines %v, want exactly one", markedLoopLines)
	}

	var staleLines []int
	for _, m := range stale {
		staleLines = append(staleLines, m.Line)
	}
	if len(staleLines) != 1 {
		t.Errorf("stale markers at lines %v, want exactly one", staleLines)
	}
}

// scanRepository runs Scan against the real repository tree and fails
// when it finds nothing: this check reads files by path, and a
// derivation that has stopped matching passes for the wrong reason.
func scanRepository(t *testing.T) ([]UnprovenNegation, []UnprovenLoop, []StaleMarker) {
	t.Helper()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	files, err := TestFiles(root)
	if err != nil {
		t.Fatalf("list test files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no *_test.go file found under the configured roots; the file walk has " +
			"stopped matching rather than the tests having gone away")
	}

	negations, loops, stale, err := Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return negations, loops, stale
}
