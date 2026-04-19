package constraint

import (
	"fmt"
	"strings"
)

// Explain returns a deterministic, i18n-key-style description of a
// constraint expression. It is NOT a localized string: the result is
// a compact pseudo-sentence built from stable tokens so the web
// layer can either render it directly (dev) or map tokens to
// i18n messages (prod).
//
// The output is deliberately pure — no clock reads, no map
// iteration order — so replay tools can diff "what this constraint
// meant at time T" against a newer version without flakes.
func Explain(c Constraint) string {
	switch c.Op {
	case OpAnd:
		parts := make([]string, 0, len(c.Terms))
		for _, t := range c.Terms {
			parts = append(parts, Explain(t))
		}
		return "(" + strings.Join(parts, " AND ") + ")"
	case OpOr:
		parts := make([]string, 0, len(c.Terms))
		for _, t := range c.Terms {
			parts = append(parts, Explain(t))
		}
		return "(" + strings.Join(parts, " OR ") + ")"
	case OpNot:
		return "NOT " + Explain(*c.Term)
	case OpTimeDueBefore:
		return fmt.Sprintf("due_before(%s)", c.Arg)
	case OpTimeDueAfter:
		return fmt.Sprintf("due_after(%s)", c.Arg)
	case OpDepAllDone:
		return fmt.Sprintf("all_done(%s)", strings.Join(c.TaskIDs, ","))
	case OpDepOpenAtMost:
		if c.Max == nil {
			return "open_at_most(?)"
		}
		return fmt.Sprintf("open_at_most(%d)", *c.Max)
	case OpActorHasRole:
		return fmt.Sprintf("actor_has_role(%s)", c.Arg)
	case OpSignalReceived:
		return fmt.Sprintf("signal_received(%s)", c.Arg)
	case OpApprovalGranted:
		return fmt.Sprintf("approval_granted(%s)", c.Arg)
	case OpCIStatusIs:
		return fmt.Sprintf("ci_status_is(%s)", c.Arg)
	}
	return "unknown"
}
