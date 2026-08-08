package stream

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// seedMember creates a user and an enabled membership row, returning
// the user's internal id.
func seedMember(t *testing.T, db *sql.DB, wsID uint32, email string) uint32 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO users (public_id, email, display_name)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, ?)`, email, email)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	userID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("user id: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, 'member', NOW(3))`, wsID, userID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	//#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative
	return uint32(userID)
}

// removeMember takes the user out of the workspace the way the members
// endpoint does.
func removeMember(t *testing.T, db *sql.DB, wsID, userID uint32) {
	t.Helper()
	if _, err := db.Exec(
		`UPDATE workspace_members SET enabled = FALSE WHERE workspace_id = ? AND user_id = ?`,
		wsID, userID,
	); err != nil {
		t.Fatalf("remove member: %v", err)
	}
}

// streamServer mounts the SSE handler behind the workspace gate and
// hands the same gate to the re-authorizer, which is how router.go
// wires it. The actor is injected instead of resolved from a token so
// the test needs no issuer; membership — the part that changes when
// somebody is removed — is decided by the real middleware, which
// resolves it through acl.CheckWorkspaceMember, the same function the
// MCP stream's revalidation goes through.
func streamServer(t *testing.T, db *sql.DB, userID uint32) (*httptest.Server, *InProcessNotifier) {
	t.Helper()
	asActor := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(authn.WithActor(r.Context(), userID)))
		})
	}
	member := middleware.RequireWorkspaceMember(db)
	reauthorize := middleware.StreamReauthorizer(asActor, member)

	notifier := NewInProcessNotifier()
	r := chi.NewRouter()
	r.Group(func(sub chi.Router) {
		sub.Use(asActor)
		sub.Use(member)
		sub.Get("/workspaces/{wsId}/stream",
			SSEHandler(notifier, nil, reauthorize))
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, notifier
}

// readFrame reads SSE lines until one frame terminates, reporting
// whether the stream is still open.
func readFrame(scanner *bufio.Scanner) (event string, open bool) {
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[len("event:"):])
		case line == "" && event != "":
			return event, true
		}
	}
	return "", false
}

// A stream authorized only when it opened keeps delivering every event
// in the workspace for as long as the client holds the socket. Removing
// somebody from a workspace has to reach the connection they already
// have, not just the requests they make next.
func TestSSEStreamClosesWhenTheCallerLosesMembership(t *testing.T) {
	db := tailDB(t)

	// The re-check cadence is a package variable so this test does not
	// have to wait the production interval. Restored before the next
	// test in this package runs; the package's tests are not parallel
	// with each other on this knob.
	prev := reauthorizeInterval
	reauthorizeInterval = 50 * time.Millisecond
	t.Cleanup(func() { reauthorizeInterval = prev })

	wsID, wsPub := seedWorkspace(t, db, fmt.Sprintf("sse-reauth-%d", time.Now().UnixNano()))
	userID := seedMember(t, db, wsID, fmt.Sprintf("reauth-%d@example.test", time.Now().UnixNano()))
	srv, _ := streamServer(t, db, userID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/workspaces/"+wsPub+"/stream", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	if event, open := readFrame(scanner); !open || event != string(KindResync) {
		t.Fatalf("first frame = %q open=%v, want a resync marker", event, open)
	}

	removeMember(t, db, wsID, userID)

	// The read returns when the server closes the connection. The wait
	// is bounded well short of the request's own 30s deadline, so a
	// stream that outlives its authorization fails here rather than
	// passing on the client's cancellation.
	ended := make(chan string, 1)
	go func() {
		event, open := readFrame(scanner)
		if open {
			ended <- event
			return
		}
		ended <- ""
	}()
	select {
	case event := <-ended:
		if event != "" {
			t.Fatalf("stream delivered %q after the caller was removed from the workspace", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream stayed open after the caller was removed from the workspace")
	}
}

// The re-check must not be a slow disconnect: a caller who is still a
// member keeps the stream they opened.
func TestSSEStreamSurvivesReauthorizationWhileStillAMember(t *testing.T) {
	db := tailDB(t)

	prev := reauthorizeInterval
	reauthorizeInterval = 20 * time.Millisecond
	t.Cleanup(func() { reauthorizeInterval = prev })

	wsID, wsPub := seedWorkspace(t, db, fmt.Sprintf("sse-keep-%d", time.Now().UnixNano()))
	userID := seedMember(t, db, wsID, fmt.Sprintf("keep-%d@example.test", time.Now().UnixNano()))
	srv, notifier := streamServer(t, db, userID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/workspaces/"+wsPub+"/stream", nil)
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

	// Well past several re-checks, then publish: a stream that had been
	// closed by its own re-check would never carry this.
	time.Sleep(200 * time.Millisecond)
	notifier.Publish(context.Background(), Event{
		Kind:        KindTaskChanged,
		WorkspaceID: wsPub,
		At:          time.Now().Unix(),
	})

	if event, open := readFrame(scanner); !open || event != string(KindTaskChanged) {
		t.Fatalf("frame = %q open=%v, want task.changed on a stream that is still authorized", event, open)
	}
}
