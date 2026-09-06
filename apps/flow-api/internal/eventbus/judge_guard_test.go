// Package eventbus — unit tests for the judge-kind runtime guard
// added in ADR 0008 D4. The guard rejects [Append] calls that try to
// emit event kinds reserved for the signaljudge Applier and forces
// callers to go through [AppendJudgeEvent] instead.
package eventbus

import (
	"context"
	"errors"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
)

// TestAppendRejectsJudgeKindsFromOutsideApplier locks in that calling
// [Append] with one of the judge-only event kinds short-circuits with
// INTERNAL.EVENTBUS.JUDGE_KIND_OUTSIDE_APPLIER before any DB write
// happens. The Applier is the sole writer of these kinds.
//
// The boundary wraps a nil pool, so any code path that reached a
// statement would panic instead of silently succeeding — the test passes
// only if the guard fires before the INSERT.
func TestAppendRejectsJudgeKindsFromOutsideApplier(t *testing.T) {
	t.Parallel()

	judgeKinds := []Kind{
		TaskAutoCompleted,
		TaskRetroDrafted,
		SignalJudged,
		SignalApplied,
		SignalRejected,
	}

	for _, kind := range judgeKinds {
		kind := kind
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			err := Append(context.Background(), dbretry.AutoCommit(nil), Event{
				Type:        kind,
				WorkspaceID: 1,
			})
			if err == nil {
				t.Fatalf("Append(%q): want guard error, got nil", kind)
			}
			var ae *apierr.APIError
			if !errors.As(err, &ae) {
				t.Fatalf("Append(%q): want *apierr.APIError, got %T (%v)", kind, err, err)
			}
			if ae.Spec == nil || ae.Spec.Code != apierrors.InternalEventbusJudgeKindOutsideApplier.Code {
				t.Fatalf("Append(%q): want code %q, got %#v", kind, apierrors.InternalEventbusJudgeKindOutsideApplier.Code, ae.Spec)
			}
		})
	}
}

// TestAppendAllowsNonJudgeKinds verifies the guard is narrow: every
// non-judge kind still gets through to the INSERT path. We use a
// representative sample (task.created, signal.attached, calendar.event.created)
// so a future regression that accidentally widens the judge set is
// caught.
//
// The DBTX is nil here too, so the test asserts only that the guard
// does NOT fire — the subsequent INSERT will panic, which we recover
// and treat as "guard passed".
func TestAppendAllowsNonJudgeKinds(t *testing.T) {
	t.Parallel()

	cases := []Kind{
		TaskCreated,
		SignalAttached, // explicitly NOT in the judge-only set
		CalEventCreated,
		AiAgentRunStarted,
	}

	for _, kind := range cases {
		kind := kind
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			defer func() {
				// Recover the expected panic from the nil pool behind
				// the boundary. The point of the test is that we got
				// PAST the guard; the subsequent INSERT failure mode is
				// not what we are exercising here.
				_ = recover()
			}()
			err := Append(context.Background(), dbretry.AutoCommit(nil), Event{
				Type:        kind,
				WorkspaceID: 1,
			})
			// We do NOT assert err is nil — the nil pool will explode
			// somewhere inside the INSERT path. The assertion we
			// actually care about is the negative one: if the guard
			// had fired we would have returned a JudgeKindOutsideApplier
			// error before reaching the INSERT, which would suppress
			// the panic and reach this line with err set.
			if err != nil {
				var ae *apierr.APIError
				if errors.As(err, &ae) && ae.Spec != nil && ae.Spec.Code == apierrors.InternalEventbusJudgeKindOutsideApplier.Code {
					t.Fatalf("Append(%q): guard fired for non-judge kind (%v)", kind, ae)
				}
			}
		})
	}
}

// TestAppendJudgeEventBypassesGuard verifies that the dedicated
// AppendJudgeEvent entry point lets the judge-only kinds through.
// This is the Applier's path — without it the Applier itself could
// not emit its own events.
func TestAppendJudgeEventBypassesGuard(t *testing.T) {
	t.Parallel()

	defer func() {
		// As in TestAppendAllowsNonJudgeKinds, the nil pool will
		// panic past the guard. Recover and pass the test.
		_ = recover()
	}()
	err := AppendJudgeEvent(context.Background(), dbretry.AutoCommit(nil), Event{
		Type:        TaskAutoCompleted,
		WorkspaceID: 1,
	})
	if err != nil {
		var ae *apierr.APIError
		if errors.As(err, &ae) && ae.Spec != nil && ae.Spec.Code == apierrors.InternalEventbusJudgeKindOutsideApplier.Code {
			t.Fatalf("AppendJudgeEvent: guard rejected the Applier's own path (%v)", ae)
		}
	}
}

// TestIsJudgeEventKind locks in the closed set of kinds the guard
// protects. Adding to / removing from judgeEventKinds without
// updating this test will be loud.
func TestIsJudgeEventKind(t *testing.T) {
	t.Parallel()

	in := []Kind{
		TaskAutoCompleted,
		TaskRetroDrafted,
		SignalJudged,
		SignalApplied,
		SignalRejected,
	}
	for _, k := range in {
		if !IsJudgeEventKind(k) {
			t.Fatalf("IsJudgeEventKind(%q) = false, want true", k)
		}
	}
	out := []Kind{
		TaskCreated,
		SignalAttached,
		AiAgentRunStarted,
		// The guard must also say no to a kind that is not declared at
		// all, which no constant can express.
		kindscan.Undeclared("completely.unknown"),
	}
	for _, k := range out {
		if IsJudgeEventKind(k) {
			t.Fatalf("IsJudgeEventKind(%q) = true, want false", k)
		}
	}
}
