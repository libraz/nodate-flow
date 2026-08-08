package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// allowAll is a middleware that lets everything through.
func allowAll(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

// denyWith is a middleware that rejects the way the real gates do —
// through writeSpecError, so the captured body is the same problem+json
// a client would have received.
func denyWith(spec *apierrors.Spec) func(http.Handler) http.Handler {
	return func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeSpecError(w, spec)
		})
	}
}

func TestStreamReauthorizerPassesWhenTheChainPasses(t *testing.T) {
	t.Parallel()

	check := StreamReauthorizer(allowAll, allowAll)
	if err := check(httptest.NewRequest(http.MethodGet, "/workspaces/x/stream", nil)); err != nil {
		t.Fatalf("chain accepted the request but the re-check reported %v", err)
	}
}

// The point of replaying the chain is that the reason travels with the
// refusal: the stream logs the code a fresh request would have been
// answered with, rather than a generic "closed".
func TestStreamReauthorizerReportsTheGatesOwnCode(t *testing.T) {
	t.Parallel()

	check := StreamReauthorizer(allowAll, denyWith(apierrors.WsWorkspaceAccessDenied))
	err := check(httptest.NewRequest(http.MethodGet, "/workspaces/x/stream", nil))
	if err == nil {
		t.Fatal("a rejected chain must fail the re-check")
	}
	var unauth *ErrStreamUnauthorized
	if !asStreamUnauthorized(err, &unauth) {
		t.Fatalf("error type = %T, want *ErrStreamUnauthorized", err)
	}
	if unauth.Code != apierrors.WsWorkspaceAccessDenied.Code {
		t.Errorf("code = %q, want %q", unauth.Code, apierrors.WsWorkspaceAccessDenied.Code)
	}
	if unauth.Status != apierrors.WsWorkspaceAccessDenied.Status {
		t.Errorf("status = %d, want %d", unauth.Status, apierrors.WsWorkspaceAccessDenied.Status)
	}
}

// A middleware that never reaches the handler and writes nothing is
// still a refusal — the stream must not read "no error written" as
// "still authorized".
func TestStreamReauthorizerTreatsASilentStopAsRefusal(t *testing.T) {
	t.Parallel()

	silent := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	if err := StreamReauthorizer(silent)(httptest.NewRequest(http.MethodGet, "/x", nil)); err == nil {
		t.Fatal("a chain that never reached the handler must fail the re-check")
	}
}

// Every open stream shares one re-check closure, so a connection that
// is still authorized must not be able to read another connection's
// refusal — or the reverse.
func TestStreamReauthorizerIsSafeUnderConcurrentStreams(t *testing.T) {
	t.Parallel()

	// The gate answers according to a header, so both outcomes run
	// through the same closure at the same time.
	byHeader := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Member") == "yes" {
				next.ServeHTTP(w, r)
				return
			}
			writeSpecError(w, apierrors.WsWorkspaceAccessDenied)
		})
	}
	check := StreamReauthorizer(byHeader)

	member := httptest.NewRequest(http.MethodGet, "/x", nil)
	member.Header.Set("X-Member", "yes")
	removed := httptest.NewRequest(http.MethodGet, "/x", nil)

	var wg sync.WaitGroup
	errs := make([]error, 200)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				errs[i] = check(member)
			} else {
				errs[i] = check(removed)
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if i%2 == 0 && err != nil {
			t.Fatalf("call %d: a member was refused: %v", i, err)
		}
		if i%2 == 1 && err == nil {
			t.Fatalf("call %d: a removed caller was accepted", i)
		}
	}
}

// asStreamUnauthorized is errors.As, spelled out so the test does not
// depend on the wrapping shape.
func asStreamUnauthorized(err error, target **ErrStreamUnauthorized) bool {
	e, ok := err.(*ErrStreamUnauthorized)
	if ok {
		*target = e
	}
	return ok
}
