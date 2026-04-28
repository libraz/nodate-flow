package acl

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingWriter captures the parameters passed to Config.WriteError
// so tests can assert on the rendered status code, error code, and
// message without depending on a particular wire format.
type recordingWriter struct {
	called  bool
	status  int
	code    string
	message string
}

func (rw *recordingWriter) write(w http.ResponseWriter, _ *http.Request, status int, code, message string) {
	rw.called = true
	rw.status = status
	rw.code = code
	rw.message = message
	w.WriteHeader(status)
}

// recordingHandler is a stand-in for the wrapped handler. It records
// whether ServeHTTP was invoked so tests can assert that the
// middleware short-circuits on failure.
type recordingHandler struct {
	called bool
}

func (rh *recordingHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	rh.called = true
	w.WriteHeader(http.StatusOK)
}

// staticActor returns an [ActorIDExtractor] that always reports the
// given uid with the supplied presence flag.
func staticActor(uid uint32, ok bool) ActorIDExtractor {
	return func(*http.Request) (uint32, bool) {
		return uid, ok
	}
}

func TestRequireInstanceAdmin_HappyPath(t *testing.T) {
	t.Parallel()

	rw := &recordingWriter{}
	rh := &recordingHandler{}
	cfg := Config{
		IsInstanceAdmin: func(_ context.Context, uid uint32) (bool, error) {
			assert.Equal(t, uint32(42), uid)
			return true, nil
		},
		ExtractActor: staticActor(42, true),
		WriteError:   rw.write,
	}

	mw := RequireInstanceAdmin(cfg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)
	mw(rh).ServeHTTP(rec, req)

	assert.True(t, rh.called, "next handler must run on success")
	assert.False(t, rw.called, "WriteError must not run on success")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireInstanceAdmin_MissingActor(t *testing.T) {
	t.Parallel()

	rw := &recordingWriter{}
	rh := &recordingHandler{}
	cfg := Config{
		IsInstanceAdmin: func(context.Context, uint32) (bool, error) {
			t.Fatalf("IsInstanceAdmin must not be called when actor is missing")
			return false, nil
		},
		ExtractActor: staticActor(0, false),
		WriteError:   rw.write,
	}

	mw := RequireInstanceAdmin(cfg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)
	mw(rh).ServeHTTP(rec, req)

	assert.False(t, rh.called, "next handler must not run when actor is missing")
	require.True(t, rw.called, "WriteError must run when actor is missing")
	assert.Equal(t, http.StatusUnauthorized, rw.status)
	assert.Equal(t, CodeSessionUnauthorized, rw.code)
	assert.Equal(t, "AUTH.SESSION.UNAUTHORIZED", rw.code,
		"the constant must match the canonical errors/auth.yaml code name")
}

func TestRequireInstanceAdmin_NotAdmin(t *testing.T) {
	t.Parallel()

	rw := &recordingWriter{}
	rh := &recordingHandler{}
	cfg := Config{
		IsInstanceAdmin: func(_ context.Context, uid uint32) (bool, error) {
			assert.Equal(t, uint32(7), uid)
			return false, nil
		},
		ExtractActor: staticActor(7, true),
		WriteError:   rw.write,
	}

	mw := RequireInstanceAdmin(cfg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)
	mw(rh).ServeHTTP(rec, req)

	assert.False(t, rh.called, "next handler must not run when actor lacks role")
	require.True(t, rw.called, "WriteError must run when actor lacks role")
	assert.Equal(t, http.StatusForbidden, rw.status)
	assert.Equal(t, CodeInstanceAdminRequired, rw.code)
	assert.Equal(t, "AUTH.PERMISSION.INSTANCE_ADMIN_REQUIRED", rw.code,
		"the constant must match the canonical error code name")
}

func TestRequireInstanceAdmin_LookupError(t *testing.T) {
	t.Parallel()

	boom := errors.New("transport boom")
	rw := &recordingWriter{}
	rh := &recordingHandler{}
	cfg := Config{
		IsInstanceAdmin: func(context.Context, uint32) (bool, error) {
			return false, boom
		},
		ExtractActor: staticActor(99, true),
		WriteError:   rw.write,
	}

	mw := RequireInstanceAdmin(cfg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)
	mw(rh).ServeHTTP(rec, req)

	assert.False(t, rh.called, "next handler must not run when lookup errors")
	require.True(t, rw.called, "WriteError must run when lookup errors")
	assert.Equal(t, http.StatusInternalServerError, rw.status)
	assert.Equal(t, CodeInternalUnexpected, rw.code)
}

func TestRequireInstanceAdmin_PanicsOnNilCallbacks(t *testing.T) {
	t.Parallel()

	good := Config{
		IsInstanceAdmin: func(context.Context, uint32) (bool, error) { return true, nil },
		ExtractActor:    staticActor(1, true),
		WriteError:      (&recordingWriter{}).write,
	}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name:   "IsInstanceAdmin nil",
			mutate: func(c *Config) { c.IsInstanceAdmin = nil },
		},
		{
			name:   "ExtractActor nil",
			mutate: func(c *Config) { c.ExtractActor = nil },
		},
		{
			name:   "WriteError nil",
			mutate: func(c *Config) { c.WriteError = nil },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := good
			tc.mutate(&cfg)
			assert.Panics(t, func() {
				_ = RequireInstanceAdmin(cfg)
			})
		})
	}
}
