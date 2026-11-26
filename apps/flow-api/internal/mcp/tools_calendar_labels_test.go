package mcp

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarlabels"
)

// The schema bound is a literal the column-bound check can read; the rule
// that enforces the name length is calendarlabels.MaxNameRunes.
func TestCalendarLabelNameLimitMatchesRules(t *testing.T) {
	t.Parallel()

	if calendarLabelNameLimit != calendarlabels.MaxNameRunes {
		t.Fatalf("calendarLabelNameLimit is %d but calendarlabels.MaxNameRunes is %d; the schema advertises a bound the rule does not enforce",
			calendarLabelNameLimit, calendarlabels.MaxNameRunes)
	}
}
