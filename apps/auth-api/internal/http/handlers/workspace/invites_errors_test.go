package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
)

// TestInviteErrorSpecs_DistinguishExpiredFromExhausted pins the
// invariant that the invite handler now reports two distinct,
// retry-actionable failure states (expired / exhausted) instead of
// collapsing both into the misleading WORKSPACE.NOT_FOUND. Without
// these distinct codes the UI cannot tell the user what to do — it
// just says "not found", which is a lie when the invite was real but
// past its lifetime or use cap.
func TestInviteErrorSpecs_DistinguishExpiredFromExhausted(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "WS.WORKSPACE_INVITE.EXPIRED",
		apierrors.WsWorkspaceInviteExpired.Code,
		"expired-invite path must surface its own code, not collapse to NOT_FOUND")
	assert.Equal(t, "WS.WORKSPACE_INVITE.EXHAUSTED",
		apierrors.WsWorkspaceInviteExhausted.Code,
		"exhausted-invite path must surface its own code, not collapse to NOT_FOUND")
	assert.NotEqual(t,
		apierrors.WsWorkspaceInviteExpired.Code,
		apierrors.WsWorkspaceInviteExhausted.Code,
		"expired and exhausted must remain distinguishable on the wire")
}

// TestInviteErrorSpecs_AreGoneNotNotFound asserts both invite-state
// specs map to HTTP 410 Gone, not 404. 410 is the right code for a
// resource that DID exist but has been definitively retired (used up
// or past its expiry); 404 lies about the resource ever having been
// real and pushes the user toward unhelpful "double-check the URL"
// debugging.
func TestInviteErrorSpecs_AreGoneNotNotFound(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 410, apierrors.WsWorkspaceInviteExpired.Status,
		"expired invite must report 410 Gone, not 404")
	assert.Equal(t, 410, apierrors.WsWorkspaceInviteExhausted.Status,
		"exhausted invite must report 410 Gone, not 404")
}
