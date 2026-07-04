package authn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
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

func requireJSONError(t *testing.T, rec *httptest.ResponseRecorder, code, message string) {
	t.Helper()
	var body errorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, code, body.Code)
	require.Equal(t, message, body.Message)
}
