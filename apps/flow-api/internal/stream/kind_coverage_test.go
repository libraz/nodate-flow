package stream

import (
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// TestStreamKindCoversEveryFamily proves the stream's routing table
// answers for every event family that exists.
//
// The table it replaced was a switch that fell through to "not
// published". A family added afterwards therefore reached the tap,
// matched no case, and was dropped — and because "no case" and "chosen
// not to publish" produced the same silence, nothing could tell the two
// apart. Requiring an entry per family makes the choice explicit; a
// family may still map to "" (not published), it just cannot be absent.
func TestStreamKindCoversEveryFamily(t *testing.T) {
	t.Parallel()

	for _, family := range eventbus.Families() {
		if _, ok := streamKindForFamily[family]; !ok {
			t.Errorf("event family %q has no entry in streamKindForFamily; map it to a stream kind, "+
				"or to \"\" if it is deliberately not published on the wire", family)
		}
	}
}

// TestStreamKindTableHasNoStaleFamilies proves the routing table names
// no family that has ceased to exist. A stale entry is a rename that
// landed on one side only, and it reads as coverage while the kinds it
// was written for fall somewhere else.
func TestStreamKindTableHasNoStaleFamilies(t *testing.T) {
	t.Parallel()

	known := map[eventbus.Family]bool{}
	for _, family := range eventbus.Families() {
		known[family] = true
	}
	for family := range streamKindForFamily {
		if !known[family] {
			t.Errorf("streamKindForFamily names %q, which is not a declared event family; drop the stale entry", family)
		}
	}
}

// TestEveryDeclaredKindResolvesThroughTheTable proves each declared
// event kind reaches a decision — a stream kind or a deliberate silence
// — rather than falling off the end of the classification.
func TestEveryDeclaredKindResolvesThroughTheTable(t *testing.T) {
	t.Parallel()

	for _, kind := range eventbus.Kinds() {
		family, ok := eventbus.FamilyOf(kind)
		if !ok {
			t.Errorf("event kind %q belongs to no family, so the stream cannot classify it", kind)
			continue
		}
		if _, ok := streamKindForFamily[family]; !ok {
			t.Errorf("event kind %q is in family %q, which streamKindForFamily does not cover", kind, family)
		}
	}
}

// TestPublishedStreamKindsAreDeclared proves the table only ever routes
// to a stream kind the wire format defines. A typo would publish a kind
// the frontend's switch does not handle, and the browser does not ignore
// that: the switch returns undefined, the SSE reader throws where it
// iterates the result, and the reconnect loop discards the throw. Every
// such event costs the connection along with any frame buffered behind
// it, while the connection stays healthy enough that the polling
// fallback never engages.
func TestPublishedStreamKindsAreDeclared(t *testing.T) {
	t.Parallel()

	declared := map[Kind]bool{
		KindTaskChanged:         true,
		KindAiSuggestionChanged: true,
		KindAiInvocationWritten: true,
		KindNotificationChanged: true,
		KindTimeboxChanged:      true,
		KindRelationChanged:     true,
		KindLensChanged:         true,
		KindPageChanged:         true,
		KindDashboardChanged:    true,
		KindCalendarChanged:     true,
		KindItemChanged:         true,
		KindResync:              true,
	}
	for family, kind := range streamKindForFamily {
		if kind == "" {
			continue
		}
		if !declared[kind] {
			t.Errorf("family %q routes to stream kind %q, which the wire format does not define", family, kind)
		}
	}
}

// TestStreamKindFollowsTheCoreRegistry proves a kind in the core registry
// reaches the stream kind its registry family maps to, including the
// registered kinds that flow-api never emits, and that a product-layer
// kind outside the registry still routes by its namespace.
func TestStreamKindFollowsTheCoreRegistry(t *testing.T) {
	t.Parallel()

	for _, name := range eventbus.CoreKindNames() {
		info, _ := eventbus.CoreKindInfo(eventbus.Kind(name))
		want := streamKindForFamily[info.Family]
		got, ok := KindForEventType(name)
		if want == "" {
			if ok {
				t.Errorf("%s: published as %q, but its family %q is not published", name, got, info.Family)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%s: KindForEventType = %q, %v; want %q", name, got, ok, want)
		}
	}
	if got, ok := KindForEventType("calendar.photo.created"); !ok || got != KindCalendarChanged {
		t.Errorf("calendar.photo.created: KindForEventType = %q, %v; want the calendar kind by namespace", got, ok)
	}
}
