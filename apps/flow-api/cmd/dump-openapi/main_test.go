package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/router"
)

// TestDumpOpenAPIMatchesLiveServedSpec pins that the document shipped to
// the SDK is the document the server answers with.
//
// Both sides call the same router.MergeAPIs, so this no longer guards
// two hand-written folds against drifting apart — that was its original
// job, and unifying the folds retired it. Two things it still pins, both
// of which the unification did not cover:
//
// The route has to actually serve the merged document. buildOpenAPIJSON
// renders it once at build time and the handler closes over the bytes;
// nothing else checks that those bytes reach the wire, so a handler
// serving a stale snapshot, an unmerged sub-API document, or the
// marshal-failure fallback is caught only here.
//
// And the merge has to stay repeatable. The live handler has already
// folded these same huma.API values by the time the test folds them
// again, so a fold that wrote into the sub-APIs it reads — as it did
// while both copies existed — makes the second fold disagree with the
// first, or fail outright. This is the assertion that fails if MergeAPIs
// stops building a fresh document.
//
// Neither is implied by the two sides sharing a function, so the test is
// weaker than it was but not vacuous.
func TestDumpOpenAPIMatchesLiveServedSpec(t *testing.T) {
	t.Parallel()

	issuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		t.Fatalf("jwt issuer: %v", err)
	}

	res := router.BuildResult(router.Deps{
		JWT:              issuer,
		DisableRateLimit: true,
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	res.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json status = %d, want 200", rr.Code)
	}

	var live any
	if err := json.Unmarshal(rr.Body.Bytes(), &live); err != nil {
		t.Fatalf("unmarshal live openapi: %v", err)
	}

	merged, err := router.MergeAPIs(res.APIs)
	if err != nil {
		t.Fatalf("merge apis: %v", err)
	}
	dumpBytes, err := json.Marshal(merged)
	if err != nil {
		t.Fatalf("marshal dump openapi: %v", err)
	}
	var dumped any
	if err := json.Unmarshal(dumpBytes, &dumped); err != nil {
		t.Fatalf("unmarshal dump openapi: %v", err)
	}

	if !reflect.DeepEqual(live, dumped) {
		t.Fatalf("dump-openapi output diverges from live /openapi.json")
	}
}
