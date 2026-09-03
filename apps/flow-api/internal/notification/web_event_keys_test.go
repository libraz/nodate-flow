package notification

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// webEventKeyPath is the map the web client renders a notification from,
// relative to this package's directory.
//
// The client cannot render a title of its own from a row: the server
// stores a pre-rendered English one, and the client translates by looking
// the row's event type up in this map. An event type the map has no entry
// for falls back to the stored English string, in front of a reader whose
// product is otherwise Japanese or Chinese. Nothing on either side sees
// that on its own — the English text never passes through the
// translator, so the i18n guard has no key to miss, and the map is a
// plain data file, so the type checker has nothing to reject.
//
// The comparison runs here rather than in the web suite because this is
// where the set is decided. Reading Go source from TypeScript to find it
// out was the previous arrangement, and it broke the moment the
// classification switch became a table — a refactor that changed no
// behaviour turned the check green over nothing, while a renamed event
// type, which is the failure it exists for, would have gone through
// unremarked.
const webEventKeyPath = "../../../../apps/flow-web/src/features/notifications/event-keys.json"

// loadWebEventKeys returns the event type -> i18n key map the web client
// ships.
func loadWebEventKeys(t *testing.T) map[string]string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(webEventKeyPath))
	if err != nil {
		t.Fatalf("read %s: %v", webEventKeyPath, err)
	}
	var keys map[string]string
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("parse %s: %v", webEventKeyPath, err)
	}
	if len(keys) == 0 {
		t.Fatalf("%s is empty; the comparison below would pass over nothing", webEventKeyPath)
	}
	return keys
}

// notifyingKinds returns the event types fan-out writes a notification
// row for, sorted.
func notifyingKinds() []string {
	out := make([]string, 0, len(classifications))
	for kind, c := range classifications {
		if c.Title == "" {
			continue
		}
		out = append(out, string(kind))
	}
	slices.Sort(out)
	return out
}

// TestWebEventKeysCoverEveryNotifyingKind proves the client can translate
// every notification the fan-out writes.
func TestWebEventKeysCoverEveryNotifyingKind(t *testing.T) {
	t.Parallel()

	keys := loadWebEventKeys(t)
	notifying := notifyingKinds()
	if len(notifying) == 0 {
		t.Fatal("no kind notifies anyone; the comparison is passing because it is looking at nothing")
	}

	for _, kind := range notifying {
		if _, ok := keys[kind]; !ok {
			t.Errorf("%q notifies but %s has no entry for it; a reader would get the stored English title",
				kind, webEventKeyPath)
		}
	}
}

// TestWebEventKeysNameNoSilentKind proves the client carries no entry for
// an event type the server never notifies on. Such an entry survives a
// rename and reads as coverage, while the event type that replaced it
// renders in English.
func TestWebEventKeysNameNoSilentKind(t *testing.T) {
	t.Parallel()

	keys := loadWebEventKeys(t)
	notifying := notifyingKinds()

	for eventType := range keys {
		if slices.Contains(notifying, eventType) {
			continue
		}
		if _, declared := classifications[eventbus.Kind(eventType)]; declared {
			t.Errorf("%s maps %q, which is classified silent; the entry is unreachable copy",
				webEventKeyPath, eventType)
			continue
		}
		t.Errorf("%s maps %q, which no event kind declares; drop the stale entry",
			webEventKeyPath, eventType)
	}
}

// TestWebEventKeysUseTheEventNamespace pins the key shape the locale
// bundles are organised by, so a malformed entry fails here rather than
// as a raw key rendered in front of a reader.
func TestWebEventKeysUseTheEventNamespace(t *testing.T) {
	t.Parallel()

	for eventType, key := range loadWebEventKeys(t) {
		if !strings.HasPrefix(key, "event.") || key == "event." {
			t.Errorf("%q maps to %q; notification copy lives under the \"event.\" namespace", eventType, key)
		}
	}
}
