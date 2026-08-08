package calendars

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestInvitesResolveAttendeesWithoutARoundTripEach guards the shape of
// the invite handlers' attendee resolution.
//
// calendar_event_invites.attendee_id is an internal FK, and the attendee
// list query used not to select it, so both invite handlers recovered it
// by calling FindCalendarEventAttendee once per listed attendee. On an
// all-hands event that is one query per invitee — roughly three hundred
// round trips for a single request, on the *unauthenticated* accept
// endpoint, which anyone holding a link can drive. The list query now
// returns a.id (tagged json:"-" so it stays off the API boundary) and
// both call sites index the rows they already have.
//
// The check is on the source rather than on a timing, because a count
// of round trips is exactly what a unit test cannot observe and exactly
// what regressed. Reinstating a per-attendee lookup — the mutation that
// undoes this — puts the call back inside a loop, which fails here.
func TestInvitesResolveAttendeesWithoutARoundTripEach(t *testing.T) {
	t.Parallel()

	const file = "invites.go"
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	var offenders []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		var body *ast.BlockStmt
		switch loop := n.(type) {
		case *ast.RangeStmt:
			body = loop.Body
		case *ast.ForStmt:
			body = loop.Body
		default:
			return true
		}
		ast.Inspect(body, func(inner ast.Node) bool {
			sel, ok := inner.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "FindCalendarEventAttendee" {
				return true
			}
			offenders = append(offenders, fset.Position(sel.Pos()).String())
			return true
		})
		return true
	})

	for _, pos := range offenders {
		t.Errorf("%s: FindCalendarEventAttendee inside a loop turns one invite request into "+
			"one query per attendee; the list query returns a.id, so index those rows instead", pos)
	}
}
