package slack

// NormalizeEventKind maps a Slack envelope to a stable canonical kind
// for `signals.kind`. Slack ships either a top-level
// `type` or a nested `event.type`; we prefer the inner one when
// present and fall back to the outer.
func NormalizeEventKind(outerType, innerType string) string {
	if innerType != "" {
		return innerType
	}
	if outerType != "" {
		return outerType
	}
	return "unknown"
}

// KnownSlackKinds enumerates the kinds we currently understand.
var KnownSlackKinds = []string{
	"message",
	"app_mention",
	"reaction_added",
	"reaction_removed",
	"channel_created",
	"member_joined_channel",
}
