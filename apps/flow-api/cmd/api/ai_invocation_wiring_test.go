package main

import (
	"os"
	"strings"
	"testing"
)

func TestAutonomousAIRunnersWireInvocationLogger(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		"invocationLogger := router.NewDBInvocationLogger(queries, aiInvocationPublisher)",
		"Log:          invocationLogger",
		"Log: func(ctx context.Context, rec signaljudge.InvocationRecord)",
		"invocationLogger(ctx, ai.InvocationRecord",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("autonomous AI wiring must include %q", want)
		}
	}
}
