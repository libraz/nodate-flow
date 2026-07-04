package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/router"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/openapiutil"
)

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

	merged := mergeSpecs(res.APIs)
	openapiutil.PatchErrorModelSchema(merged)
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
