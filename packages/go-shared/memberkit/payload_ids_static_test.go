package memberkit_test

import (
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/payloadscan"
)

// TestNoInternalIDsInMemberEvents is the go-shared half of the payload
// scan. Membership events name the user they are about, and this package
// holds only internal ids: the value written under userId is exactly the
// kind of field the rule exists for.
//
// See the flow-api counterpart for why the check resolves types instead
// of matching source text.
func TestNoInternalIDsInMemberEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping type-checking scan in -short mode")
	}

	findings, err := payloadscan.Scan(payloadscan.Config{Dir: "."})
	if err != nil {
		t.Fatalf("scan memberkit: %v", err)
	}
	for _, f := range findings {
		t.Errorf("event payload field %q is %s, not a string — resolve the user to its public_id before building the payload (%s)",
			f.Key, f.Type, f.Pos)
	}
}
