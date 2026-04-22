package stream

import (
	"context"
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
