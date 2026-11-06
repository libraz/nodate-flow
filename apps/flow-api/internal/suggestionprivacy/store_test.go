package suggestionprivacy

import (
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

func TestNormalizeAcceptsOnlyCanonicalTaskReferences(t *testing.T) {
	first, second := types.New(), types.New()
	got, err := normalize([]Ref{{Kind: ResourceTask, PublicID: first}, {Kind: ResourceTask, PublicID: first}, {Kind: ResourceTask, PublicID: second}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].PublicID != first || got[1].PublicID != second {
		t.Fatalf("normalized refs = %+v, want stable unique task ids", got)
	}
	for _, invalid := range []Ref{{Kind: "", PublicID: first}, {Kind: "signal", PublicID: first}, {Kind: ResourceTask}} {
		if _, err := normalize([]Ref{invalid}); err == nil {
			t.Errorf("normalize(%+v) accepted incomplete provenance", invalid)
		}
	}
}

func TestTimelinePredicateBindsBothBatchOwnerAndEventActor(t *testing.T) {
	predicate, args := TimelinePredicate(17)
	if !strings.Contains(predicate, "suggestion_event.owner_user_id = ?") || !strings.Contains(predicate, "suggestion_event.actor_user_id = ?") {
		t.Fatalf("timeline predicate does not check both actor identities: %s", predicate)
	}
	if len(args) != 2 || args[0] != uint32(17) || args[1] != uint32(17) {
		t.Fatalf("timeline predicate args = %#v, want the caller twice", args)
	}
}
