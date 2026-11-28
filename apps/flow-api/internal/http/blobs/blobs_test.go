package blobs_test

import (
	"fmt"
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
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
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

const inlineCSP = "default-src 'none'; sandbox; frame-ancestors 'none'"

var storedBody = []byte(strings.Repeat("0123456789", 10))

// newStoredRelay mounts the relay behind the global security middleware, as the
// router does, over a client that reads one fixed object from a stand-in S3
// endpoint. The middleware sets its headers first, so a header the relay then
// fails to override shows through here.
func newStoredRelay(t *testing.T) (*httptest.Server, *storage.Signer) {
	t.Helper()
	modTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("location") {
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></LocationConstraint>`)
			return
		}
		w.Header().Set("ETag", `"fixed"`)
		http.ServeContent(w, r, "", modTime, strings.NewReader(string(storedBody)))
	}))
	t.Cleanup(s3.Close)

	signer := storage.NewSigner("", []byte("relay-unit-test-key"))
	client, err := storage.NewClient(storage.Config{
		Endpoint: strings.TrimPrefix(s3.URL, "http://"),
		Bucket:   "test",
		Signer:   signer,
	})
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Use(middleware.SecurityHeaders())
	r.Mount(storage.BlobPath, blobs.Handler(blobs.Deps{Storage: client, Signer: signer}))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	signer.SetBase(srv.URL)
	return srv, signer
}

func doWith(t *testing.T, method, target string, header map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, target, nil)
	require.NoError(t, err)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, raw
}

func requireInlineHeaders(t *testing.T, resp *http.Response, contentType, filename string) {
	t.Helper()
	require.Equal(t, contentType, resp.Header.Get("Content-Type"))
	require.Equal(t, storage.ContentDispositionInline(filename), resp.Header.Get("Content-Disposition"))
	require.True(t, strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline; "))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	// The security middleware's own policy must not survive on this response.
	require.Equal(t, inlineCSP, resp.Header.Get("Content-Security-Policy"))
	require.True(t, strings.HasPrefix(resp.Header.Get("Cache-Control"), "private, max-age="), resp.Header.Get("Cache-Control"))
}

func TestRelayInlineImageHeaders(t *testing.T) {
	t.Parallel()
	_, signer := newStoredRelay(t)
	const key = "workspace/aa/obj"
	// Alias and parameters normalize to the canonical type on the way out.
	u, _, err := signer.PreviewURL(key, "写真 1.png", "IMAGE/X-PNG; charset=binary", time.Now())
	require.NoError(t, err)

	t.Run("200", func(t *testing.T) {
		resp, body := doWith(t, http.MethodGet, u, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		requireInlineHeaders(t, resp, "image/png", "写真 1.png")
		require.Equal(t, storedBody, body)
	})
	t.Run("206", func(t *testing.T) {
		resp, body := doWith(t, http.MethodGet, u, map[string]string{"Range": "bytes=10-19"})
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		requireInlineHeaders(t, resp, "image/png", "写真 1.png")
		require.Equal(t, storedBody[10:20], body)
		require.Equal(t, fmt.Sprintf("bytes 10-19/%d", len(storedBody)), resp.Header.Get("Content-Range"))
	})
	t.Run("HEAD", func(t *testing.T) {
		resp, body := doWith(t, http.MethodHead, u, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		requireInlineHeaders(t, resp, "image/png", "写真 1.png")
		require.Empty(t, body)
	})
}

func TestRelayPlainDownloadKeepsAttachmentHeaders(t *testing.T) {
	t.Parallel()
	_, signer := newStoredRelay(t)
	u, err := signer.DownloadURL("workspace/aa/obj", "a.png", time.Minute)
	require.NoError(t, err)

	resp, body := doWith(t, http.MethodGet, u, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
	require.Equal(t, storage.ContentDispositionAttachment("a.png"), resp.Header.Get("Content-Disposition"))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	csp := resp.Header.Get("Content-Security-Policy")
	require.True(t, strings.HasPrefix(csp, "default-src 'self'"), csp)
	require.NotEqual(t, inlineCSP, csp)
	require.Equal(t, storedBody, body)
}

func TestRelayRefusesInlineWrite(t *testing.T) {
	t.Parallel()
	_, signer := newRelay(t)
	// A preview URL carries no size or digest and is bound to GET, so a PUT on
	// it is turned away before storage is consulted.
	u, _, err := signer.PreviewURL("workspace/aa/"+sha, "a.png", "image/png", time.Now())
	require.NoError(t, err)
	resp, _ := do(t, http.MethodPut, u, "image/png", strings.NewReader("x"), 1)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}
