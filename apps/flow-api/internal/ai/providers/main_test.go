package providers

import (
	"os"
	"testing"
)

// TestMain enables the private-destination escape hatch
// (NF_FLOW_AI_ALLOW_PRIVATE) for this package's tests. Almost every
// provider test drives a loopback httptest server, which is exactly what
// the SSRF guard refuses to dial by default.
//
// The guard's own tests clear the variable with t.Setenv and therefore do
// not call t.Parallel: the sequential phase of a Go test binary runs while
// every parallel test is still parked, so no parallel test observes the
// strict setting.
func TestMain(m *testing.M) {
	if err := os.Setenv(AllowPrivateEnv, "1"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
