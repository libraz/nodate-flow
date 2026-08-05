package webhook

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// deliveryProbe records which rows were delivered and when, so a test
// can tell "ran while the slow one was blocked" from "ran after it".
type deliveryProbe struct {
	mu       sync.Mutex
	finished []uint32
	inFlight int32
	peak     int32
}

func (p *deliveryProbe) enter() {
	n := atomic.AddInt32(&p.inFlight, 1)
	p.mu.Lock()
	if n > p.peak {
		p.peak = n
	}
	p.mu.Unlock()
}

func (p *deliveryProbe) leave(id uint32) {
	atomic.AddInt32(&p.inFlight, -1)
	p.mu.Lock()
	p.finished = append(p.finished, id)
	p.mu.Unlock()
}

func (p *deliveryProbe) snapshot() ([]uint32, int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]uint32(nil), p.finished...), p.peak
}

// row builds a claimed delivery row for the fake deliverer.
func row(id, subscriptionID uint32) generated.ClaimPendingDeliveriesRow {
	return generated.ClaimPendingDeliveriesRow{
		ID:             id,
		PublicID:       types.New(),
		WorkspaceID:    1,
		SubscriptionID: subscriptionID,
		EventType:      "task.created",
		PayloadJson:    json.RawMessage(`{}`),
	}
}

// TestSlowSubscriberDoesNotBlockOthers is the isolation contract. The
// endpoints belong to other people: one of them being slow is normal
// and cannot be prevented from here, so the only question is whether it
// costs everyone else their deliveries.
//
// Delivering the batch in claim order made it cost exactly that — a
// subscriber sitting on the client timeout held every row behind it,
// for as long as it liked, with nothing in the log to say why the queue
// was draining slowly.
func TestSlowSubscriberDoesNotBlockOthers(t *testing.T) {
	probe := &deliveryProbe{}
	release := make(chan struct{})

	w := &Worker{}
	w.deliverFn = func(_ context.Context, r generated.ClaimPendingDeliveriesRow) {
		probe.enter()
		defer probe.leave(r.ID)
		if r.SubscriptionID == 1 {
			<-release // the endpoint that never answers in time
		}
	}

	// One slow subscription with three queued rows, two healthy ones.
	rows := []generated.ClaimPendingDeliveriesRow{
		row(10, 1), row(11, 1), row(12, 1),
		row(20, 2),
		row(30, 3),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.deliverBatch(context.Background(), rows)
	}()

	// The healthy subscriptions must finish while the slow one is still
	// blocked. Without isolation none of them can, because row 10 is
	// first in the batch.
	waitFor(t, func() bool {
		finished, _ := probe.snapshot()
		return containsAll(finished, 20, 30)
	}, "deliveries for other subscriptions must complete while one endpoint is blocked")

	finished, _ := probe.snapshot()
	if containsAny(finished, 11, 12) {
		t.Fatal("the slow subscription's later rows must wait for its earlier ones, not overtake them")
	}

	close(release)
	<-done

	finished, peak := probe.snapshot()
	if len(finished) != len(rows) {
		t.Fatalf("every row must be delivered eventually, got %d of %d", len(finished), len(rows))
	}
	if peak > deliveryConcurrency {
		t.Fatalf("concurrency must stay bounded, peaked at %d (limit %d)", peak, deliveryConcurrency)
	}
}

// TestBatchKeepsPerSubscriptionOrder pins the other half: rows for one
// subscription stay in the order they were claimed. Receivers apply
// events in the order they arrive, so parallelising within a
// subscription would reorder them for no benefit.
func TestBatchKeepsPerSubscriptionOrder(t *testing.T) {
	var mu sync.Mutex
	var seen []uint32

	w := &Worker{}
	w.deliverFn = func(_ context.Context, r generated.ClaimPendingDeliveriesRow) {
		mu.Lock()
		seen = append(seen, r.ID)
		mu.Unlock()
		time.Sleep(time.Millisecond)
	}

	w.deliverBatch(context.Background(), []generated.ClaimPendingDeliveriesRow{
		row(1, 7), row(2, 7), row(3, 7),
	})

	mu.Lock()
	defer mu.Unlock()
	want := []uint32{1, 2, 3}
	if len(seen) != len(want) {
		t.Fatalf("got %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("per-subscription order must be preserved: got %v, want %v", seen, want)
		}
	}
}

// TestBatchStopsOnShutdown checks that a cancelled context stops the
// batch instead of firing the remaining requests on the way out. Rows
// left claimed are picked up by the stranded reaper.
func TestBatchStopsOnShutdown(t *testing.T) {
	var delivered atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())

	w := &Worker{}
	w.deliverFn = func(_ context.Context, _ generated.ClaimPendingDeliveriesRow) {
		delivered.Add(1)
		cancel()
		time.Sleep(2 * time.Millisecond)
	}

	w.deliverBatch(ctx, []generated.ClaimPendingDeliveriesRow{
		row(1, 5), row(2, 5), row(3, 5), row(4, 5),
	})

	if got := delivered.Load(); got != 1 {
		t.Fatalf("a cancelled batch must stop after the row in flight, delivered %d", got)
	}
}

func containsAll(haystack []uint32, needles ...uint32) bool {
	for _, n := range needles {
		found := false
		for _, h := range haystack {
			if h == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func containsAny(haystack []uint32, needles ...uint32) bool {
	for _, n := range needles {
		for _, h := range haystack {
			if h == n {
				return true
			}
		}
	}
	return false
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(time.Millisecond)
	}
}
