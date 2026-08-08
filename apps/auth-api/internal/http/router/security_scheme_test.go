package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// specOperations walks every sub-API document and returns each
// operation's path together with the security requirement it declares.
func specOperations(res Result) map[string][]map[string][]string {
	out := map[string][]map[string][]string{}
	for _, api := range res.APIs {
		spec := api.OpenAPI()
		if spec == nil || spec.Paths == nil {
			continue
		}
		for path, item := range spec.Paths {
			for method, op := range map[string]*huma.Operation{
				http.MethodGet:    item.Get,
				http.MethodPost:   item.Post,
				http.MethodPut:    item.Put,
				http.MethodPatch:  item.Patch,
				http.MethodDelete: item.Delete,
			} {
				if op == nil {
					continue
				}
				out[method+" "+path] = op.Security
			}
		}
	}
	return out
}

// TestSpecDeclaresTheBearerScheme pins the mechanism a reader of the
// published document needs in order to call anything behind a token.
func TestSpecDeclaresTheBearerScheme(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	if len(res.APIs) == 0 {
		t.Fatal("router registered no APIs")
	}
	for _, api := range res.APIs {
		spec := api.OpenAPI()
		if spec.Components == nil || spec.Components.SecuritySchemes == nil {
			t.Fatal("a sub-API document declares no securitySchemes")
		}
		scheme, ok := spec.Components.SecuritySchemes[bearerSchemeName]
		if !ok {
			t.Fatalf("securitySchemes has no %q entry", bearerSchemeName)
		}
		if scheme.Type != "http" || scheme.Scheme != "bearer" {
			t.Errorf("scheme is %s/%s, want http/bearer", scheme.Type, scheme.Scheme)
		}
	}
}

// TestSpecSecurityMatchesTheMiddleware asserts the document says what
// the router does: an operation declares the bearer requirement exactly
// when calling it without credentials is refused.
//
// Both sides are read from the running router — the requirement from the
// registered OpenAPI document, the refusal from an actual request — so
// neither can drift into agreeing with the other on paper.
func TestSpecSecurityMatchesTheMiddleware(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	security := specOperations(res)
	if len(security) == 0 {
		t.Fatal("router registered no operations")
	}

	guarded, open := 0, 0
	for key, requirement := range security {
		method, path, ok := splitKey(key)
		if !ok {
			t.Fatalf("unparseable operation key %q", key)
		}
		declared := len(requirement) > 0
		if declared {
			guarded++
		} else {
			open++
		}

		// The sign-in endpoints are checked by declaration only. They
		// answer 401 about the credential in the request rather than a
		// missing bearer — a refresh without the cookie, a login with
		// the wrong password — so a response tells us nothing here, and
		// the OIDC start handlers need a configured provider client that
		// these stub deps deliberately do not supply.
		if strings.HasPrefix(path, "/auth/") {
			if declared {
				t.Errorf("%s hands out credentials but declares %v", key, requirement)
			}
			continue
		}

		req := httptest.NewRequest(method, resolvePath(t, path), nil)
		rr := httptest.NewRecorder()
		res.Handler.ServeHTTP(rr, req)
		refused := rr.Code == http.StatusUnauthorized

		if refused && !declared {
			t.Errorf("%s rejects unauthenticated calls but declares no security requirement", key)
		}
		if !refused && declared {
			t.Errorf("%s answers without credentials but declares %v", key, requirement)
		}
	}
	// Both kinds must exist, or the loop above proves nothing.
	if guarded == 0 || open == 0 {
		t.Fatalf("expected operations of both kinds, got %d guarded / %d open", guarded, open)
	}
}

func splitKey(key string) (method, path string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == ' ' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

// resolvePath substitutes path parameters with fixture values so chi can
// match the route. The values are never inspected by the auth
// middleware, which is the only thing these tests reach.
func resolvePath(t *testing.T, p string) string {
	t.Helper()
	placeholders := map[string]string{
		"id":        "01HX000000000000000000000E",
		"inviteId":  "01HX000000000000000000000D",
		"provider":  "github",
		"sessionId": "01HX000000000000000000000A",
		"token":     "invite-token-stub",
		"userId":    "01HX000000000000000000000B",
		"wsId":      "01HX000000000000000000000C",
	}
	out := p
	for {
		open := -1
		for i := 0; i < len(out); i++ {
			if out[i] == '{' {
				open = i
				break
			}
		}
		if open < 0 {
			return out
		}
		closeIdx := -1
		for i := open; i < len(out); i++ {
			if out[i] == '}' {
				closeIdx = i
				break
			}
		}
		if closeIdx < 0 {
			t.Fatalf("malformed path template: %q", p)
		}
		name := out[open+1 : closeIdx]
		val, ok := placeholders[name]
		if !ok {
			t.Fatalf("path %q references unknown parameter %q — add it to resolvePath", p, name)
		}
		out = out[:open] + val + out[closeIdx+1:]
	}
}
