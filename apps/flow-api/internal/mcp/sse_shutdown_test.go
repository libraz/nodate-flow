package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/lifecycle"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// An open MCP stream never finishes on its own, so unless shutdown
// reaches it Server.Shutdown waits out its whole deadline.
func TestMCPStreamEndsWhenTheServerShutsDown(t *testing.T) {
	requireMCPIntegration(t)
	db := helpers.StartShared(t).DB

	closing := make(chan struct{})
	handler := mcp.NewHandler(mcp.Deps{DB: db, Queries: generated.New(db)})
	srv := httptest.NewUnstartedServer(lifecycle.WithStreamShutdown(closing)(handler))
	srv.Config.RegisterOnShutdown(sync.OnceFunc(func() { close(closing) }))
	srv.Start()
	t.Cleanup(srv.Close)

	fx := seedMCPStreamFixture(t, db, []string{"read:workspace"})
	resp, closed := openStream(t, srv.URL, fx.plain)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, srv.Config.Shutdown(shutdownCtx), "an open stream must not hold shutdown to its deadline")
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("the stream stayed open after the server shut down")
	}
}
