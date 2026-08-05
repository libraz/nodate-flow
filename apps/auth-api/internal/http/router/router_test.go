package router

import (
	"database/sql"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	_ "github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
)

// stubDeps returns the minimal Deps the router needs to build with
// every sub-API registered. The DB points at an unreachable address —
// these unit tests inspect the registered OpenAPI document only and
// never dispatch a real request.
func stubDeps(t *testing.T) Deps {
	t.Helper()
	issuer, err := auth.NewJWTIssuer(nil, "nodate-auth", "api", 15*time.Minute)
	if err != nil {
		t.Fatalf("jwt issuer: %v", err)
	}
	db, err := sql.Open("mysql", "stub:stub@tcp(127.0.0.1:1)/stub?timeout=1ms")
	if err != nil {
		t.Fatalf("stub db open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return Deps{
		DB:               db,
		Queries:          generated.New(db),
		JWT:              issuer,
		DisableRateLimit: true,
	}
}

// TestEveryOperationHasDescription mirrors the flow-api guard: every
// huma.Operation registered against any sub-API must carry a non-empty
// Description so the wire OpenAPI is self-documenting. Each operation
// must declare its Description inline at registration; there is no
// fallback — Summary alone is not enough.
func TestEveryOperationHasDescription(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))

	type missing struct {
		method      string
		path        string
		operationID string
		summary     string
	}
	seen := map[string]missing{}

	for _, a := range res.APIs {
		spec := a.OpenAPI()
		if spec == nil || spec.Paths == nil {
			continue
		}
		for path, item := range spec.Paths {
			if item == nil {
				continue
			}
			verbs := map[string]*huma.Operation{
				http.MethodGet:     item.Get,
				http.MethodPost:    item.Post,
				http.MethodPut:     item.Put,
				http.MethodPatch:   item.Patch,
				http.MethodDelete:  item.Delete,
				http.MethodHead:    item.Head,
				http.MethodOptions: item.Options,
			}
			for method, op := range verbs {
				if op == nil {
					continue
				}
				if strings.TrimSpace(op.Description) == "" {
					key := method + " " + path + " " + op.OperationID
					seen[key] = missing{
						method:      method,
						path:        path,
						operationID: op.OperationID,
						summary:     op.Summary,
					}
				}
			}
		}
	}

	bad := make([]missing, 0, len(seen))
	for _, m := range seen {
		bad = append(bad, m)
	}
	if len(bad) > 0 {
		sort.Slice(bad, func(i, j int) bool {
			if bad[i].path != bad[j].path {
				return bad[i].path < bad[j].path
			}
			return bad[i].method < bad[j].method
		})
		t.Errorf("%d operations are missing huma.Operation.Description:", len(bad))
		for _, m := range bad {
			t.Errorf("  %-6s %s (%s) — summary: %q", m.method, m.path, m.operationID, m.summary)
		}
	}
}
