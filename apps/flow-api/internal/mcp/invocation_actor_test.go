package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInvocationActorRecordsTheAgent covers the attribution mcp_invocations
// exists to answer.
//
// An MCP token minted for an agent still carries its creator's user id,
// so a row that records only the user is indistinguishable from that
// person working by hand — every tool call an agent makes shows up in
// the activity timeline under a human's name and avatar. The agent id
// is what makes the two separable, and v_workspace_activity reads it
// first when deciding actor_kind.
func TestInvocationActorRecordsTheAgent(t *testing.T) {
	t.Parallel()

	userID, agentID := invocationActor(&session{userID: 7, agentID: 42})

	require.True(t, userID.Valid, "the token owner stays on the row")
	require.Equal(t, int32(7), userID.Int32)
	require.True(t, agentID.Valid,
		"an agent-owned token must record the agent, or its work is filed under the human who minted it")
	require.Equal(t, int32(42), agentID.Int32)
}

// TestInvocationActorLeavesAgentNullForHumans is the other half: a
// human's own token must not be attributed to an agent, or the Bot
// badge appears on work a person did.
func TestInvocationActorLeavesAgentNullForHumans(t *testing.T) {
	t.Parallel()

	userID, agentID := invocationActor(&session{userID: 7})

	require.True(t, userID.Valid)
	require.False(t, agentID.Valid, "a human-owned token has no agent")
}

// TestInvocationActorHandlesNoSession keeps the audit write safe on the
// paths that fail before authentication resolves a session.
func TestInvocationActorHandlesNoSession(t *testing.T) {
	t.Parallel()

	userID, agentID := invocationActor(nil)

	require.False(t, userID.Valid)
	require.False(t, agentID.Valid)
}
