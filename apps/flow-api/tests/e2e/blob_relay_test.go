// Blob relay suite. Object storage has no route from the browser, so every
// upload and download goes through /blobs on the API, authorized by the HMAC in
// the URL. These tests mint URLs straight from the storage client (the same
// call the presign handlers make) and drive the relay over real HTTP against
// real MinIO.
package e2e

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"

	flowstorage "github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
)

// relayKey returns an object key no other test will touch and removes whatever
// ends up stored there.
func relayKey(t *testing.T) string {
	t.Helper()
	var b [8]byte
	_, err := rand.Read(b[:])
	require.NoError(t, err)
	key := "relay-e2e/" + hex.EncodeToString(b[:])
	t.Cleanup(func() { _ = testStorage.Flow.RemoveObject(t.Context(), key) })
	return key
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

// relayPut sends a PUT with exactly the pieces the caller chose. length < 0
// sends the body chunked.
func relayPut(t *testing.T, target, contentType string, body []byte, length int64) (int, string) {
	t.Helper()
	var r io.Reader = bytes.NewReader(body)
	if length < 0 {
		// An opaque reader leaves the client no length to declare.
		r = io.MultiReader(bytes.NewReader(body))
	}
	req, err := http.NewRequest(http.MethodPut, target, r)
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.ContentLength = length
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// tamperSig changes the first hex digit of the signature.
func tamperSig(raw string) string {
	i := strings.Index(raw, "sig=") + len("sig=")
	repl := byte('0')
	if raw[i] == '0' {
		repl = '1'
	}
	return raw[:i] + string(repl) + raw[i+1:]
}

// TestBlobRelayPutMatrix covers the four independent inputs a PUT is judged on
// -- signature, body length against the signed size, Content-Type against the
// signed type, and body digest against the signed digest -- with the rows of a
// pairwise-covering set over the 3 x 4 x 4 x 2 value space, plus the
// all-valid row. Whatever the combination, the relay must answer with the
// first failing check in the order below and must leave no object behind unless
// every input was good.
func TestBlobRelayPutMatrix(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	rows := []struct{ sig, length, ctype, digest string }{
		{"tampered", "chunked", "other", "mismatch"},
		{"tampered", "smaller", "case_and_params", "match"},
		{"expired", "smaller", "exact", "mismatch"},
		{"valid", "exact", "missing", "mismatch"},
		{"tampered", "larger", "case_and_params", "mismatch"},
		{"expired", "exact", "other", "match"},
		{"valid", "larger", "missing", "match"},
		{"valid", "smaller", "other", "mismatch"},
		{"expired", "larger", "other", "mismatch"},
		{"valid", "chunked", "exact", "match"},
		{"valid", "exact", "case_and_params", "mismatch"},
		{"tampered", "chunked", "missing", "mismatch"},
		{"tampered", "smaller", "exact", "mismatch"},
		{"valid", "smaller", "missing", "match"},
		{"valid", "larger", "exact", "mismatch"},
		{"expired", "chunked", "case_and_params", "match"},
		{"tampered", "exact", "exact", "mismatch"},
		{"expired", "chunked", "missing", "mismatch"},
		// Not part of the pairwise set: the one combination that must succeed,
		// and its case-and-parameter variant of the media type.
		{"valid", "exact", "exact", "match"},
		{"valid", "exact", "case_and_params", "match"},
	}

	const signedType = "image/png"
	for _, row := range rows {
		t.Run(fmt.Sprintf("sig=%s/length=%s/ctype=%s/digest=%s", row.sig, row.length, row.ctype, row.digest), func(t *testing.T) {
			t.Parallel()
			key := relayKey(t)
			declared := randomBytes(t, 2048)
			sum := sha256.Sum256(declared)
			ttl := 5 * time.Minute
			if row.sig == "expired" {
				ttl = -time.Minute
			}
			target, err := testStorage.Flow.SignedPutURL(key, signedType, int64(len(declared)), hex.EncodeToString(sum[:]), ttl)
			require.NoError(t, err)
			if row.sig == "tampered" {
				target = tamperSig(target)
			}

			body := declared
			if row.digest == "mismatch" {
				body = flipLastByte(declared)
			}
			length := int64(len(body))
			switch row.length {
			case "smaller":
				body = body[:len(body)-1]
				length = int64(len(body))
			case "larger":
				body = append(append([]byte(nil), body...), 'x')
				length = int64(len(body))
			case "chunked":
				length = -1
			}

			contentType := map[string]string{
				"exact":           signedType,
				"case_and_params": "IMAGE/PNG; charset=binary",
				"other":           "text/html",
				"missing":         "",
			}[row.ctype]

			want := http.StatusOK
			switch {
			case row.sig != "valid":
				want = http.StatusForbidden
			case row.length == "larger":
				want = http.StatusRequestEntityTooLarge
			case row.length != "exact":
				want = http.StatusBadRequest
			case row.ctype == "other" || row.ctype == "missing":
				want = http.StatusUnsupportedMediaType
			case row.digest == "mismatch":
				want = http.StatusBadRequest
			}

			status, resp := relayPut(t, target, contentType, body, length)
			require.Equalf(t, want, status, "body=%s", resp)

			if want != http.StatusOK {
				testStorage.MustNotExist(t, key)
				return
			}
			testStorage.MustExist(t, key)
			info, err := testStorage.Raw.StatObject(t.Context(), testStorage.Bucket, key, minio.StatObjectOptions{})
			require.NoError(t, err)
			require.Equal(t, signedType, info.ContentType, "stored as the signed type, not the request's spelling of it")
			require.Equal(t, int64(len(declared)), info.Size)
			obj, found, err := testStorage.Flow.Open(t.Context(), key)
			require.NoError(t, err)
			require.True(t, found)
			defer func() { _ = obj.Close() }()
			got, err := io.ReadAll(obj)
			require.NoError(t, err)
			require.Equal(t, declared, got)
		})
	}
}

// A signature minted for one key must not write another, and a download grant
// must not write at all.
func TestBlobRelayPutSignatureIsBoundToKeyAndMethod(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	keyA, keyB := relayKey(t), relayKey(t)
	payload := randomBytes(t, 512)
	sum := sha256.Sum256(payload)
	put, err := testStorage.Flow.SignedPutURL(keyA, "application/pdf", int64(len(payload)), hex.EncodeToString(sum[:]), time.Minute)
	require.NoError(t, err)

	moved := strings.Replace(put, "/blobs/"+keyA, "/blobs/"+keyB, 1)
	require.NotEqual(t, put, moved)
	status, body := relayPut(t, moved, "application/pdf", payload, int64(len(payload)))
	require.Equalf(t, http.StatusForbidden, status, "body=%s", body)
	testStorage.MustNotExist(t, keyB)

	dl, err := testStorage.Flow.SignedDownloadURL(keyA, "x.pdf", time.Minute)
	require.NoError(t, err)
	status, body = relayPut(t, dl, "application/pdf", payload, int64(len(payload)))
	require.Equalf(t, http.StatusForbidden, status, "body=%s", body)
	testStorage.MustNotExist(t, keyA)
}

func TestBlobRelayGet(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	key := relayKey(t)
	payload := randomBytes(t, 1024)
	// Stored as a type a browser would render and run if it were served as itself.
	require.NoError(t, testStorage.PutBytes(t.Context(), key, payload, "text/html"))
	name := "日本語 レポート.html"

	signed, err := testStorage.Flow.SignedDownloadURL(key, name, 5*time.Minute)
	require.NoError(t, err)
	get := func(t *testing.T, method, target, rng string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, target, nil)
		require.NoError(t, err)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp, raw
	}

	t.Run("full body is an opaque attachment", func(t *testing.T) {
		t.Parallel()
		resp, raw := get(t, http.MethodGet, signed, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, payload, raw)
		require.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
		require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		require.Equal(t, flowstorage.ContentDispositionAttachment(name), resp.Header.Get("Content-Disposition"))
		require.True(t, strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment;"))
		require.Contains(t, resp.Header.Get("Cache-Control"), "private")
	})

	t.Run("range request is answered with 206", func(t *testing.T) {
		t.Parallel()
		resp, raw := get(t, http.MethodGet, signed, "bytes=10-19")
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		require.Equal(t, "bytes 10-19/1024", resp.Header.Get("Content-Range"))
		require.Equal(t, payload[10:20], raw)
		require.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
		require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		require.True(t, strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment;"))

		resp, raw = get(t, http.MethodGet, signed, "bytes=-16")
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		require.Equal(t, payload[1008:], raw)
	})

	t.Run("unsatisfiable range is 416", func(t *testing.T) {
		t.Parallel()
		resp, _ := get(t, http.MethodGet, signed, "bytes=5000-6000")
		require.Equal(t, http.StatusRequestedRangeNotSatisfiable, resp.StatusCode)
	})

	t.Run("head carries the headers and no body", func(t *testing.T) {
		t.Parallel()
		resp, raw := get(t, http.MethodHead, signed, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Empty(t, raw)
		require.Equal(t, "1024", resp.Header.Get("Content-Length"))
		require.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
		require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	})

	t.Run("a different filename needs a different signature", func(t *testing.T) {
		t.Parallel()
		u, err := url.Parse(signed)
		require.NoError(t, err)
		q := u.Query()
		q.Set("fn", "evil.html")
		u.RawQuery = q.Encode()
		resp, _ := get(t, http.MethodGet, u.String(), "")
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("expired and tampered are the same refusal", func(t *testing.T) {
		t.Parallel()
		expired, err := testStorage.Flow.SignedDownloadURL(key, name, -time.Minute)
		require.NoError(t, err)
		r1, b1 := get(t, http.MethodGet, expired, "")
		r2, b2 := get(t, http.MethodGet, tamperSig(signed), "")
		require.Equal(t, http.StatusForbidden, r1.StatusCode)
		require.Equal(t, http.StatusForbidden, r2.StatusCode)
		require.Equal(t, b1, b2)
	})

	t.Run("missing object is 404 behind a good signature", func(t *testing.T) {
		t.Parallel()
		u, err := testStorage.Flow.SignedDownloadURL(relayKey(t), "x", time.Minute)
		require.NoError(t, err)
		resp, _ := get(t, http.MethodGet, u, "")
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

// TestBlobRelayInlineImage covers the inline (preview) form of a GET URL:
// allowlisted raster types come back as themselves with the sandbox headers,
// anything else cannot be minted, and the inline mark cannot be added or
// removed after signing.
func TestBlobRelayInlineImage(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	const sandboxCSP = "default-src 'none'; sandbox; frame-ancestors 'none'"
	get := func(t *testing.T, method, target, rng string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, target, nil)
		require.NoError(t, err)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp, raw
	}
	requireInlineHeaders := func(t *testing.T, resp *http.Response, wantType, name string) {
		t.Helper()
		require.Equal(t, wantType, resp.Header.Get("Content-Type"))
		require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		require.Equal(t, sandboxCSP, resp.Header.Get("Content-Security-Policy"))
		require.Equal(t, flowstorage.ContentDispositionInline(name), resp.Header.Get("Content-Disposition"))
		require.True(t, strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline;"))
		require.Contains(t, resp.Header.Get("Cache-Control"), "private")
	}

	t.Run("each allowlisted type is served as itself", func(t *testing.T) {
		t.Parallel()
		cases := []struct{ stored, served string }{
			{"image/png", "image/png"},
			{"image/jpeg", "image/jpeg"},
			{"image/gif", "image/gif"},
			{"image/webp", "image/webp"},
			{"image/avif", "image/avif"},
			{"IMAGE/PNG; charset=binary", "image/png"},
			{" image/png ", "image/png"},
			{"image/jpg", "image/jpeg"},
			{"image/pjpeg", "image/jpeg"},
			{"image/x-png", "image/png"},
		}
		for _, c := range cases {
			t.Run(c.stored, func(t *testing.T) {
				t.Parallel()
				key := relayKey(t)
				payload := randomBytes(t, 256)
				// The object metadata is irrelevant: the served type comes from the signed grant.
				require.NoError(t, testStorage.PutBytes(t.Context(), key, payload, "application/octet-stream"))
				target, expires, err := testStorage.Flow.SignedPreviewURL(key, "pic.img", c.stored)
				require.NoError(t, err)
				require.True(t, expires.After(time.Now().Add(10*time.Minute)), "valid for at least 15 minutes")
				require.True(t, expires.Before(time.Now().Add(31*time.Minute)), "valid for at most 30 minutes")

				resp, raw := get(t, http.MethodGet, target, "")
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, payload, raw)
				requireInlineHeaders(t, resp, c.served, "pic.img")
			})
		}
	})

	t.Run("html bytes stored as image/png are still served as image/png with the sandbox", func(t *testing.T) {
		t.Parallel()
		key := relayKey(t)
		payload := []byte("<!doctype html><title>x</title><script>document.title='pwned'</script>")
		require.NoError(t, testStorage.PutBytes(t.Context(), key, payload, "image/png"))
		target, _, err := testStorage.Flow.SignedPreviewURL(key, "page.png", "image/png")
		require.NoError(t, err)

		resp, raw := get(t, http.MethodGet, target, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, payload, raw)
		requireInlineHeaders(t, resp, "image/png", "page.png")
	})

	t.Run("range and head carry the inline headers", func(t *testing.T) {
		t.Parallel()
		key := relayKey(t)
		payload := randomBytes(t, 1024)
		require.NoError(t, testStorage.PutBytes(t.Context(), key, payload, "image/webp"))
		target, _, err := testStorage.Flow.SignedPreviewURL(key, "r.webp", "image/webp")
		require.NoError(t, err)

		resp, raw := get(t, http.MethodGet, target, "bytes=10-19")
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		require.Equal(t, "bytes 10-19/1024", resp.Header.Get("Content-Range"))
		require.Equal(t, payload[10:20], raw)
		requireInlineHeaders(t, resp, "image/webp", "r.webp")

		resp, raw = get(t, http.MethodHead, target, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Empty(t, raw)
		require.Equal(t, "1024", resp.Header.Get("Content-Length"))
		requireInlineHeaders(t, resp, "image/webp", "r.webp")

		resp, _ = get(t, http.MethodGet, target, "bytes=5000-6000")
		require.Equal(t, http.StatusRequestedRangeNotSatisfiable, resp.StatusCode)
		require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		require.Equal(t, sandboxCSP, resp.Header.Get("Content-Security-Policy"))
	})

	t.Run("japanese filename uses the shared disposition encoding", func(t *testing.T) {
		t.Parallel()
		key := relayKey(t)
		require.NoError(t, testStorage.PutBytes(t.Context(), key, randomBytes(t, 64), "image/png"))
		name := "日本語 \"写真\".png"
		target, _, err := testStorage.Flow.SignedPreviewURL(key, name, "image/png")
		require.NoError(t, err)

		resp, _ := get(t, http.MethodGet, target, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		requireInlineHeaders(t, resp, "image/png", name)
		require.Contains(t, resp.Header.Get("Content-Disposition"), "filename*=UTF-8''")
	})

	t.Run("a type outside the allowlist cannot be minted", func(t *testing.T) {
		t.Parallel()
		key := relayKey(t)
		for _, ct := range []string{"image/svg+xml", "application/pdf", "text/html", "application/octet-stream", "image/png, text/html", ""} {
			_, _, err := testStorage.Flow.SignedPreviewURL(key, "x", ct)
			require.ErrorIsf(t, err, flowstorage.ErrNotPreviewable, "content type %q", ct)
		}
	})

	t.Run("the inline mark and the type are bound by the signature", func(t *testing.T) {
		t.Parallel()
		key := relayKey(t)
		require.NoError(t, testStorage.PutBytes(t.Context(), key, randomBytes(t, 64), "image/png"))
		inline, _, err := testStorage.Flow.SignedPreviewURL(key, "p.png", "image/png")
		require.NoError(t, err)
		attach, err := testStorage.Flow.SignedDownloadURL(key, "p.png", 5*time.Minute)
		require.NoError(t, err)

		resp, _ := get(t, http.MethodGet, inline, "")
		require.Equal(t, http.StatusOK, resp.StatusCode, "control: the untouched inline URL works")
		resp, _ = get(t, http.MethodGet, attach, "")
		require.Equal(t, http.StatusOK, resp.StatusCode, "control: the untouched download URL works")
		require.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
		require.True(t, strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment;"))

		mutate := func(raw string, fn func(q url.Values)) string {
			u, err := url.Parse(raw)
			require.NoError(t, err)
			q := u.Query()
			fn(q)
			u.RawQuery = q.Encode()
			return u.String()
		}
		for name, target := range map[string]string{
			"inl removed":                 mutate(inline, func(q url.Values) { q.Del("inl") }),
			"inl added to a download URL": mutate(attach, func(q url.Values) { q.Set("inl", "1") }),
			"ct tampered":                 mutate(inline, func(q url.Values) { q.Set("ct", "text/html") }),
			"ct emptied":                  mutate(inline, func(q url.Values) { q.Del("ct") }),
			"inl not 1":                   mutate(inline, func(q url.Values) { q.Set("inl", "true") }),
			"inl duplicated":              inline + "&inl=1",
			"ct added to a download URL":  mutate(attach, func(q url.Values) { q.Set("ct", "image/png") }),
		} {
			resp, _ := get(t, http.MethodGet, target, "")
			require.Equalf(t, http.StatusForbidden, resp.StatusCode, "%s", name)
		}
	})

	t.Run("an inline mark cannot be used to write", func(t *testing.T) {
		t.Parallel()
		key := relayKey(t)
		payload := randomBytes(t, 128)
		sum := sha256.Sum256(payload)
		put, err := testStorage.Flow.SignedPutURL(key, "image/png", int64(len(payload)), hex.EncodeToString(sum[:]), time.Minute)
		require.NoError(t, err)
		status, body := relayPut(t, put+"&inl=1", "image/png", payload, int64(len(payload)))
		require.Equalf(t, http.StatusForbidden, status, "body=%s", body)
		testStorage.MustNotExist(t, key)
	})
}
