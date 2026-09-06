// Tests for the Fanout goroutine lifecycle. The work function (f.run)
// is overridden so the goroutine plumbing — detached cancellation,
// per-event timeout, and Shutdown drain — can be exercised without a
// live MySQL connection.
package notification

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/obs"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// TestHook_DetachesFromParentContext verifies that cancelling the
// parent context passed to the hook does not abort the spawned
// fan-out goroutine. The detached child should observe context.Canceled
// only on its own deadline, never propagated from the parent.
func TestHook_DetachesFromParentContext(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)
	f.SetTimeout(2 * time.Second)

	done := make(chan struct{})
	var observedErr atomic.Value // stores error
	f.run = func(ctx context.Context, _ uint32, _ string, _ uint64) {
		defer close(done)
		// Wait long enough that, if cancellation propagated from the
		// parent, ctx would be Done immediately.
		select {
		case <-ctx.Done():
			observedErr.Store(ctx.Err())
		case <-time.After(200 * time.Millisecond):
			// Healthy path: no cancellation, work completes.
		}
	}

	parent, cancel := context.WithCancel(context.Background())
	hook := f.Hook()
	hook(parent, 1, "task.created", 0)
	// Cancel the parent immediately; the fan-out goroutine must
	// continue running to completion.
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fanout goroutine did not run after parent context was cancelled")
	}

	if v := observedErr.Load(); v != nil {
		t.Fatalf("fanout context was cancelled even though parent cancellation should be detached: %v", v)
	}

	// Drain to keep things tidy.
	if err := f.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned unexpected error: %v", err)
	}
}

// TestHook_TimeoutAborts verifies that work exceeding the configured
// timeout sees ctx.Err() == context.DeadlineExceeded.
func TestHook_TimeoutAborts(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)
	f.SetTimeout(50 * time.Millisecond)

	done := make(chan struct{})
	var observedErr atomic.Value
	f.run = func(ctx context.Context, _ uint32, _ string, _ uint64) {
		defer close(done)
		// Block until the timeout fires.
		select {
		case <-ctx.Done():
			observedErr.Store(ctx.Err())
		case <-time.After(2 * time.Second):
			t.Error("timeout did not fire within 2s budget")
		}
	}

	hook := f.Hook()
	hook(context.Background(), 1, "task.created", 0)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fanout goroutine did not finish")
	}

	gotV := observedErr.Load()
	if gotV == nil {
		t.Fatal("expected DeadlineExceeded but ctx.Done was never observed")
	}
	got, _ := gotV.(error)
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", got)
	}

	if err := f.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned unexpected error: %v", err)
	}
}

// TestShutdown_WaitsForInFlight verifies that Shutdown blocks until
// every in-flight goroutine has returned, and that hooks fired after
// Shutdown was initiated are dropped.
func TestShutdown_WaitsForInFlight(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)
	f.SetTimeout(5 * time.Second)

	release := make(chan struct{})
	started := make(chan struct{})
	var finished atomic.Int32
	var startedOnce atomic.Bool
	f.run = func(_ context.Context, _ uint32, _ string, _ uint64) {
		if startedOnce.CompareAndSwap(false, true) {
			close(started)
		}
		<-release
		finished.Add(1)
	}

	hook := f.Hook()
	hook(context.Background(), 1, "task.created", 0)

	// Wait for the goroutine to actually start so we know Shutdown
	// has something to wait on.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fanout goroutine did not start")
	}

	// Shutdown should block until release is closed.
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- f.Shutdown(context.Background())
	}()

	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before in-flight work finished (err=%v)", err)
	case <-time.After(100 * time.Millisecond):
	}

	// New hooks fired after Shutdown started must be dropped.
	hook(context.Background(), 1, "task.created", 0)
	if got := finished.Load(); got != 0 {
		t.Fatalf("expected 0 finished goroutines before release, got %d", got)
	}

	// Release the in-flight goroutine and confirm Shutdown returns.
	close(release)

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return after in-flight goroutine finished")
	}

	if got := finished.Load(); got != 1 {
		t.Fatalf("expected exactly 1 finished goroutine (post-shutdown hook should be dropped), got %d", got)
	}
}

// TestShutdown_ContextDeadline verifies that Shutdown returns the
// supplied context's error when the wait budget is exhausted before
// in-flight work finishes.
func TestShutdown_ContextDeadline(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)
	f.SetTimeout(5 * time.Second)

	release := make(chan struct{})
	defer close(release)

	started := make(chan struct{})
	var startedOnce atomic.Bool
	f.run = func(_ context.Context, _ uint32, _ string, _ uint64) {
		if startedOnce.CompareAndSwap(false, true) {
			close(started)
		}
		<-release
	}

	hook := f.Hook()
	hook(context.Background(), 1, "task.created", 0)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fanout goroutine did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := f.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

// TestLoadPreferencesWithRetry_TransientErrorRecovers verifies that a
// single transient failure on the first attempt is masked by the
// retry path: the second attempt's rows are returned and no
// preference-fetch error counter increment is observed.
func TestLoadPreferencesWithRetry_TransientErrorRecovers(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)

	var calls atomic.Int32
	wantRow := generated.GetNotificationPreferencesForRecipientsRow{
		UserID:  42,
		Channel: generated.NotificationPreferencesChannelEmail,
	}
	f.fetchPreferences = func(_ context.Context, _ generated.GetNotificationPreferencesForRecipientsParams) ([]generated.GetNotificationPreferencesForRecipientsRow, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("transient: connection reset by peer")
		}
		return []generated.GetNotificationPreferencesForRecipientsRow{wantRow}, nil
	}

	rows, err := f.loadPreferencesWithRetry(context.Background(), generated.GetNotificationPreferencesForRecipientsParams{
		WorkspaceID:   1,
		EventCategory: "task.lifecycle",
		UserIds:       []uint32{42},
	})
	if err != nil {
		t.Fatalf("expected nil error after retry success, got %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected exactly 2 fetch attempts (initial + retry), got %d", got)
	}
	if len(rows) != 1 || rows[0] != wantRow {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

// TestLoadPreferencesWithRetry_PersistentErrorPropagates verifies that
// when both the initial attempt and the retry fail, the error is
// returned to the caller (which then bumps the error counter and
// falls back to the in_app default in [Fanout.fanout]).
func TestLoadPreferencesWithRetry_PersistentErrorPropagates(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)

	var calls atomic.Int32
	dbErr := errors.New("driver: bad connection")
	f.fetchPreferences = func(_ context.Context, _ generated.GetNotificationPreferencesForRecipientsParams) ([]generated.GetNotificationPreferencesForRecipientsRow, error) {
		calls.Add(1)
		return nil, dbErr
	}

	_, err := f.loadPreferencesWithRetry(context.Background(), generated.GetNotificationPreferencesForRecipientsParams{
		WorkspaceID:   1,
		EventCategory: "task.lifecycle",
		UserIds:       []uint32{42},
	})
	if err == nil {
		t.Fatal("expected error after persistent failure, got nil")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected exactly 2 fetch attempts (initial + retry), got %d", got)
	}
}

// TestLoadPreferencesWithRetry_ContextCancelDuringBackoff verifies
// that a deadline firing during the inter-attempt backoff aborts
// the retry promptly rather than burning the rest of the goroutine
// timeout sleeping.
func TestLoadPreferencesWithRetry_ContextCancelDuringBackoff(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)

	var calls atomic.Int32
	f.fetchPreferences = func(_ context.Context, _ generated.GetNotificationPreferencesForRecipientsParams) ([]generated.GetNotificationPreferencesForRecipientsRow, error) {
		calls.Add(1)
		return nil, errors.New("transient")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := f.loadPreferencesWithRetry(ctx, generated.GetNotificationPreferencesForRecipientsParams{
		WorkspaceID:   1,
		EventCategory: "task.lifecycle",
		UserIds:       []uint32{42},
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from ctx cancel, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected only the first attempt before backoff cancel, got %d", got)
	}
	// The backoff window is 50ms; the deadline should fire well before.
	if elapsed >= preferenceFetchRetryDelay {
		t.Fatalf("backoff was not interrupted by ctx (elapsed=%v >= %v)", elapsed, preferenceFetchRetryDelay)
	}
}

// TestFanoutMetrics_PreferenceFetchErrorTypeLabel verifies that a
// generic DB error increments the counter under type="db" while a
// timeout / cancellation increments it under type="timeout". The
// counters are package-global so this test does not run in parallel
// with itself, but it can run alongside other notification tests
// because the assertions are deltas off the pre-call baseline.
func TestFanoutMetrics_PreferenceFetchErrorTypeLabel(t *testing.T) {
	dbCounter := obs.NotificationFanoutPreferenceFetchErrorsCounter("db")
	timeoutCounter := obs.NotificationFanoutPreferenceFetchErrorsCounter("timeout")

	dbBefore := testutil.ToFloat64(dbCounter)
	timeoutBefore := testutil.ToFloat64(timeoutCounter)

	obs.IncNotificationFanoutPreferenceFetchError(errors.New("driver: bad connection"))
	obs.IncNotificationFanoutPreferenceFetchError(context.DeadlineExceeded)
	obs.IncNotificationFanoutPreferenceFetchError(context.Canceled)

	if got, want := testutil.ToFloat64(dbCounter)-dbBefore, 1.0; got != want {
		t.Fatalf("type=db delta: got %v want %v", got, want)
	}
	if got, want := testutil.ToFloat64(timeoutCounter)-timeoutBefore, 2.0; got != want {
		t.Fatalf("type=timeout delta: got %v want %v", got, want)
	}
}

// TestFanout_SilentKindAnnouncesNothing covers the cheapest way a pass
// notifies nobody: the classifications table answers [silent] and the
// pass ends before it reads anything. Most declared kinds land here, so
// announcing on them would put the bulk of the event stream on the wire
// as invalidations for a bell that never changed.
//
// The nil database is part of the assertion. A silent kind must be
// settled from the table alone, so reaching any query panics here rather
// than costing a round trip per event in production.
func TestFanout_SilentKindAnnouncesNothing(t *testing.T) {
	t.Parallel()

	kind := eventbus.ReactionAdded
	if got := classifications[kind]; got != silent {
		t.Fatalf("%s is classified %+v; this test needs a kind that notifies nobody", kind, got)
	}

	var announced atomic.Int64
	f := NewFanout(nil, nil, nil)
	f.SetNotificationPublisher(func(context.Context, uint32) { announced.Add(1) })

	f.fanout(context.Background(), 1, string(kind), 1)

	if got := announced.Load(); got != 0 {
		t.Fatalf("announced %d times for a kind that notifies nobody, want 0", got)
	}
}

// TestClassifyEvent_AgentTaskEvents verifies that the agent.task.*
// event family produces the expected (title, resource, severity)
// tuples and routes through categoryForEventType to the "ai"
// notification preference bucket. agent.task.thought is suppressed
// (empty title) because it represents private agent reasoning that
// must not generate user notifications.
func TestClassifyEvent_AgentTaskEvents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		eventType    string
		wantTitle    string
		wantResource string
		wantSeverity generated.NotificationsSeverity
		wantCategory string
	}{
		{
			eventType:    "agent.task.handoff_to_user",
			wantTitle:    "An agent handed back to you",
			wantResource: "task",
			wantSeverity: generated.NotificationsSeverityHigh,
			wantCategory: "ai",
		},
		{
			eventType:    "agent.task.handoff_to_agent",
			wantTitle:    "A task was handed off to an agent",
			wantResource: "task",
			wantSeverity: generated.NotificationsSeverityNormal,
			wantCategory: "ai",
		},
		{
			eventType:    "agent.task.attached",
			wantTitle:    "An agent was attached to a task",
			wantResource: "task",
			wantSeverity: generated.NotificationsSeverityLow,
			wantCategory: "ai",
		},
		{
			eventType:    "agent.task.detached",
			wantTitle:    "An agent was detached from a task",
			wantResource: "task",
			wantSeverity: generated.NotificationsSeverityLow,
			wantCategory: "ai",
		},
		{
			eventType:    "agent.task.thought",
			wantTitle:    "",
			wantResource: "",
			wantSeverity: generated.NotificationsSeverity(""),
			wantCategory: "ai",
		},
	}

	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			t.Parallel()
			gotTitle, gotResource, gotSeverity := classifyEvent(tc.eventType)
			if gotTitle != tc.wantTitle {
				t.Errorf("title: got %q, want %q", gotTitle, tc.wantTitle)
			}
			if gotResource != tc.wantResource {
				t.Errorf("resource: got %q, want %q", gotResource, tc.wantResource)
			}
			if gotSeverity != tc.wantSeverity {
				t.Errorf("severity: got %q, want %q", gotSeverity, tc.wantSeverity)
			}
			if got := categoryForEventType(tc.eventType); got != tc.wantCategory {
				t.Errorf("category: got %q, want %q", got, tc.wantCategory)
			}
		})
	}
}

// TestMentionRecipients narrows a mention to the people its payload names.
//
// The two cases are the whole contract of the intersection. A named user
// who is in the recipient set is the only one delivered to, which is what
// keeps a mention off everyone else's bell; a named user who is absent from
// it is delivered nothing, because the set the visibility rule produced is
// the one being narrowed and a mention cannot add to it. Nobody is the
// correct answer in the second case, not an error.
//
// The resolution is stubbed at the same seam production reads it from, so
// the test states what the workspace-scoped lookup returned rather than
// re-deciding membership itself.
func TestMentionRecipients(t *testing.T) {
	t.Parallel()

	visible := types.New()
	invisible := types.New()
	const (
		visibleUserID   uint32 = 7
		invisibleUserID uint32 = 9
	)
	recipients := []uint32{4, visibleUserID, 12}

	byPublicID := map[types.PublicID]uint32{
		visible:   visibleUserID,
		invisible: invisibleUserID,
	}

	cases := []struct {
		name    string
		payload string
		want    []uint32
	}{
		{
			name:    "named user is in the recipient set",
			payload: `{"taskId":"t","mentionedUserIds":["` + visible.String() + `"]}`,
			want:    []uint32{visibleUserID},
		},
		{
			name:    "named user is outside the recipient set",
			payload: `{"taskId":"t","mentionedUserIds":["` + invisible.String() + `"]}`,
			want:    nil,
		},
		{
			name:    "one of each",
			payload: `{"taskId":"t","mentionedUserIds":["` + invisible.String() + `","` + visible.String() + `"]}`,
			want:    []uint32{visibleUserID},
		},
		{
			name:    "payload names nobody",
			payload: `{"taskId":"t"}`,
			want:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := NewFanout(nil, nil, nil)
			f.resolveMentionedUsers = func(_ context.Context, params generated.FindWorkspaceMemberUserInternalIdsByPublicIdsParams) ([]generated.FindWorkspaceMemberUserInternalIdsByPublicIdsRow, error) {
				var rows []generated.FindWorkspaceMemberUserInternalIdsByPublicIdsRow
				for _, id := range params.PublicIds {
					if internal, ok := byPublicID[id]; ok {
						rows = append(rows, generated.FindWorkspaceMemberUserInternalIdsByPublicIdsRow{
							ID:       internal,
							PublicID: id,
						})
					}
				}
				return rows, nil
			}

			got, err := f.mentionRecipients(context.Background(), 1, recipients, json.RawMessage(tc.payload))
			if err != nil {
				t.Fatalf("mentionRecipients returned error: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("recipients: got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMentionRecipients_ResolutionFailurePropagates keeps a failed lookup
// from reading as "nobody was mentioned": the caller has to be able to tell
// the two apart, because falling through with the unnarrowed set would send
// the mention to everyone who can see the task.
func TestMentionRecipients_ResolutionFailurePropagates(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)
	f.resolveMentionedUsers = func(_ context.Context, _ generated.FindWorkspaceMemberUserInternalIdsByPublicIdsParams) ([]generated.FindWorkspaceMemberUserInternalIdsByPublicIdsRow, error) {
		return nil, errors.New("driver: bad connection")
	}

	payload := json.RawMessage(`{"mentionedUserIds":["` + types.New().String() + `"]}`)
	got, err := f.mentionRecipients(context.Background(), 1, []uint32{1, 2}, payload)
	if err == nil {
		t.Fatal("expected the resolution error to propagate, got nil")
	}
	if got != nil {
		t.Fatalf("expected no recipients alongside the error, got %v", got)
	}
}

// TestFanoutMetrics_DedupCounter verifies that IncNotificationFanoutDedup
// bumps the (reason) labelled counter exactly once per call. The
// fan-out path under [Fanout.fanout] calls this on every INSERT IGNORE
// row that hit the (recipient, source_event, channel) UNIQUE key.
func TestFanoutMetrics_DedupCounter(t *testing.T) {
	uniqueCounter := obs.NotificationFanoutDedupCounter("unique_collision")
	before := testutil.ToFloat64(uniqueCounter)

	obs.IncNotificationFanoutDedup("unique_collision")
	obs.IncNotificationFanoutDedup("unique_collision")
	obs.IncNotificationFanoutDedup("unique_collision")

	if got, want := testutil.ToFloat64(uniqueCounter)-before, 3.0; got != want {
		t.Fatalf("unique_collision delta: got %v want %v", got, want)
	}
}
