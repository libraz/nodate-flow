package embed

import (
	"os"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// TestMain enables the private-destination escape hatch for this package's
// tests. Embedding requests travel on the shared providers transport, whose
// dialer refuses loopback by default — which is exactly where the httptest
// servers these tests drive are listening.
func TestMain(m *testing.M) {
	if err := os.Setenv(providers.AllowPrivateEnv, "1"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
