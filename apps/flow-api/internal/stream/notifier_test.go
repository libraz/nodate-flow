package stream

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestInProcessNotifier_PublishDelivers(t *testing.T) {
	t.Parallel()
	n := NewInProcessNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := n.Subscribe(ctx, "ws-1")
	go n.Publish(ctx, Event{Kind: KindTaskChanged, WorkspaceID: "ws-1", At: 1})

	select {
	case evt := <-ch:
		if evt.Kind != KindTaskChanged || evt.WorkspaceID != "ws-1" {
			t.Fatalf("unexpected event: %+v", evt)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestInProcessNotifier_IsolatesWorkspaces(t *testing.T) {
	t.Parallel()
	n := NewInProcessNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chA := n.Subscribe(ctx, "ws-a")
	chB := n.Subscribe(ctx, "ws-b")

	n.Publish(ctx, Event{Kind: KindTaskChanged, WorkspaceID: "ws-a", At: 1})

	select {
	case evt := <-chA:
		if evt.WorkspaceID != "ws-a" {
			t.Fatalf("expected ws-a, got %+v", evt)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("chA got nothing")
	}

	select {
	case evt := <-chB:
		t.Fatalf("chB should not have received anything, got %+v", evt)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestInProcessNotifier_SlowSubscriberGetsResync(t *testing.T) {
	t.Parallel()
	n := NewInProcessNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := n.Subscribe(ctx, "ws-slow")

	// Fill the inbox past capacity without draining.
	for i := 0; i < subscriberInboxSize+5; i++ {
		n.Publish(ctx, Event{Kind: KindTaskChanged, WorkspaceID: "ws-slow", At: int64(i)})
	}

	// Drain everything and assert a resync marker appears somewhere.
	var sawResync bool
	timeout := time.After(200 * time.Millisecond)
drain:
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				break drain
			}
			if evt.Kind == KindResync {
				sawResync = true
			}
		case <-timeout:
			break drain
		}
	}
	if !sawResync {
		t.Fatal("expected a resync marker after overflow, got none")
	}
}

// TestInProcessNotifier_ResyncRearmsAfterDrain covers the recovery
// half of the drop policy. A subscriber that falls behind once must
// resume receiving normal events after it catches up, and must get a
// fresh resync marker the next time it falls behind. A resync flag
// that is only ever set silences the subscriber for the rest of its
// session: the SSE connection stays healthy on heartbeats, so the UI
// looks fine and simply stops updating until the user reloads.
func TestInProcessNotifier_ResyncRearmsAfterDrain(t *testing.T) {
	t.Parallel()
	n := NewInProcessNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := n.Subscribe(ctx, "ws-rearm")

	overflow := func() {
		for i := 0; i < subscriberInboxSize+5; i++ {
			n.Publish(ctx, Event{Kind: KindTaskChanged, WorkspaceID: "ws-rearm", At: int64(i)})
		}
	}
	// Publish is synchronous on this goroutine, so everything the
	// subscriber will ever get is already buffered by the time we
	// drain: an empty inbox means done, no timeout needed.
	drain := func() bool {
		sawResync := false
		for {
			select {
			case evt := <-ch:
				if evt.Kind == KindResync {
					sawResync = true
				}
			default:
				return sawResync
			}
		}
	}

	overflow()
	if !drain() {
		t.Fatal("first overflow: expected a resync marker, got none")
	}

	// Caught up: normal delivery must resume.
	n.Publish(ctx, Event{Kind: KindTaskChanged, WorkspaceID: "ws-rearm", At: 100})
	select {
	case evt := <-ch:
		if evt.Kind != KindTaskChanged {
			t.Fatalf("expected a normal event after catching up, got %+v", evt)
		}
	default:
		t.Fatal("subscriber stayed silent after draining the resync marker")
	}

	overflow()
	if !drain() {
		t.Fatal("second overflow: expected another resync marker, got none")
	}
}

func TestInProcessNotifier_UnsubscribeOnCancel(t *testing.T) {
	t.Parallel()
	n := NewInProcessNotifier()
	ctx, cancel := context.WithCancel(context.Background())

	_ = n.Subscribe(ctx, "ws-x")
	if got := n.ActiveSubscribers("ws-x"); got != 1 {
		t.Fatalf("expected 1 subscriber, got %d", got)
	}
	cancel()

	// Wait for the cleanup goroutine to run.
	for i := 0; i < 20; i++ {
		if n.ActiveSubscribers("ws-x") == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subscriber not cleaned up after context cancel")
}

// TestInProcessNotifier_PublishRacesUnsubscribe hammers the window
// between the registry snapshot Publish takes under RLock and the
// lock-free sends it performs afterwards, while subscriptions are torn
// down concurrently. Signalling teardown by closing the inbox channel
// makes this panic with "send on closed channel" and takes the whole
// process down, so the subscriber uses a separate done channel.
func TestInProcessNotifier_PublishRacesUnsubscribe(t *testing.T) {
	t.Parallel()
	const (
		rounds          = 40
		subsPerRound    = 32
		publishers      = 8
		eventsPerPubber = 32
	)

	n := NewInProcessNotifier()
	for r := 0; r < rounds; r++ {
		cancels := make([]context.CancelFunc, 0, subsPerRound)
		var drained sync.WaitGroup
		for i := 0; i < subsPerRound; i++ {
			// Cancelling here would end the subscription before the
			// race this test exists to reproduce. Every cancel is
			// collected and invoked below, concurrently with Publish,
			// which is the teardown ordering that used to panic.
			ctx, cancel := context.WithCancel(context.Background()) //#nosec G118 -- cancel is invoked from the cancels slice below
			ch := n.Subscribe(ctx, "ws-race")
			cancels = append(cancels, cancel)
			// Keep every inbox drained so Publish keeps taking the
			// real send path instead of short-circuiting on the
			// overflow/resync branch.
			drained.Add(1)
			go func() {
				defer drained.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ch:
					}
				}
			}()
		}

		var wg sync.WaitGroup
		start := make(chan struct{})
		for p := 0; p < publishers; p++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := 0; i < eventsPerPubber; i++ {
					n.Publish(context.Background(), Event{
						Kind:        KindTaskChanged,
						WorkspaceID: "ws-race",
						At:          int64(i),
					})
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for _, cancel := range cancels {
				cancel()
			}
		}()
		close(start)
		wg.Wait()
		drained.Wait()
	}

	for i := 0; i < 100; i++ {
		if n.ActiveSubscribers("ws-race") == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subscribers not cleaned up: %d remain", n.ActiveSubscribers("ws-race"))
}

// TestInProcessNotifier_PublishAfterUnsubscribeIsDropped pins the
// teardown contract the SSE handler relies on: once the subscription
// context is cancelled the inbox stops receiving, and Publish stays a
// no-op for that subscriber instead of panicking or blocking.
func TestInProcessNotifier_PublishAfterUnsubscribeIsDropped(t *testing.T) {
	t.Parallel()
	n := NewInProcessNotifier()
	ctx, cancel := context.WithCancel(context.Background())

	ch := n.Subscribe(ctx, "ws-gone")
	cancel()

	var cleaned bool
	for i := 0; i < 100; i++ {
		if n.ActiveSubscribers("ws-gone") == 0 {
			cleaned = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cleaned {
		t.Fatal("subscriber not cleaned up after context cancel")
	}

	n.Publish(context.Background(), Event{Kind: KindTaskChanged, WorkspaceID: "ws-gone", At: 1})

	select {
	case evt, ok := <-ch:
		t.Fatalf("expected no delivery after unsubscribe, got %+v (open=%v)", evt, ok)
	case <-time.After(50 * time.Millisecond):
		// expected: no event, and the channel stays open so a racing
		// Publish can never send on a closed channel.
	}
}

func TestKindForEventType(t *testing.T) {
	t.Parallel()
	cases := map[string]Kind{
		"task.created":             KindTaskChanged,
		"task.transition.complete": KindTaskChanged,
		"ai.suggestion.applied":    KindAiSuggestionChanged,
		"calendar.event.created":   KindCalendarChanged,
		"calendar.member.joined":   KindCalendarChanged,
		"share.token.rotated":      KindCalendarChanged,
		"item.scheduled":           KindItemChanged,
		"item.rescheduled":         KindItemChanged,
		"signal.attached":          "",
		"comment.added":            "",
	}
	for in, want := range cases {
		got, ok := KindForEventType(in)
		if want == "" {
			if ok {
				t.Fatalf("%q: expected no kind, got %q", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("%q: expected %q, got %q (ok=%v)", in, want, got, ok)
		}
	}
}
