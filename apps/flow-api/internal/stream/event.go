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

import "github.com/libraz/nodate-flow/packages/go-shared/eventbus"

// Kind is the closed set of event families the stream emits. Adding
// a new kind requires an ADR amendment (ADR 0005 §Wire format).
type Kind string

// Known kinds. Keep this list in sync with
// apps/flow-web/src/features/realtime/event-to-keys.ts and with the ADR
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

	// KindNotificationChanged fires when a fan-out pass writes at least
	// one row into the notifications table for the workspace. The
	// frontend invalidates the notification list and unread count.
	//
	// Both queries are scoped to the reader on the server, so the
	// members a pass wrote no row for refetch and see nothing new. That
	// is the cost of a workspace-addressed wire format, and it is the
	// reason a pass that writes nothing at all publishes nothing.
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

	// KindCalendarChanged fires on any `calendar.*` or `share.*` append
	// for the workspace. The frontend invalidates calendar lists,
	// event lists, member/memo queries, and public-share management
	// views for this workspace.
	KindCalendarChanged Kind = "calendar.changed"

	// KindItemChanged fires on any `item.*` append from itemkit
	// (the unified task+event facade). The frontend invalidates both
	// task and calendar-event caches because an item change touches
	// both sides of the link in one transaction.
	KindItemChanged Kind = "item.changed"

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

// streamKindForFamily maps every event family onto the stream [Kind]
// subscribers see for it. A family mapped to "" is deliberately not
// published: the wire format is a closed set (ADR 0005 §Wire format), so
// carrying a family onto the stream is an ADR amendment and a frontend
// change, not a silent addition here.
//
// The table is total over [eventbus.Families]. That is the point of it:
// the prefix switch it replaced answered "not published" for anything it
// did not list, so a family added later reached this function, matched
// no case, and vanished — indistinguishable from a family somebody chose
// not to publish. A missing entry now fails
// TestStreamKindCoversEveryFamily instead, and the choice has to be
// written down either way.
var streamKindForFamily = map[eventbus.Family]Kind{
	eventbus.FamilyTask:         KindTaskChanged,
	eventbus.FamilyAiSuggestion: KindAiSuggestionChanged,

	// An auto-action proposal changes nothing about the task except what
	// its activity feed shows, and that feed is what KindTaskChanged
	// invalidates. It is not an ai.suggestion: those name rows in
	// ai_suggestions, and a subscriber told to re-read that list would
	// find nothing new.
	eventbus.FamilyAiAutoAction: KindTaskChanged,

	eventbus.FamilyTimebox:   KindTimeboxChanged,
	eventbus.FamilyRelation:  KindRelationChanged,
	eventbus.FamilyLens:      KindLensChanged,
	eventbus.FamilyPage:      KindPageChanged,
	eventbus.FamilyDashboard: KindDashboardChanged,
	eventbus.FamilyItem:      KindItemChanged,

	// Calendar and public-share appends share one stream kind because the
	// frontend invalidates calendars, events and public-share views
	// together off it.
	eventbus.FamilyCalendar:    KindCalendarChanged,
	eventbus.FamilyPublicShare: KindCalendarChanged,

	// Not published. Each of these is an audit-trail or back-office
	// family with no live query to invalidate, or one whose stream kind
	// the frontend does not yet accept.
	eventbus.FamilyLabel:           "",
	eventbus.FamilyAgentTask:       "",
	eventbus.FamilyAiAgent:         "",
	eventbus.FamilySignal:          "",
	eventbus.FamilyExport:          "",
	eventbus.FamilyReaction:        "",
	eventbus.FamilyMention:         "",
	eventbus.FamilyFavorite:        "",
	eventbus.FamilyIntake:          "",
	eventbus.FamilyDescription:     "",
	eventbus.FamilyImport:          "",
	eventbus.FamilyWorkspaceMember: "",
	eventbus.FamilyComment:         "",
}

// KindForEventType maps a dotted `eventbus.Kind` string (e.g.
// "task.transition.complete") to the stream [Kind] the frontend
// should see. Returns ("", false) when the event does not belong to
// any published family.
//
// [KindNotificationChanged] and [KindAiInvocationWritten] have no entry
// above because no row in `events` carries them. Each names a write to a
// table of its own, so the tap exposes a dedicated entry point per kind
// and the writer calls it: [EventbusTap.PublishNotification] from the
// notification fan-out, [EventbusTap.PublishAiInvocation] from the
// invocation logger. Mapping either one off an event family instead
// would fire on the append that triggered the write rather than on the
// write, which is the case where nothing was written at all.
func KindForEventType(eventType string) (Kind, bool) {
	family, ok := eventbus.FamilyForEventType(eventType)
	if !ok {
		return "", false
	}
	kind, published := streamKindForFamily[family]
	if !published || kind == "" {
		return "", false
	}
	return kind, true
}
