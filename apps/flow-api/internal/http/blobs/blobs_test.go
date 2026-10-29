package blobs_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/blobs"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
)

const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// newRelay mounts the relay the way the router does, with no object storage
// behind it: everything that must be decided before storage is touched can be
// checked here, and anything that would reach storage answers 503.
func newRelay(t *testing.T) (*httptest.Server, *storage.Signer) {
	t.Helper()
	signer := storage.NewSigner("", []byte("relay-unit-test-key"))
	r := chi.NewRouter()
	r.Mount(storage.BlobPath, blobs.Handler(blobs.Deps{Signer: signer}))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	signer.SetBase(srv.URL)
	return srv, signer
}

func do(t *testing.T, method, target, contentType string, body io.Reader, length int64) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, target, body)
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.ContentLength = length
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(raw)
}

func TestRelayRefusalsLookTheSame(t *testing.T) {
	t.Parallel()
	srv, signer := newRelay(t)
	const key = "workspace/aa/obj"

	good, err := signer.DownloadURL(key, "f.txt", time.Minute)
	require.NoError(t, err)
	expired, err := signer.DownloadURL(key, "f.txt", -time.Minute)
	require.NoError(t, err)
	tampered := strings.Replace(good, "fn=f.txt", "fn=g.txt", 1)
	require.NotEqual(t, good, tampered)
	unsigned := srv.URL + storage.BlobPath + "/" + key
	otherKey, err := signer.DownloadURL(key+"2", "f.txt", time.Minute)
	require.NoError(t, err)
	swapped := strings.Replace(otherKey, "/"+key+"2?", "/"+key+"?", 1)

	var bodies []string
	for name, u := range map[string]string{
		"expired": expired, "tampered": tampered, "unsigned": unsigned, "other key": swapped,
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			resp, body := do(t, method, u, "", nil, 0)
			require.Equalf(t, http.StatusForbidden, resp.StatusCode, "%s %s", method, name)
			if method == http.MethodGet {
				bodies = append(bodies, body)
			}
		}
	}
	for _, b := range bodies {
		require.Equal(t, bodies[0], b, "a refusal must not say why")
	}

	// A good signature with nothing behind it is a 503, not a 404 and not a pass.
	resp, _ := do(t, http.MethodGet, good, "", nil, 0)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestRelayPutRefusals(t *testing.T) {
	t.Parallel()
	_, signer := newRelay(t)
	const key = "workspace/aa/" + sha

	signed, err := signer.PutURL(key, "image/png", 10, sha, time.Minute)
	require.NoError(t, err)
	expired, err := signer.PutURL(key, "image/png", 10, sha, -time.Minute)
	require.NoError(t, err)
	widened := strings.Replace(signed, "sz=10", "sz=1000000", 1)
	require.NotEqual(t, signed, widened)
	// A download grant is not a write grant.
	dl, err := signer.DownloadURL(key, "f", time.Minute)
	require.NoError(t, err)

	body := strings.Repeat("x", 10)
	for name, u := range map[string]string{"expired": expired, "widened size": widened, "download grant": dl} {
		resp, _ := do(t, http.MethodPut, u, "image/png", strings.NewReader(body), 10)
		require.Equalf(t, http.StatusForbidden, resp.StatusCode, name)
	}

	// A well-formed request ends at the storage check; the length, type and
	// digest matrix needs real storage and lives in the e2e suite.
	resp, _ := do(t, http.MethodPut, signed, "image/png", strings.NewReader(body), 10)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestRelayRejectsOtherMethods(t *testing.T) {
	t.Parallel()
	_, signer := newRelay(t)
	u, err := signer.DownloadURL("k", "f", time.Minute)
	require.NoError(t, err)
	for _, m := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		resp, _ := do(t, m, u, "", nil, 0)
		require.Equalf(t, http.StatusMethodNotAllowed, resp.StatusCode, m)
	}
}

func TestRelayKeyWithEscapedCharactersVerifies(t *testing.T) {
	t.Parallel()
	_, signer := newRelay(t)
	// The key is signed as the decoded path; the relay must recover the same
	// bytes from an escaped request path or every such key would be refused.
	u, err := signer.DownloadURL("user/a b/ü", "f", time.Minute)
	require.NoError(t, err)
	parsed, err := url.Parse(u)
	require.NoError(t, err)
	require.Contains(t, parsed.EscapedPath(), "a%20b")
	resp, _ := do(t, http.MethodGet, u, "", nil, 0)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "signature must verify for an escaped key")
}
