package testhelpers

import (
	"os"
	"testing"
)

// SkipUnlessIntegration is the single gate every Docker-backed test in
// the repository goes through, so the whole suite answers "should this
// run?" the same way.
//
// Two conditions skip: `go test -short`, and an unset
// NF_TEST_INTEGRATION. Both mean the caller asked for the fast,
// Docker-free run, and a test that needs a database has nothing to say
// about it.
//
// What the gate deliberately does not do is absorb a missing Docker
// daemon. Once NF_TEST_INTEGRATION is set the caller has asked for the
// integration suites, and a container that will not start is a failure
// to report, not a reason to quietly pass. Call sites keep that half of
// the contract by failing on the container startup error rather than
// skipping on it.
func SkipUnlessIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run integration tests (Docker required)")
	}
}
