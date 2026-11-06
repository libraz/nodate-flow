package ai

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// TestInboxTriageOutputRejectsDuplicateItems pins one proposal per captured
// inbox item: a second entry for the same item, in any spelling, rejects the
// whole batch instead of producing two proposals.
func TestInboxTriageOutputRejectsDuplicateItems(t *testing.T) {
	t.Parallel()

	itemID := types.New().String()
	otherID := types.New().String()
	versions := map[string]string{itemID: "v1", otherID: "v2"}
	entry := func(id, action string) string {
		return fmt.Sprintf(`{"inboxItemId":%q,"score":0.5,"recommendedAction":%q,"reasoning":"r"}`, id, action)
	}
	cases := []struct {
		name    string
		items   []string
		wantErr bool
	}{
		{"distinct items", []string{entry(itemID, "archive"), entry(otherID, "snooze")}, false},
		{"same item twice", []string{entry(itemID, "archive"), entry(itemID, "snooze")}, true},
		{"same item in another spelling", []string{entry(itemID, "archive"), entry(strings.ToUpper(itemID), "archive")}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := &providers.Response{Text: "[" + strings.Join(tc.items, ",") + "]", Termination: providers.TerminationComplete}
			suggestions, err := validateInboxTriageOutput(resp, versions, nil)
			if !tc.wantErr {
				if err != nil || len(suggestions) != len(tc.items) {
					t.Fatalf("suggestions = %v, err = %v; want %d accepted", suggestions, err, len(tc.items))
				}
				return
			}
			if !errors.Is(err, providers.ErrResponseSchemaMismatch) {
				t.Fatalf("err = %v, want ErrResponseSchemaMismatch", err)
			}
			if suggestions != nil {
				t.Fatalf("suggestions = %v, want none from a rejected batch", suggestions)
			}
		})
	}
}
