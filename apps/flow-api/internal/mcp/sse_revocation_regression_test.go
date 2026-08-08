package mcp_test

import (
	"bufio"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// mcpStreamFixture is one workspace member holding one MCP token.
type mcpStreamFixture struct {
	wsID    uint32
	userID  uint32
	tokenID uint32
	plain   string
}

func seedMCPStreamFixture(t *testing.T, db *sql.DB, scopes []string) *mcpStreamFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.New().String()[:8]

	userPub := uuid.Must(uuid.NewV7())
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
		userPub[:], "mcpstream-"+suffix+"@example.test", "MCPStream "+suffix)
	require.NoError(t, err)
	userID64, err := res.LastInsertId()
	require.NoError(t, err)
	userID := uint32(userID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	wsPub := uuid.Must(uuid.NewV7())
	res, err = db.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		wsPub[:], "mcpstream-ws-"+suffix, "MCPStream Workspace")
	require.NoError(t, err)
	wsID64, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	memberPub := uuid.Must(uuid.NewV7())
	_, err = db.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'owner')`,
		memberPub[:], wsID, userID)
	require.NoError(t, err)

	plain, tokenID := seedMCPToken(t, db, wsID, userID, scopes)

	return &mcpStreamFixture{
		wsID:    wsID,
		userID:  userID,
		tokenID: tokenID,
		plain:   plain,
	}
}

// openStream opens an SSE connection and returns the response plus a
// channel that closes when the server stops writing.
//
// The request carries a cancellable context that the test cancels on
// cleanup. Without it httptest.Server.Close would block forever waiting
// for a stream that is behaving correctly by staying open.
func openStream(t *testing.T, baseURL, token string) (*http.Response, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/mcp", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req) //nolint:bodyclose // closed by the reader goroutine below
	require.NoError(t, err)

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		defer func() { _ = resp.Body.Close() }()
		r := bufio.NewReader(resp.Body)
		for {
			if _, rerr := r.ReadString('\n'); rerr != nil {
				return
			}
		}
	}()
	return resp, closed
}

// TestMCPStreamStopsWhenTheCredentialDoes proves an already-open event
// stream ends when the credential behind it stops being valid.
//
// This is what the token-revocation UI promises. Before it held, the
// stream authenticated once at connect and never again: revoking a token
// left every stream it had opened delivering the workspace's events
// until the client chose to hang up, and removing somebody from a
// workspace did not touch mcp_tokens at all, so their stream carried on
// and they could open a new one.
func TestMCPStreamStopsWhenTheCredentialDoes(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB

	restore := mcp.SetSSEIntervalsForTest(50*time.Millisecond, 50*time.Millisecond)
	t.Cleanup(restore)

	handler := mcp.NewHandler(mcp.Deps{DB: db, Queries: generated.New(db)})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	t.Run("revoked_token/stream_closes", func(t *testing.T) {
		fx := seedMCPStreamFixture(t, db, []string{"read:workspace"})
		resp, closed := openStream(t, srv.URL, fx.plain)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Positive control: a live credential keeps the stream open.
		select {
		case <-closed:
			t.Fatal("stream closed while the token was still valid")
		case <-time.After(300 * time.Millisecond):
		}

		_, err := db.Exec(
			`UPDATE mcp_tokens SET revoked_at = CURRENT_TIMESTAMP, enabled = FALSE WHERE id = ?`,
			fx.tokenID)
		require.NoError(t, err)

		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Fatal("revoking the token did not close the open stream")
		}
	})

	t.Run("removed_member/stream_closes", func(t *testing.T) {
		fx := seedMCPStreamFixture(t, db, []string{"read:workspace"})
		resp, closed := openStream(t, srv.URL, fx.plain)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Removing a member does not touch mcp_tokens, which is exactly
		// why the stream has to re-ask rather than trust its own opening
		// decision.
		_, err := db.Exec(
			`UPDATE workspace_members SET enabled = FALSE WHERE workspace_id = ? AND user_id = ?`,
			fx.wsID, fx.userID)
		require.NoError(t, err)

		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Fatal("removing the member from the workspace did not close their open stream")
		}
	})

	t.Run("removed_member/cannot_open_a_stream", func(t *testing.T) {
		fx := seedMCPStreamFixture(t, db, []string{"read:workspace"})
		_, err := db.Exec(
			`UPDATE workspace_members SET enabled = FALSE WHERE workspace_id = ? AND user_id = ?`,
			fx.wsID, fx.userID)
		require.NoError(t, err)

		resp, _ := openStream(t, srv.URL, fx.plain)
		require.NotEqual(t, http.StatusOK, resp.StatusCode,
			"a token whose owner is no longer in the workspace must not be able to open a stream")
	})

	t.Run("no_read_scope/cannot_open_a_stream", func(t *testing.T) {
		fx := seedMCPStreamFixture(t, db, []string{})
		resp, _ := openStream(t, srv.URL, fx.plain)
		require.Equal(t, http.StatusForbidden, resp.StatusCode,
			"the stream carries every workspace event, so it needs read:workspace like any other read")
	})

	t.Run("read_scope/can_open_a_stream", func(t *testing.T) {
		// The negative cases above are all satisfied by a server that
		// refuses every stream; this one is what says it does not.
		fx := seedMCPStreamFixture(t, db, []string{"read:workspace"})
		resp, closed := openStream(t, srv.URL, fx.plain)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		select {
		case <-closed:
			t.Fatal("a valid read:workspace token was refused a stream")
		case <-time.After(300 * time.Millisecond):
		}
	})
}
