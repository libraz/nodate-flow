package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/packages/go-shared/email"
)

// registeredOperations returns the "METHOD path" set the built router
// documents across all of its sub-APIs.
func registeredOperations(t *testing.T, deps Deps) map[string]bool {
	t.Helper()
	ops := map[string]bool{}
	for _, a := range BuildResult(deps).APIs {
		spec := a.OpenAPI()
		if spec == nil {
			continue
		}
		for path, item := range spec.Paths {
			if item == nil {
				continue
			}
			if item.Get != nil {
				ops[http.MethodGet+" "+path] = true
			}
			if item.Post != nil {
				ops[http.MethodPost+" "+path] = true
			}
			if item.Delete != nil {
				ops[http.MethodDelete+" "+path] = true
			}
		}
	}
	return ops
}

// passwordRoutes are the routes that exist only for password accounts.
var passwordRoutes = []string{
	"POST /auth/register",
	"POST /auth/login",
	"POST /me/password",
}

// totpManagementRoutes are the routes that manage the second factor of a
// password account; each reauthenticates with the local password.
var totpManagementRoutes = []string{
	"GET /me/totp",
	"DELETE /me/totp",
	"POST /me/totp/enroll",
	"POST /me/totp/confirm",
	"GET /me/totp/recovery-codes",
	"POST /me/totp/recovery-codes",
}

// magicLinkRoutes are the routes that exist only when mail can be sent.
var magicLinkRoutes = []string{
	"POST /auth/magic-link/request",
	"GET /auth/magic-link/verify",
}

func TestPasswordRoutes_AbsentWhenPasswordLoginDisabled(t *testing.T) {
	t.Parallel()
	deps := stubDeps(t)
	deps.PasswordLoginEnabled = false
	ops := registeredOperations(t, deps)
	for _, r := range passwordRoutes {
		assert.Falsef(t, ops[r], "%s must not be registered when password login is disabled", r)
	}
	for _, r := range totpManagementRoutes {
		assert.Falsef(t, ops[r], "%s must not be registered when password login is disabled", r)
	}
	// The shared TOTP step-up endpoint also finishes OIDC sign-ins, and
	// accounts enrolled earlier keep their challenge; OIDC routes are unaffected.
	for _, r := range []string{"POST /auth/login/totp", "GET /auth/oidc/google/start", "GET /auth/capabilities"} {
		assert.Truef(t, ops[r], "%s must stay registered", r)
	}
}

func TestPasswordRoutes_PresentWhenPasswordLoginEnabled(t *testing.T) {
	t.Parallel()
	deps := stubDeps(t)
	deps.PasswordLoginEnabled = true
	ops := registeredOperations(t, deps)
	for _, r := range append(append([]string{}, passwordRoutes...), totpManagementRoutes...) {
		assert.Truef(t, ops[r], "%s must be registered when password login is enabled", r)
	}
}

func TestMagicLinkRoutes_RequireARealMailer(t *testing.T) {
	t.Parallel()
	for name, sender := range map[string]email.Sender{
		"nil sender":  nil,
		"noop sender": email.NoopSender{},
	} {
		deps := stubDeps(t)
		deps.EmailSender = sender
		ops := registeredOperations(t, deps)
		for _, r := range magicLinkRoutes {
			assert.Falsef(t, ops[r], "%s: %s must not be registered", name, r)
		}
	}

	deps := stubDeps(t)
	deps.EmailSender = &email.MemorySender{}
	ops := registeredOperations(t, deps)
	for _, r := range magicLinkRoutes {
		assert.Truef(t, ops[r], "%s must be registered with a real mailer", r)
	}
}

func TestCapabilities_ReflectsPasswordLoginAndMailer(t *testing.T) {
	t.Parallel()
	deps := stubDeps(t)
	deps.PasswordLoginEnabled = false
	deps.EmailSender = nil

	rec := httptest.NewRecorder()
	Build(deps).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/capabilities", nil))
	require.Equalf(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var caps struct {
		PasswordLogin bool `json:"passwordLogin"`
		MagicLink     bool `json:"magicLink"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &caps))
	assert.False(t, caps.PasswordLogin)
	assert.False(t, caps.MagicLink)
}
