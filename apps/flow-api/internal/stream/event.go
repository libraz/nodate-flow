// Package stream implements the realtime SSE fan-out described in
// ADR 0005. It turns append-only `events` writes into
// invalidation-only Server-Sent Events that the web frontend uses to
// refresh its workspace-scoped queries without polling.
//
// The package exposes three pieces:
//
//   - [Event] + [Kind], the wire shape the SSE handler writes and the
//     frontend parses.
//   - [Notifier], the per-process pub/sub that [eventbus.Append] feeds
//     and the SSE handler subscribes to.
//   - [SSEHandler], a Huma-free http.Handler that upgrades the
//     request to an SSE stream for a single authenticated workspace.
//
// Per ADR 0005 the wire format carries *no payloads*. The server only
// says "something in this workspace changed, refetch whatever you
// cared about", and the frontend maps each [Kind] to a set of
// TanStack Query invalidation calls.
package stream

import "strings"

// Kind is the closed set of event families the stream emits. Adding
// a new kind requires an ADR amendment (ADR 0005 §Wire format).
type Kind string

// Known kinds. Keep this list in sync with
// apps/web/src/features/realtime/event-to-keys.ts and with the ADR
// 0005 §Wire format table.
const (
	// KindTaskChanged fires on any `task.*` append for the workspace.
	// The frontend invalidates auto-actions, reminders,
	// state-suggestions, weekly-digest, and any open task list or
	// task detail queries for this workspace.
	KindTaskChanged Kind = "task.changed"

	// KindAiSuggestionChanged fires on any `ai.suggestion.*` append
	// for the workspace. The frontend invalidates the ai-suggestions
	// list for this workspace.
	KindAiSuggestionChanged Kind = "ai.suggestion.changed"

	// KindAiInvocationWritten fires when a new row is inserted into
	// the ai_invocations table for the workspace. The frontend
	// invalidates the ai-invocations audit list.
	KindAiInvocationWritten Kind = "ai.invocation.written"

	// KindNotificationChanged fires on any notification-related event.
	// The frontend invalidates the notification list and unread count.
	KindNotificationChanged Kind = "notification.changed"

	// KindTimeboxChanged fires on any `timebox.*` append for the
	// workspace. The frontend invalidates timebox lists and detail.
	KindTimeboxChanged Kind = "timebox.changed"

	// KindRelationChanged fires on any `relation.*` append for the
	// workspace. The frontend invalidates relation suggestion lists.
	KindRelationChanged Kind = "relation.changed"

	// KindLensChanged fires on any `lens.*` append for the
	// workspace. The frontend invalidates lens lists.
	KindLensChanged Kind = "lens.changed"

	// KindPageChanged fires on any `page.*` append for the
	// workspace. The frontend invalidates page lists and detail.
	KindPageChanged Kind = "page.changed"

	// KindDashboardChanged fires on any `dashboard.*` append for the
	// workspace. The frontend invalidates dashboard widget lists.
	KindDashboardChanged Kind = "dashboard.changed"

	// KindCalendarChanged fires on any `calendar.*` append for the
	// workspace. The frontend invalidates calendar lists, event lists,
	// and member/memo queries for this workspace.
	KindCalendarChanged Kind = "calendar.changed"

	// KindResync is sent when the server has dropped events for a
	// slow subscriber. The frontend reacts by invalidating every
	// workspace-scoped query, which is the safe superset.
	KindResync Kind = "resync"
)

// Event is the wire shape written to the SSE stream. WorkspaceID is
// the *public* workspace id and is the only identifier on the wire;
// task / actor ids are deliberately omitted because the frontend
// invalidates at the workspace prefix anyway.
type Event struct {
	Kind        Kind   `json:"kind"`
	WorkspaceID string `json:"workspaceId"`
	At          int64  `json:"at"`
}

// KindForEventType maps a dotted `eventbus.Kind` string (e.g.
// "task.transition.complete") to the stream [Kind] the frontend
// should see. Returns ("", false) when the event does not belong to
// any published family.
func KindForEventType(eventType string) (Kind, bool) {
	switch {
	case strings.HasPrefix(eventType, "task."):
		return KindTaskChanged, true
	case strings.HasPrefix(eventType, "ai.suggestion."):
		return KindAiSuggestionChanged, true
	case strings.HasPrefix(eventType, "notification."):
		return KindNotificationChanged, true
	case strings.HasPrefix(eventType, "timebox."):
		return KindTimeboxChanged, true
	case strings.HasPrefix(eventType, "relation."):
		return KindRelationChanged, true
	case strings.HasPrefix(eventType, "lens."):
		return KindLensChanged, true
	case strings.HasPrefix(eventType, "page."):
		return KindPageChanged, true
	case strings.HasPrefix(eventType, "calendar."):
		return KindCalendarChanged, true
	case strings.HasPrefix(eventType, "dashboard."):
		return KindDashboardChanged, true
	}
	return "", false
}
