package authn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/problem"
	"github.com/stretchr/testify/require"
)

type staticResolver struct {
	err error
}

func (r staticResolver) Resolve(context.Context, string) (uint32, dbtype.PublicID, error) {
	if r.err != nil {
		return 0, dbtype.PublicID{}, r.err
	}
	return 42, dbtype.PublicID{}, nil
}

func TestRequireAuth_PropagatesAPIErrorFromResolver(t *testing.T) {
	t.Parallel()
	spec := &apierr.Spec{
		Code:    "AUTH.PAT.EXPIRED",
		Status:  401,
		Message: "Personal access token has expired",
	}
	rec := exerciseRequireAuth(t, apierr.New(spec))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	requireJSONError(t, rec, spec.Code, spec.Message)
}

func TestRequireAuth_UserDisabledIsUnauthorizedSession(t *testing.T) {
	t.Parallel()
	rec := exerciseRequireAuth(t, ErrUserDisabled)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	requireJSONError(t, rec, "AUTH.SESSION.UNAUTHORIZED", "You must be signed in to access this resource")
}

func TestRequireAuth_UnexpectedResolverErrorIs500(t *testing.T) {
	t.Parallel()
	rec := exerciseRequireAuth(t, errors.New("database unavailable"))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	requireJSONError(t, rec, "INTERNAL.UNEXPECTED", "Unexpected server error")
}

func TestRequireAuth_MissingTokenUsesTheSameEnvelope(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	RequireAuth(staticResolver{})(next).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	requireJSONError(t, rec, "AUTH.TOKEN.MISSING_OR_MALFORMED", "Missing or invalid authentication token")
}

func exerciseRequireAuth(t *testing.T, resolverErr error) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	RequireAuth(staticResolver{err: resolverErr})(next).ServeHTTP(rec, req)
	return rec
}

// requireJSONError asserts the middleware answered with the canonical
// problem+json envelope.
//
// The status inside the body is checked, and not only the response
// code, because that is the field clients actually read: the SDK builds
// its ApiError from the parsed envelope, and the frontend's dead-session
// handling and its "never retry a 4xx" rule both branch on the status it
// carries. This middleware guards nearly every authenticated route, so
// when it answered with a bare {code, message} an expired token reached
// the browser as an error with no status at all — indistinguishable from
// a connection failure, leaving the user apparently signed in.
func requireJSONError(t *testing.T, rec *httptest.ResponseRecorder, code, message string) {
	t.Helper()
	require.Equal(t, problem.ContentType, rec.Header().Get("Content-Type"))
	var body problem.Details
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, code, body.Type)
	require.Equal(t, message, body.Detail)
	require.Equal(t, rec.Code, body.Status)
	require.Equal(t, http.StatusText(rec.Code), body.Title)

	// The old shape is not merely absent from the struct above; it must
	// be absent from the payload, or a client could still be reading it.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.NotContains(t, raw, "code")
	require.NotContains(t, raw, "message")
}
