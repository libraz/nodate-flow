package stream

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/lifecycle"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// An open stream never finishes on its own, so unless shutdown reaches it
// Server.Shutdown waits out its whole deadline and every later shutdown
// phase runs out of time.
func TestSSEStreamEndsWhenTheServerShutsDown(t *testing.T) {
	db := tailDB(t)
	wsID, wsPub := seedWorkspace(t, db, fmt.Sprintf("sse-shutdown-%d", time.Now().UnixNano()))
	userID := seedMember(t, db, wsID, fmt.Sprintf("shutdown-%d@example.test", time.Now().UnixNano()))

	asActor := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(authn.WithActor(r.Context(), userID)))
		})
	}
	closing := make(chan struct{})
	r := chi.NewRouter()
	r.Use(lifecycle.WithStreamShutdown(closing))
	r.Group(func(sub chi.Router) {
		sub.Use(asActor)
		sub.Use(middleware.RequireWorkspaceMember(db))
		sub.Get("/workspaces/{wsId}/stream", SSEHandler(NewInProcessNotifier(), nil, nil))
	})

	srv := httptest.NewUnstartedServer(r)
	srv.Config.RegisterOnShutdown(sync.OnceFunc(func() { close(closing) }))
	srv.Start()
	t.Cleanup(srv.Close)

	reqCtx, cancelReq := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReq()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL+"/workspaces/"+wsPub+"/stream", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	scanner := bufio.NewScanner(resp.Body)
	if event, open := readFrame(scanner); !open || event != string(KindResync) {
		t.Fatalf("first frame = %q open=%v, want a resync marker", event, open)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	if err := srv.Config.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown with an open stream = %v after %s, want the stream to end promptly",
			err, time.Since(started).Round(time.Millisecond))
	}
	if _, open := readFrame(scanner); open {
		t.Fatal("the stream kept delivering after the server shut down")
	}
}
