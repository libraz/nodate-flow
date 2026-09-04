package eventbus

import (
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	sharedbus "github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/eventlog"
)

// The counters live for the life of the process and are keyed by
// workspace id, so each test here claims ids nothing else in the package
// appends to. Sharing an id with another test would make the expected
// dense sequence depend on test order.
const (
	seqIsolationWorkspaceA uint32 = 900001
	seqIsolationWorkspaceB uint32 = 900002
	seqBothAppendersWS     uint32 = 900003
	seqConcurrentWS        uint32 = 900004
)

// seqRecorder collects the sequence number each dispatch carried,
// grouped by workspace. It is safe to use from several goroutines
// because hooks run on whichever goroutine appended.
type seqRecorder struct {
	mu  sync.Mutex
	got map[uint32][]int64
}

func newSeqRecorder(t *testing.T) *seqRecorder {
	t.Helper()
	r := &seqRecorder{got: map[uint32][]int64{}}
	handle := AddNotifyHook(func(ctx context.Context, ws uint32, _ string, _ uint64) {
		seq := SeqFromContext(ctx)
		r.mu.Lock()
		r.got[ws] = append(r.got[ws], seq)
		r.mu.Unlock()
	})
	t.Cleanup(func() { RemoveNotifyHook(handle) })
	return r
}

func (r *seqRecorder) seqs(ws uint32) []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.got[ws]...)
}

// TestSeqIsPerWorkspace pins the property a subscriber depends on: the
// numbers it sees count its own workspace's events and nothing else. A
// process-wide counter passes an ordering check just as well, and fails
// here — workspace B's second event would be numbered after everything
// workspace A did in between.
func TestSeqIsPerWorkspace(t *testing.T) {
	rec := newSeqRecorder(t)

	fire := func(ws uint32) {
		fireNotifyHooks(context.Background(), ws, string(TaskCreated), 1)
	}
	fire(seqIsolationWorkspaceA)
	fire(seqIsolationWorkspaceB)
	fire(seqIsolationWorkspaceA)
	fire(seqIsolationWorkspaceB)
	fire(seqIsolationWorkspaceA)

	wantA := []int64{1, 2, 3}
	wantB := []int64{1, 2}
	if got := rec.seqs(seqIsolationWorkspaceA); !equalSeqs(got, wantA) {
		t.Fatalf("workspace A saw %v, want %v: a subscriber must not see another tenant's events as gaps", got, wantA)
	}
	if got := rec.seqs(seqIsolationWorkspaceB); !equalSeqs(got, wantB) {
		t.Fatalf("workspace B saw %v, want %v: a subscriber must not see another tenant's events as gaps", got, wantB)
	}
}

// TestSeqSpansBothAppenders pins that the sequence covers every event a
// subscriber is woken for, not only the ones flow-api appended itself.
// The cross-service kits write through eventlog and reach the same
// subscribers via the bridge; an unnumbered event there is delivered as
// seq 0, which reads as a stream that restarted.
func TestSeqSpansBothAppenders(t *testing.T) {
	db := stubDB(t)
	eventlog.ClearHooks()
	t.Cleanup(eventlog.ClearHooks)
	resetBridge(t)

	rec := newSeqRecorder(t)
	BridgeEventlog()

	ctx := context.Background()
	if err := Append(ctx, dbretry.AutoCommit(db), Event{Type: TaskCreated, WorkspaceID: seqBothAppendersWS}); err != nil {
		t.Fatalf("eventbus append: %v", err)
	}
	if _, err := eventlog.Append(ctx, dbretry.AutoCommit(db), eventlog.Event{
		Type:        sharedbus.ItemScheduled,
		WorkspaceID: seqBothAppendersWS,
	}); err != nil {
		t.Fatalf("eventlog append: %v", err)
	}

	got := rec.seqs(seqBothAppendersWS)
	if len(got) != 2 {
		t.Fatalf("both appenders must reach subscribers, got %d dispatches: %v", len(got), got)
	}
	if got[1] == 0 {
		t.Fatal("the bridged append was delivered with seq 0: events forwarded from the shared eventlog must be numbered too")
	}
	if want := []int64{1, 2}; !equalSeqs(got, want) {
		t.Fatalf("the two appenders produced %v, want %v: one sequence must cover both", got, want)
	}
}

// TestSeqUnderConcurrentAppends pins that concurrent events of one
// workspace consume the sequence exactly once each. Handlers append from
// independent request goroutines, so a counter that lost a race would
// hand two subscribers the same number and the client would drop one as
// a duplicate.
func TestSeqUnderConcurrentAppends(t *testing.T) {
	const workers = 64

	rec := newSeqRecorder(t)

	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			fireNotifyHooks(context.Background(), seqConcurrentWS, string(TaskCreated), 1)
		}()
	}
	wg.Wait()

	got := rec.seqs(seqConcurrentWS)
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	want := make([]int64, workers)
	for i := range want {
		want[i] = int64(i + 1)
	}
	if !equalSeqs(got, want) {
		t.Fatalf("%d concurrent events produced %v, want a dense 1..%d with no duplicate", workers, got, workers)
	}
}

func equalSeqs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
