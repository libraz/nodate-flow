package router

import (
	"os"
	"strings"
	"testing"
)

// TestDBInvocationLoggerRecordsTheProviderReportedCost pins that the
// ai_invocations writer persists the cost the provider reported and does
// not re-derive one from the model name.
//
// It used to do the opposite: any record arriving with a zero cost was
// re-priced from its token counts. Since a zero cost only ever reaches
// the writer alongside nonzero tokens when the provider deliberately
// reported one — local inference on hardware the operator already owns —
// and since local model names are absent from the price table, the
// re-pricing fell through to the table's deliberately conservative
// highest rate. Free inference was billed against the workspace's daily
// budget at claude-3-opus prices.
//
// Unpriced models still get that conservative rate; it is applied inside
// the provider, which is the layer that knows whether it charged
// anything at all.
func TestDBInvocationLoggerRecordsTheProviderReportedCost(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	src := string(b)
	if strings.Contains(src, "providers.EstimateCostMicrosUSD") {
		t.Fatalf("the ai_invocations writer must persist the provider's own cost, not re-estimate one from the model name: " +
			"a provider reporting zero is reporting that the call was free, and re-estimating can only overwrite that")
	}
	if strings.Contains(src, "if rec.CostCents > 0 {\n\t\t\tcost =") {
		t.Fatalf("DB invocation logger must not gate persistence only on whole-cent CostCents")
	}
}
