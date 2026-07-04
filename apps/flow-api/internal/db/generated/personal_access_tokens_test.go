package generated

import (
	"strings"
	"testing"
)

func TestFindPatByHashRequiresEnabledUser(t *testing.T) {
	t.Parallel()

	if !strings.Contains(findPatByHash, "INNER JOIN users") {
		t.Fatalf("FindPatByHash must join users to enforce account enabled state:\n%s", findPatByHash)
	}
	if !strings.Contains(findPatByHash, "u.enabled = TRUE") {
		t.Fatalf("FindPatByHash must reject PATs owned by disabled users:\n%s", findPatByHash)
	}
}
