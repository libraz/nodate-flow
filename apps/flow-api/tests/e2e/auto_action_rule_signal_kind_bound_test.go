package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// autoActionSignalKindMaxLen is the width of
// auto_action_rules.signal_kind. Stated here rather than read from the
// DTO so loosening the DTO shows up as a failure rather than as a test
// that quietly follows it.
const autoActionSignalKindMaxLen = 64

// TestAutoActionRuleSignalKindIsBounded holds the bound on the rule's
// scope address. The value is written through to the column unchanged,
// so without the bound an overlong scope is refused by the insert and
// reported as a server error rather than as a validation error naming
// the field.
func TestAutoActionRuleSignalKindIsBounded(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	overlong := strings.Repeat("s", autoActionSignalKindMaxLen+1)

	status, raw := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/auto-action-rules",
		tt.AccessToken, map[string]any{
			"rules": []map[string]any{
				{"kind": "escalate_overdue", "signalKind": overlong, "enabled": true},
			},
		})
	body := string(raw)

	require.Equalf(t, http.StatusUnprocessableEntity, status,
		"an overlong signalKind must be refused by validation, not by the insert; body=%s", body)
	require.Containsf(t, body, "signalKind",
		"the refusal does not name the field that was rejected: %s", body)
}

// TestAutoActionRuleSignalKindAtTheBoundIsAccepted is the counterweight:
// the bound must refuse one character too many and nothing less.
func TestAutoActionRuleSignalKindAtTheBoundIsAccepted(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	atBound := strings.Repeat("s", autoActionSignalKindMaxLen)

	status, raw := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/auto-action-rules",
		tt.AccessToken, map[string]any{
			"rules": []map[string]any{
				{"kind": "escalate_overdue", "signalKind": atBound, "enabled": true},
			},
		})
	require.GreaterOrEqualf(t, status, 200, "body=%s", string(raw))
	require.Lessf(t, status, 300,
		"a signalKind exactly at the column width must be accepted; body=%s", string(raw))
}
