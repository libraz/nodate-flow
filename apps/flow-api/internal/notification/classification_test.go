package notification

import (
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// TestEveryKindIsClassified proves the notification table answers for
// every event kind the system declares.
//
// The switch it replaced ended in a default that returned an empty
// title, which fan-out reads as "notify nobody". A kind added later —
// or renamed, which is the same thing to a table keyed by string —
// therefore delivered zero notifications and looked exactly like a kind
// somebody had decided was not worth one. Requiring an entry per kind
// forces the decision to be written down; `silent` is a perfectly good
// answer, an absent entry is not.
func TestEveryKindIsClassified(t *testing.T) {
	t.Parallel()

	for _, kind := range eventbus.Kinds() {
		if _, ok := classifications[kind]; !ok {
			t.Errorf("event kind %q has no entry in classifications; give it a title, or mark it silent "+
				"with the reason it notifies nobody", kind)
		}
	}
}

// TestClassificationsHaveNoStaleKinds proves the table names no kind
// that has ceased to exist. A stale row survives a rename and reads as
// coverage for a kind nothing emits, while the kind that replaced it
// notifies nobody.
func TestClassificationsHaveNoStaleKinds(t *testing.T) {
	t.Parallel()

	declared := map[eventbus.Kind]bool{}
	for _, kind := range eventbus.Kinds() {
		declared[kind] = true
	}
	for kind := range classifications {
		if !declared[kind] {
			t.Errorf("classifications names %q, which no event kind constant declares; drop the stale entry", kind)
		}
	}
}

// TestNotifyingKindsCarryResourceAndSeverity proves a kind that does
// notify carries the two fields the row needs alongside the title.
// A title with no resource type writes a notification the frontend
// cannot link anywhere, which renders as an entry that does nothing when
// clicked.
func TestNotifyingKindsCarryResourceAndSeverity(t *testing.T) {
	t.Parallel()

	notifying := 0
	for kind, c := range classifications {
		if c.Title == "" {
			if c.ResourceType != "" || c.Severity != "" {
				t.Errorf("%q has no title but sets resource/severity; a row is never written, so the fields mislead", kind)
			}
			continue
		}
		notifying++
		if c.ResourceType == "" {
			t.Errorf("%q notifies but names no resource type; the notification would link nowhere", kind)
		}
		if c.Severity == "" {
			t.Errorf("%q notifies but has no severity", kind)
		}
	}
	if notifying == 0 {
		t.Fatal("no kind notifies anyone; the guard is passing because it is looking at nothing")
	}
}

// TestClassifyEventReadsTheTable pins the lookup itself, including the
// runtime-minted transition kinds that are not constants and therefore
// have no entry to find.
func TestClassifyEventReadsTheTable(t *testing.T) {
	t.Parallel()

	title, resource, severity := classifyEvent(string(eventbus.TaskCommentAdded))
	if title == "" || resource != "comment" || severity == "" {
		t.Errorf("task.comment.added classified as (%q, %q, %q); want a comment notification", title, resource, severity)
	}

	if title, _, _ := classifyEvent(string(eventbus.AgentTaskThought)); title != "" {
		t.Errorf("agent.task.thought is private agent reasoning; got title %q", title)
	}

	// A transition name the product invented at runtime. It has no copy,
	// and asking for it must not panic or invent one.
	if title, _, _ := classifyEvent(string(eventbus.TaskTransition("bespoke"))); title != "" {
		t.Errorf("a runtime-minted transition kind has no copy; got title %q", title)
	}
}
