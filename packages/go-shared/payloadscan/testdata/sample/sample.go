// Package sample is scanned by the payloadscan tests. It stands in for a
// production package that appends events: the shapes here are the ones
// the scan has to tell apart.
package sample

// Event mirrors the append signature the scan looks for.
type Event struct {
	Type         string
	Payload      any
	ExtraPayload map[string]any
}

// PublicID stands in for the UUID wrapper the real code carries.
type PublicID struct{}

func (PublicID) String() string { return "" }

// CleanInline names identifiers with strings, which is the rule.
func CleanInline(pub PublicID, name string) Event {
	return Event{
		Type: "task.created",
		Payload: map[string]any{
			"taskId": pub.String(),
			"title":  name,
			"count":  3,
		},
	}
}

// CleanPassThrough is the false positive a source scan produces: the
// value is already a public id string, just not spelled with .String().
func CleanPassThrough(taskID string) Event {
	return Event{
		Payload: map[string]any{"taskId": taskID},
	}
}

// LeakInline writes an internal key straight into the literal.
func LeakInline(internal uint32) Event {
	return Event{
		Payload: map[string]any{"userId": internal},
	}
}

// LeakViaLocal hoists the map into a variable first.
func LeakViaLocal(internal uint32) Event {
	payload := map[string]any{"eventId": internal}
	return Event{Payload: payload}
}

// LeakViaAny hides the type behind an interface variable.
func LeakViaAny(internal uint32) Event {
	var hidden any = internal
	return Event{Payload: map[string]any{"labelId": hidden}}
}

// LeakInExtra covers the second payload field.
func LeakInExtra(internal int64) Event {
	return Event{ExtraPayload: map[string]any{"agentId": internal}}
}

// CommentedOutFix is the shape that defeats a source scan: the correct
// call is present as text but not as code.
func CommentedOutFix(internal uint32, pub PublicID) Event {
	// return Event{Payload: map[string]any{"taskId": pub.String()}}
	_ = pub
	return Event{Payload: map[string]any{"taskId": internal}}
}

// UnrelatedMap has an id-shaped key outside any payload field.
func UnrelatedMap(internal uint32) map[string]any {
	return map[string]any{"taskId": internal}
}
