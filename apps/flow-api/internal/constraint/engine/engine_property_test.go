package engine

import (
	"context"
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/constraint"
)

// engine_property_test.go — property-based tests for the constraint
// engine. These verify structural invariants that must hold regardless
// of which constraints or facts are supplied.
//
//  1. EvaluateTask is idempotent: calling it twice produces the same outcomes.
//  2. EvaluateTask with empty facts never panics.
//  3. Outcome count always equals the number of constraint rows.
//  4. Parse errors never trigger MarkSatisfied / MarkFailed.

// trackingStore records calls so properties can inspect them.
type trackingStore struct {
	facts          constraint.Facts
	rows           []Row
	satisfiedCalls []string
	failedCalls    []string
}

func newTracking(facts constraint.Facts, rows []Row) *trackingStore {
	return &trackingStore{
		facts:          facts,
		rows:           rows,
		satisfiedCalls: []string{},
		failedCalls:    []string{},
	}
}

func (s *trackingStore) LoadTask(_ context.Context, _ uint32) (constraint.Facts, []Row, error) {
	return s.facts, s.rows, nil
}

func (s *trackingStore) MarkSatisfied(_ context.Context, id string, _ time.Time) error {
	s.satisfiedCalls = append(s.satisfiedCalls, id)
	return nil
}

func (s *trackingStore) MarkFailed(_ context.Context, id string, _ time.Time) error {
	s.failedCalls = append(s.failedCalls, id)
	return nil
}

func (s *trackingStore) reset() {
	s.satisfiedCalls = s.satisfiedCalls[:0]
	s.failedCalls = s.failedCalls[:0]
}

// validExpressions returns a set of valid constraint JSON strings.
func validExpressions() []string {
	return []string{
		`{"op":"time.due_before","arg":"2026-06-01"}`,
		`{"op":"time.due_after","arg":"2026-01-01"}`,
		`{"op":"dependency.all_done","taskIds":["a"]}`,
		`{"op":"actor.has_role","arg":"reviewer"}`,
		`{"op":"signal.received","arg":"github.pr.merged"}`,
		`{"op":"approval.granted","arg":"owner"}`,
		`{"op":"ci.status_is","arg":"success"}`,
		`{"op":"and","terms":[{"op":"ci.status_is","arg":"success"},{"op":"actor.has_role","arg":"reviewer"}]}`,
		`{"op":"not","term":{"op":"ci.status_is","arg":"failure"}}`,
	}
}

func randomFacts(rng *rand.Rand) constraint.Facts {
	var dueOn *time.Time
	if rng.Intn(2) == 0 {
		d := time.Date(2026, time.Month(rng.Intn(12)+1), rng.Intn(28)+1, 0, 0, 0, 0, time.UTC)
		dueOn = &d
	}
	deps := map[string]string{}
	if rng.Intn(2) == 0 {
		states := []string{"done", "waiting", "open", "cancelled"}
		deps["a"] = states[rng.Intn(len(states))]
	}
	roles := map[string]bool{}
	if rng.Intn(2) == 0 {
		roles["reviewer"] = true
	}
	signals := map[string]bool{}
	if rng.Intn(2) == 0 {
		signals["github.pr.merged"] = true
	}
	approvals := map[string]bool{}
	if rng.Intn(2) == 0 {
		approvals["owner"] = true
	}
	ciStatuses := []string{"", "success", "failure", "pending"}
	return constraint.Facts{
		Now:              time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
		DueOn:            dueOn,
		DependencyStates: deps,
		ActorRoles:       roles,
		SignalsReceived:  signals,
		Approvals:        approvals,
		CIStatus:         ciStatuses[rng.Intn(len(ciStatuses))],
	}
}

// TestProperty_Engine_Idempotent verifies that evaluating the same
// task twice produces identical outcome slices.
func TestProperty_Engine_Idempotent(t *testing.T) {
	t.Parallel()
	exprs := validExpressions()
	rng := rand.New(rand.NewSource(42))
	frozenNow := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	for iter := 0; iter < 100; iter++ {
		// Pick 1-4 random expressions.
		n := rng.Intn(4) + 1
		rows := make([]Row, n)
		for i := range rows {
			rows[i] = Row{
				PublicID:   "c" + string(rune('0'+i)),
				Expression: []byte(exprs[rng.Intn(len(exprs))]),
			}
		}
		facts := randomFacts(rng)

		// First evaluation.
		store1 := newTracking(facts, rows)
		eng1 := &Engine{Store: store1, Now: func() time.Time { return frozenNow }}
		out1, err1 := eng1.EvaluateTask(context.Background(), 1)
		if err1 != nil {
			t.Fatalf("iter %d: first eval error: %v", iter, err1)
		}

		// Second evaluation with a fresh store (same data).
		store2 := newTracking(facts, rows)
		eng2 := &Engine{Store: store2, Now: func() time.Time { return frozenNow }}
		out2, err2 := eng2.EvaluateTask(context.Background(), 1)
		if err2 != nil {
			t.Fatalf("iter %d: second eval error: %v", iter, err2)
		}

		if len(out1) != len(out2) {
			t.Fatalf("iter %d: outcome count mismatch: %d vs %d", iter, len(out1), len(out2))
		}
		for i := range out1 {
			if out1[i].Satisfied != out2[i].Satisfied {
				t.Fatalf("iter %d row %d: satisfied mismatch: %v vs %v", iter, i, out1[i].Satisfied, out2[i].Satisfied)
			}
			if (out1[i].ParseError == nil) != (out2[i].ParseError == nil) {
				t.Fatalf("iter %d row %d: parse error presence mismatch", iter, i)
			}
		}
	}
}

// TestProperty_Engine_EmptyFacts verifies the engine never panics
// when facts are completely empty.
func TestProperty_Engine_EmptyFacts(t *testing.T) {
	t.Parallel()
	exprs := validExpressions()
	frozenNow := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	rows := make([]Row, len(exprs))
	for i, expr := range exprs {
		rows[i] = Row{PublicID: "e" + string(rune('0'+i)), Expression: []byte(expr)}
	}

	store := newTracking(constraint.Facts{}, rows)
	eng := &Engine{Store: store, Now: func() time.Time { return frozenNow }}
	out, err := eng.EvaluateTask(context.Background(), 1)
	if err != nil {
		t.Fatalf("empty facts should not error: %v", err)
	}
	if len(out) != len(rows) {
		t.Fatalf("outcome count %d != row count %d", len(out), len(rows))
	}
}

// TestProperty_Engine_OutcomeCountEqualsRowCount verifies the engine
// always produces exactly one outcome per constraint row, regardless
// of whether each row parses or evaluates successfully.
func TestProperty_Engine_OutcomeCountEqualsRowCount(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(88))
	frozenNow := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	// Mix valid and invalid expressions.
	allExprs := append(validExpressions(),
		`{"op":"bogus"}`,
		`not json`,
		`{"op":"and","terms":[]}`,
	)

	for iter := 0; iter < 50; iter++ {
		n := rng.Intn(6) + 1
		rows := make([]Row, n)
		for i := range rows {
			rows[i] = Row{
				PublicID:   "r" + string(rune('a'+i)),
				Expression: []byte(allExprs[rng.Intn(len(allExprs))]),
			}
		}
		facts := randomFacts(rng)
		store := newTracking(facts, rows)
		eng := &Engine{Store: store, Now: func() time.Time { return frozenNow }}
		out, err := eng.EvaluateTask(context.Background(), 1)
		if err != nil {
			t.Fatalf("iter %d: unexpected error: %v", iter, err)
		}
		if len(out) != n {
			t.Fatalf("iter %d: expected %d outcomes, got %d", iter, n, len(out))
		}
	}
}

// TestProperty_Engine_ParseErrorNeverMarked verifies that a constraint
// row with an unparseable expression never has MarkSatisfied or
// MarkFailed called for it.
func TestProperty_Engine_ParseErrorNeverMarked(t *testing.T) {
	t.Parallel()
	frozenNow := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	badExprs := []string{
		`{"op":"bogus"}`,
		`not json at all`,
		`{"op":"and","terms":[]}`,
		`{"op":"dependency.open_at_most"}`,
	}

	for _, expr := range badExprs {
		store := newTracking(constraint.Facts{}, []Row{
			{PublicID: "bad1", Expression: []byte(expr)},
		})
		eng := &Engine{Store: store, Now: func() time.Time { return frozenNow }}
		out, err := eng.EvaluateTask(context.Background(), 1)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", expr, err)
		}
		if len(out) != 1 {
			t.Fatalf("expected 1 outcome for %q, got %d", expr, len(out))
		}
		if out[0].ParseError == nil {
			t.Fatalf("expected parse error for %q", expr)
		}
		if len(store.satisfiedCalls) != 0 || len(store.failedCalls) != 0 {
			t.Fatalf("store should not be called for parse-error row %q: satisfied=%v failed=%v",
				expr, store.satisfiedCalls, store.failedCalls)
		}
	}
}

// TestProperty_Engine_ValidRowAlwaysMarked verifies that every valid
// constraint row results in exactly one MarkSatisfied or MarkFailed
// call.
func TestProperty_Engine_ValidRowAlwaysMarked(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(55))
	frozenNow := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	exprs := validExpressions()

	for iter := 0; iter < 50; iter++ {
		n := rng.Intn(4) + 1
		rows := make([]Row, n)
		ids := make([]string, n)
		for i := range rows {
			id := "v" + string(rune('a'+i))
			ids[i] = id
			rows[i] = Row{
				PublicID:   id,
				Expression: []byte(exprs[rng.Intn(len(exprs))]),
			}
		}
		facts := randomFacts(rng)
		store := newTracking(facts, rows)
		eng := &Engine{Store: store, Now: func() time.Time { return frozenNow }}
		_, err := eng.EvaluateTask(context.Background(), 1)
		if err != nil {
			t.Fatalf("iter %d: unexpected error: %v", iter, err)
		}

		// Every valid row must appear in exactly one of satisfied or failed.
		for _, id := range ids {
			sCount := countOccurrences(store.satisfiedCalls, id)
			fCount := countOccurrences(store.failedCalls, id)
			total := sCount + fCount
			if total != 1 {
				data, _ := json.Marshal(rows)
				t.Fatalf("iter %d: row %s marked %d times (satisfied=%d, failed=%d)\nrows: %s",
					iter, id, total, sCount, fCount, data)
			}
		}
	}
}

func countOccurrences(ss []string, target string) int {
	n := 0
	for _, s := range ss {
		if s == target {
			n++
		}
	}
	return n
}
