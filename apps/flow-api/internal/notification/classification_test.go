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

// TestClassificationLookupReadsTheTable pins the lookup itself, including
// the runtime-minted transition kinds that are not constants and therefore
// have no entry to find.
func TestClassificationLookupReadsTheTable(t *testing.T) {
	t.Parallel()

	// A comment notification links the task the comment is on: the event
	// row supplies the task's public id, so naming the row a comment would
	// pair a comment type with a task id.
	c := classifications[eventbus.TaskCommentAdded]
	if c.Title == "" || c.ResourceType != "task" || c.Severity == "" {
		t.Errorf("task.comment.added classified as (%q, %q, %q); want a task-linked notification", c.Title, c.ResourceType, c.Severity)
	}

	if c := classifications[eventbus.AgentTaskThought]; c.Title != "" {
		t.Errorf("agent.task.thought is private agent reasoning; got title %q", c.Title)
	}

	// A transition name the product invented at runtime. It has no copy,
	// and asking for it must not panic or invent one.
	if c := classifications[eventbus.TaskTransition("bespoke")]; c.Title != "" {
		t.Errorf("a runtime-minted transition kind has no copy; got title %q", c.Title)
	}
}

// TestEveryNotifyingResourceIsTheEventTask holds the table to the one
// public id the fan-out can attach: the event's task. A notifying kind
// naming any other resource type would pair that type with a task id.
func TestEveryNotifyingResourceIsTheEventTask(t *testing.T) {
	t.Parallel()

	for kind, c := range classifications {
		if c.Title != "" && c.ResourceType != "task" {
			t.Errorf("%q links resource type %q, but resource_public_id is always the event's task", kind, c.ResourceType)
		}
	}
}
