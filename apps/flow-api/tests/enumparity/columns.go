package enumparity

import (
	"sort"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/columnbounds"
)

// Placement is one wire field together with the ENUM column it writes.
//
// The column is found by the same resolution that places a length bound:
// the resource an operation's name states and the tables its own statements
// write, or the statements the handler taking the input calls. Nothing is
// listed, and a field two tables could hold is left unplaced rather than
// guessed at.
type Placement struct {
	Field
	Column columnbounds.Column
	// Rule is which of the two derivations placed it. A placement from the
	// owner's name can be confirmed by reading the name; one from the
	// statements a handler calls cannot, so the two are reported apart.
	Rule columnbounds.Rule
}

// Declared returns the values the field states it accepts. An open field
// states none.
func (p Placement) Declared() []string {
	if p.Enum == "" {
		return nil
	}
	var out []string
	for _, v := range strings.Split(p.Enum, ",") {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// Verdict is what a field's stated values amount to against the column
// behind them.
type Verdict string

const (
	// Matches: the field states exactly what the column accepts.
	Matches Verdict = "matches"
	// Open: the field states nothing, so every client is told it takes any
	// string while the column takes a handful.
	Open Verdict = "open"
	// Narrows: the field states a strict subset. An operation may take less
	// than its column holds — an RSVP recorded through an invite cannot be
	// pending, a member cannot be added as owner though one can be promoted
	// to it, a dependency kind the system writes is not one a caller may
	// ask for — so this is reported rather than refused.
	Narrows Verdict = "narrows"
	// Overstates: the field states a value the column does not accept. The
	// contract promises what storage refuses, whichever side is right.
	Overstates Verdict = "overstates"
)

// Comparison is one placement together with what its stated values amount
// to.
type Comparison struct {
	Placement
	// Missing are the column's values the field does not state, and Extra
	// the ones it states that the column does not accept.
	Missing []string
	Extra   []string
	Verdict Verdict
}

// PlaceOnEnums resolves the fields that write an ENUM column onto it.
//
// The evidence is the one the length check assembles, read through the
// schema's ENUM columns rather than its length-bounded ones. Everything
// else — which tables a surface writes, which statements a handler calls,
// which of them write what — is the same reading, because where a field
// lands is the same question whichever property of it is being checked.
//
// A repeated field is left out. It carries a set of values into rows of
// another table rather than one value into a column, so a column of that
// name is not where it lands.
func PlaceOnEnums(fields []Field, ev columnbounds.Evidence) []Placement {
	ev.Schema = ev.Schema.EnumsOnly()

	decls := make([]columnbounds.Declaration, 0, len(fields))
	source := make([]int, 0, len(fields))
	for i, f := range fields {
		if f.Repeated {
			continue
		}
		decls = append(decls, columnbounds.RESTDeclaration(
			f.Package, f.Owner, f.Section, f.Name, f.Path, f.Line))
		source = append(source, i)
	}

	var out []Placement
	for i, r := range columnbounds.ResolveAll(decls, ev) {
		if !r.Placed {
			continue
		}
		out = append(out, Placement{Field: fields[source[i]], Column: r.Column, Rule: r.Rule})
	}
	return out
}

// Compare states, for every placement, what its stated values amount to
// against its column.
//
// It is separate from the caller's decision because the failure mode of a
// derived check is that the derivation stops matching — a renamed table, a
// schema dump that changed shape — and then it passes because it compared
// nothing. The caller asserts on the placements for that reason, and on
// this to see the shapes it is not refusing.
func Compare(placements []Placement) []Comparison {
	out := make([]Comparison, 0, len(placements))
	for _, p := range placements {
		declared := map[string]bool{}
		for _, v := range p.Declared() {
			declared[v] = true
		}
		accepted := map[string]bool{}
		for _, v := range p.Column.Members {
			accepted[v] = true
		}

		c := Comparison{Placement: p}
		for _, v := range p.Column.Members {
			if !declared[v] {
				c.Missing = append(c.Missing, v)
			}
		}
		for _, v := range p.Declared() {
			if !accepted[v] {
				c.Extra = append(c.Extra, v)
			}
		}
		switch {
		case p.Enum == "":
			c.Verdict = Open
		case len(c.Extra) > 0:
			c.Verdict = Overstates
		case len(c.Missing) > 0:
			c.Verdict = Narrows
		default:
			c.Verdict = Matches
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// Violations narrows the comparisons down to the ones no reading of the
// operation excuses: a field stating nothing, and a field stating a value
// its column refuses.
//
// A strict subset is deliberately not one. This repository already holds
// that two operations may accept different subsets of a column, and the
// tree carries narrowings that are the point of the operation rather than
// an oversight. Refusing them would put an exemption on more declarations
// than the rule caught, and an exemption most of them carry is one nobody
// reads. They are printed instead.
func Violations(comparisons []Comparison) []Comparison {
	var out []Comparison
	for _, c := range comparisons {
		if c.Verdict == Open || c.Verdict == Overstates {
			out = append(out, c)
		}
	}
	return out
}

// WithVerdict narrows the comparisons down to one verdict.
func WithVerdict(comparisons []Comparison, v Verdict) []Comparison {
	var out []Comparison
	for _, c := range comparisons {
		if c.Verdict == v {
			out = append(out, c)
		}
	}
	return out
}
