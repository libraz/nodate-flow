package constraint

import (
	"errors"
	"testing"
	"time"
)

// FuzzParse ensures the DSL parser never panics on arbitrary bytes and
// that any returned error is always *ParseError / errors.Is(ErrParse).
// When parsing succeeds, re-evaluating the result must also never panic
// (3.TEST-2 fuzz coverage).
func FuzzParse(f *testing.F) {
	seeds := []string{
		`{"op":"and","terms":[{"op":"time.due_before","arg":"2026-01-01"}]}`,
		`{"op":"not","term":{"op":"ci.status_is","arg":"success"}}`,
		`{"op":"dependency.open_at_most","max":2}`,
		`{"op":"or","terms":[]}`,
		`{"op":"unknown"}`,
		`not json`,
		`{}`,
		`{"op":"and","terms":[{"op":"actor.has_role","arg":"reviewer"}]}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	facts := Facts{
		Now:              time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC),
		DependencyStates: map[string]string{"a": "done"},
		ActorRoles:       map[string]bool{"reviewer": true},
		SignalsReceived:  map[string]bool{"github.pr.merged": true},
		Approvals:        map[string]bool{"lead": true},
		CIStatus:         "success",
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		c, err := Parse(raw)
		if err != nil {
			if !errors.Is(err, ErrParse) {
				t.Fatalf("parse error not ErrParse: %v", err)
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("parse error not *ParseError: %T", err)
			}
			return
		}
		// Parsed successfully — Evaluate must not panic regardless of
		// facts, and if it returns an error it must be non-panicking.
		_, _ = Evaluate(c, facts)
	})
}
