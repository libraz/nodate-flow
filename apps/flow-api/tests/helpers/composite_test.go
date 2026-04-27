package helpers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompositeHandler_PrimaryServed(t *testing.T) {
	primary := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Source", "primary")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("from primary"))
	})
	secondary := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("secondary should not be called when primary returns 200")
	})
	composite := newCompositeHandler(primary, secondary)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	composite.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "primary", rec.Header().Get("X-Source"))
	body, _ := io.ReadAll(rec.Body)
	require.Equal(t, "from primary", string(body))
}

func TestCompositeHandler_FallbackOnNotFound(t *testing.T) {
	primary := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	secondary := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Source", "secondary")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("from secondary"))
	})
	composite := newCompositeHandler(primary, secondary)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/only-in-secondary", nil)
	composite.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "secondary", rec.Header().Get("X-Source"))
	body, _ := io.ReadAll(rec.Body)
	require.Equal(t, "from secondary", string(body))
}

func TestCompositeHandler_PrimaryErrorPassesThrough(t *testing.T) {
	primary := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	})
	secondary := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("secondary should not be called for non-404 errors")
	})
	composite := newCompositeHandler(primary, secondary)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	composite.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	body, _ := io.ReadAll(rec.Body)
	require.Equal(t, "forbidden", string(body))
}

func TestCompositeHandler_HeadersPreserved(t *testing.T) {
	primary := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom", "value")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	secondary := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("should not reach secondary")
	})
	composite := newCompositeHandler(primary, secondary)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/create", nil)
	composite.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.Equal(t, "value", rec.Header().Get("X-Custom"))
}
