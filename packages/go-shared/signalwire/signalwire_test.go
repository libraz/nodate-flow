package signalwire

import (
	"strings"
	"testing"
)

// TestSourceEnumTagMatchesList guards the Huma enum tag derivation: the
// tag string must be the wire-enum-ordered comma join of the canonical
// sources.
func TestSourceEnumTagMatchesList(t *testing.T) {
	want := strings.Join(SourceStrings(), ",")
	if got := SourceEnumTag(); got != want {
		t.Fatalf("SourceEnumTag() = %q, want %q", got, want)
	}
}

// TestIsSource exercises membership for a canonical value and a bogus
// one.
func TestIsSource(t *testing.T) {
	if !IsSource("discord") {
		t.Fatal("discord must be a canonical source")
	}
	if IsSource("teams") {
		t.Fatal("teams is not (currently) a canonical source")
	}
}

// TestAssertSourcesCovered confirms the one-directional invariant: a
// subset of the wire enum passes, a stray source fails and is named.
func TestAssertSourcesCovered(t *testing.T) {
	if err := AssertSourcesCovered([]string{"manual", "discord", "calendar"}); err != nil {
		t.Fatalf("subset of wire enum should pass: %v", err)
	}
	err := AssertSourcesCovered([]string{"manual", "teams", "weather"})
	if err == nil {
		t.Fatal("a non-wire source must fail the assertion")
	}
	if !strings.Contains(err.Error(), "teams") || !strings.Contains(err.Error(), "weather") {
		t.Fatalf("error must name the offending sources, got: %v", err)
	}
}

// TestSourcesIsCopy ensures callers cannot mutate the canonical list.
func TestSourcesIsCopy(t *testing.T) {
	got := Sources()
	got[0] = "mutated"
	if Sources()[0] == "mutated" {
		t.Fatal("Sources() must return a defensive copy")
	}
}
