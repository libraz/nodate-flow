package notification

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification/prefs"
)

// TestEveryRoutedCategoryIsConfigurable pins the pairing between the
// category fan-out routes an event to and the categories the
// preferences screen offers. A category returned here but missing from
// [prefs.Categories] would deliver notifications nobody could mute —
// the failure mode the preference endpoint exists to remove.
//
// The event list is every type [classifyEvent] produces a title for,
// plus the unknown type that exercises the default bucket.
func TestEveryRoutedCategoryIsConfigurable(t *testing.T) {
	t.Parallel()

	eventTypes := []string{
		"task.created", "task.updated", "task.disabled",
		"task.comment.added", "task.comment.edited", "task.comment.removed",
		"task.actor.added", "task.actor.removed",
		"task.transition.start", "task.transition.complete",
		"task.transition.block", "task.transition.unblock",
		"task.transition.submit", "task.transition.reopen",
		"task.transition.cancel",
		"item.scheduled", "item.unscheduled", "item.rescheduled",
		"item.renamed", "item.deleted", "item.reconciled",
		"item.actor.added", "item.actor.removed",
		"item.visibility.changed",
		"item.milestone.link.added", "item.milestone.link.removed",
		"agent.task.handoff_to_user", "agent.task.handoff_to_agent",
		"agent.task.attached", "agent.task.detached", "agent.task.thought",
		"some.event.type.added.later",
	}

	for _, eventType := range eventTypes {
		category := categoryForEventType(eventType)
		if !prefs.ValidCategory(category) {
			t.Errorf("event %q routes to category %q, which is not configurable", eventType, category)
		}
	}
}
