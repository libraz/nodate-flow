package router

import (
	"os"
	"strings"
	"testing"
)

func TestDBInvocationLoggerPersistsMicroCostEstimate(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, "providers.EstimateCostMicrosUSD") {
		t.Fatalf("DB invocation logger must compute micro-USD cost from token usage")
	}
	if strings.Contains(src, "if rec.CostCents > 0 {\n\t\t\tcost =") {
		t.Fatalf("DB invocation logger must not gate persistence only on whole-cent CostCents")
	}
}
