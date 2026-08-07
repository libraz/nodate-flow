package webhook

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// TestPayloadIdentifiesTheResource is the usability contract. A body of
// {eventType, workspaceId, occurredAt} tells a receiver that something
// changed and nothing else: not which event, not which row, and no way
// to fetch either. Every id below is what makes the delivery actionable.
func TestPayloadIdentifiesTheResource(t *testing.T) {
	t.Parallel()

	var (
		deliveryID = types.New()
		eventID    = types.New()
		wsID       = types.New()
		taskID     = types.New()
	)
	occurredAt := time.Unix(1700000000, 0).UTC()

	raw := buildPayload(deliveryID, eventID, "task.created", occurredAt, eventContext{
		workspacePublicID: wsID,
		taskPublicID:      taskID,
	})

	var got webhookPayload
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}

	if got.EventID != eventID.String() {
		t.Errorf("eventId = %q, want %q", got.EventID, eventID.String())
	}
	if got.DeliveryID != deliveryID.String() {
		t.Errorf("deliveryId = %q, want %q", got.DeliveryID, deliveryID.String())
	}
	if got.WorkspaceID != wsID.String() {
		t.Errorf("workspaceId = %q, want %q", got.WorkspaceID, wsID.String())
	}
	if got.EventType != "task.created" {
		t.Errorf("eventType = %q, want %q", got.EventType, "task.created")
	}
	if got.OccurredAt != occurredAt.Unix() {
		t.Errorf("occurredAt = %d, want %d", got.OccurredAt, occurredAt.Unix())
	}
	if len(got.Resources) != 1 {
		t.Fatalf("resources = %v, want exactly the task", got.Resources)
	}
	if got.Resources[0].Type != "task" || got.Resources[0].ID != taskID.String() {
		t.Errorf("resources[0] = %+v, want {task %s}", got.Resources[0], taskID.String())
	}
}

// TestPayloadCarriesEveryTargetedResource covers the scheduled item: it
// is a task and it lives in a calendar, and a receiver subscribed to one
// of the two should not have to guess the other.
func TestPayloadCarriesEveryTargetedResource(t *testing.T) {
	t.Parallel()

	taskID, calID := types.New(), types.New()
	raw := buildPayload(types.New(), types.New(), "item.scheduled", time.Unix(1700000000, 0).UTC(), eventContext{
		workspacePublicID: types.New(),
		taskPublicID:      taskID,
		calendarPublicID:  calID,
	})

	var got webhookPayload
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	want := []webhookResource{
		{Type: "task", ID: taskID.String()},
		{Type: "calendar", ID: calID.String()},
	}
	if len(got.Resources) != len(want) {
		t.Fatalf("resources = %+v, want %+v", got.Resources, want)
	}
	for i := range want {
		if got.Resources[i] != want[i] {
			t.Errorf("resources[%d] = %+v, want %+v", i, got.Resources[i], want[i])
		}
	}
}

// TestPayloadOmitsResourcesTheEventDoesNotTarget keeps the absent case
// honest: an event with no task and no calendar must not claim one under
// the zero UUID, which reads as a real id to a receiver.
func TestPayloadOmitsResourcesTheEventDoesNotTarget(t *testing.T) {
	t.Parallel()

	raw := buildPayload(types.New(), types.New(), "workspace.member.added",
		time.Unix(1700000000, 0).UTC(), eventContext{workspacePublicID: types.New()})

	var got webhookPayload
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if len(got.Resources) != 0 {
		t.Fatalf("resources = %+v, want none", got.Resources)
	}
	if body := string(raw); strings.Contains(body, "00000000-0000-0000-0000-000000000000") {
		t.Fatalf("payload advertises the zero UUID as a real id: %s", body)
	}
}

// TestPayloadDoesNotCarryTheEventPayload pins the decision not to
// forward the source event's payload_json. A webhook target is an
// arbitrary third-party URL, and the event log has carried a share token
// in plaintext before; adding a passthrough field here would make the
// next producer mistake an external disclosure.
func TestPayloadDoesNotCarryTheEventPayload(t *testing.T) {
	t.Parallel()

	raw := buildPayload(types.New(), types.New(), "lens.shared",
		time.Unix(1700000000, 0).UTC(), eventContext{workspacePublicID: types.New()})

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	allowed := map[string]bool{
		"eventId": true, "eventType": true, "deliveryId": true,
		"workspaceId": true, "occurredAt": true, "resources": true,
	}
	for key := range fields {
		if !allowed[key] {
			t.Errorf("payload field %q is not part of the identifier-only contract; "+
				"anything forwarded from the event log reaches a third-party URL unfiltered", key)
		}
	}
}
