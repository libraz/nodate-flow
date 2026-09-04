package mcp

import "context"

// This file exists only in the test build. It exposes the stream's two
// authorization entry points to the external (mcp_test) package, which is
// where a DB-backed test has to live: such a test imports tests/helpers,
// and tests/helpers imports this package.
//
// Production reaches authorizeStream and revalidateStream from inside
// serveSSE's select loop, whose revalidation period is measured in tens of
// seconds. Observing one decision through that loop would make every case a
// multi-second wait on a real timer; calling the same two methods directly
// asks the same question of the same code against the same database, and
// leaves the production period alone.

// NewTestAgentSession builds a caller session that claims to act on behalf
// of the given agent. NewTestSession covers the human-token case; this is
// the one a stream test needs to hand revalidateStream a stale or wrong
// agent id and watch which one the rebuilt session ends up carrying.
func NewTestAgentSession(userID, workspaceID, agentID uint32, scopes []string) *TestSession {
	return &session{userID: userID, workspaceID: workspaceID, agentID: agentID, scopes: scopes}
}

// AuthorizeStreamForTest is the gate serveSSE applies before a stream is
// registered.
func (h *Handler) AuthorizeStreamForTest(ctx context.Context, s *TestSession) error {
	return h.authorizeStream(ctx, s)
}

// RevalidateStreamForTest is the check the stream's revalidation tick runs
// against an already-open connection.
func (h *Handler) RevalidateStreamForTest(ctx context.Context, token string, s *TestSession) error {
	return h.revalidateStream(ctx, token, s)
}
