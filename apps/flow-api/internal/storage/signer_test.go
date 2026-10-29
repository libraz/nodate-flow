package storage

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testBase   = "https://flow.example.test"
	testSha    = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testKey    = "workspace/0a1b2c3d/" + testSha
	testCT     = "image/png"
	testSize   = int64(4096)
	testExpiry = 10 * time.Minute
)

func newTestSigner() *Signer { return NewSigner(testBase, []byte("unit-test-signing-key")) }

// split parses a minted URL into the object key the relay would see and the
// query it would verify.
func split(t *testing.T, raw string) (string, url.Values) {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	key, ok := strings.CutPrefix(u.Path, BlobPath+"/")
	require.True(t, ok, "path %q is not under %s", u.Path, BlobPath)
	return key, u.Query()
}

func mintPut(t *testing.T, s *Signer) (string, url.Values) {
	t.Helper()
	raw, err := s.PutURL(testKey, testCT, testSize, testSha, testExpiry)
	require.NoError(t, err)
	return split(t, raw)
}

func clone(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func TestSignerPutRoundTrip(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	raw, err := s.PutURL(testKey, testCT, testSize, testSha, testExpiry)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(raw, testBase+BlobPath+"/workspace/0a1b2c3d/"), raw)

	key, q := split(t, raw)
	require.Equal(t, testKey, key)

	grant, err := s.Verify("PUT", key, q, time.Now())
	require.NoError(t, err)
	require.Equal(t, testCT, grant.ContentType)
	require.Equal(t, testSize, grant.ByteSize)
	require.Equal(t, testSha, grant.Sha256Hex)
	require.Empty(t, grant.Filename)
	require.WithinDuration(t, time.Now().Add(testExpiry), grant.ExpiresAt, 5*time.Second)
}

func TestSignerDownloadRoundTrip(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	name := "日本語 report & \"final\".png"
	raw, err := s.DownloadURL(testKey, name, testExpiry)
	require.NoError(t, err)

	key, q := split(t, raw)
	grant, err := s.Verify("GET", key, q, time.Now())
	require.NoError(t, err)
	require.Equal(t, name, grant.Filename)
	require.Empty(t, grant.ContentType)
	require.Zero(t, grant.ByteSize)
	require.Empty(t, grant.Sha256Hex)
}

func TestSignerKeyWithReservedCharactersRoundTrips(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	key := "user/a b/ü?#%2F&x=y"
	raw, err := s.DownloadURL(key, "f", testExpiry)
	require.NoError(t, err)

	u, err := url.Parse(raw)
	require.NoError(t, err)
	// The query must not have absorbed any part of the key.
	require.Equal(t, []string{"exp", "fn", "sig"}, sortedKeys(u.Query()))
	got, ok := strings.CutPrefix(u.Path, BlobPath+"/")
	require.True(t, ok)
	require.Equal(t, key, got)
	_, err = s.Verify("GET", got, u.Query(), time.Now())
	require.NoError(t, err)
}

func sortedKeys(q url.Values) []string {
	out := make([]string, 0, len(q))
	for k := range q {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestSignerRejectsTamperedPutFields(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	otherSha := strings.Repeat("f", 64)

	cases := []struct {
		name   string
		method string
		key    string
		edit   func(q url.Values)
	}{
		{"key", "PUT", testKey + "x", func(_ url.Values) {}},
		{"key sibling workspace", "PUT", "workspace/ffffffff/" + testSha, func(_ url.Values) {}},
		{"method GET", "GET", testKey, func(_ url.Values) {}},
		{"method POST", "POST", testKey, func(_ url.Values) {}},
		{"exp extended", "PUT", testKey, func(q url.Values) { q.Set("exp", "99999999999") }},
		{"exp unparsable", "PUT", testKey, func(q url.Values) { q.Set("exp", "soon") }},
		{"exp removed", "PUT", testKey, func(q url.Values) { q.Del("exp") }},
		{"ct changed", "PUT", testKey, func(q url.Values) { q.Set("ct", "text/html") }},
		{"ct case changed", "PUT", testKey, func(q url.Values) { q.Set("ct", "IMAGE/PNG") }},
		{"ct removed", "PUT", testKey, func(q url.Values) { q.Del("ct") }},
		{"sz enlarged", "PUT", testKey, func(q url.Values) { q.Set("sz", "4097") }},
		{"sz shrunk", "PUT", testKey, func(q url.Values) { q.Set("sz", "4095") }},
		{"sz unparsable", "PUT", testKey, func(q url.Values) { q.Set("sz", "big") }},
		{"sz removed", "PUT", testKey, func(q url.Values) { q.Del("sz") }},
		{"sha changed", "PUT", testKey, func(q url.Values) { q.Set("sha", otherSha) }},
		{"sha removed", "PUT", testKey, func(q url.Values) { q.Del("sha") }},
		{"fn added", "PUT", testKey, func(q url.Values) { q.Set("fn", "evil.html") }},
		{"sig altered", "PUT", testKey, func(q url.Values) {
			sig := q.Get("sig")
			last := byte('0')
			if sig[len(sig)-1] == '0' {
				last = '1'
			}
			q.Set("sig", sig[:len(sig)-1]+string(last))
		}},
		{"sig truncated", "PUT", testKey, func(q url.Values) { q.Set("sig", q.Get("sig")[:10]) }},
		{"sig removed", "PUT", testKey, func(q url.Values) { q.Del("sig") }},
		{"sig empty", "PUT", testKey, func(q url.Values) { q.Set("sig", "") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, q := mintPut(t, s)
			q = clone(q)
			tc.edit(q)
			_, err := s.Verify(tc.method, tc.key, q, time.Now())
			require.ErrorIs(t, err, ErrURLInvalid)
		})
	}
}

func TestSignerRejectsTamperedDownloadFields(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	raw, err := s.DownloadURL(testKey, "report.pdf", testExpiry)
	require.NoError(t, err)
	key, q := split(t, raw)

	t.Run("filename changed", func(t *testing.T) {
		t.Parallel()
		q := clone(q)
		q.Set("fn", "report.html")
		_, err := s.Verify("GET", key, q, time.Now())
		require.ErrorIs(t, err, ErrURLInvalid)
	})
	t.Run("filename removed", func(t *testing.T) {
		t.Parallel()
		q := clone(q)
		q.Del("fn")
		_, err := s.Verify("GET", key, q, time.Now())
		require.ErrorIs(t, err, ErrURLInvalid)
	})
	t.Run("turned into a write", func(t *testing.T) {
		t.Parallel()
		_, err := s.Verify("PUT", key, q, time.Now())
		require.ErrorIs(t, err, ErrURLInvalid)
	})
	t.Run("write grant added", func(t *testing.T) {
		t.Parallel()
		q := clone(q)
		q.Set("sz", "1")
		q.Set("sha", testSha)
		q.Set("ct", testCT)
		_, err := s.Verify("GET", key, q, time.Now())
		require.ErrorIs(t, err, ErrURLInvalid)
	})
}

func TestSignerRejectsAnotherKey(t *testing.T) {
	t.Parallel()
	_, q := mintPut(t, newTestSigner())
	other := NewSigner(testBase, []byte("a-different-signing-key"))
	_, err := other.Verify("PUT", testKey, q, time.Now())
	require.ErrorIs(t, err, ErrURLInvalid)
}

func TestSignerExpiry(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	_, q := mintPut(t, s)
	grant, err := s.Verify("PUT", testKey, q, time.Now())
	require.NoError(t, err)
	exp := grant.ExpiresAt

	_, err = s.Verify("PUT", testKey, q, exp.Add(-time.Second))
	require.NoError(t, err, "one second before the expiry is still valid")

	_, err = s.Verify("PUT", testKey, q, exp)
	require.ErrorIs(t, err, ErrURLExpired, "the expiry instant itself is already expired")

	_, err = s.Verify("PUT", testKey, q, exp.Add(time.Hour))
	require.ErrorIs(t, err, ErrURLExpired)

	// An URL minted already expired is expired, not invalid: the signature
	// is good.
	raw, err := s.DownloadURL(testKey, "f", -time.Minute)
	require.NoError(t, err)
	key, dq := split(t, raw)
	_, err = s.Verify("GET", key, dq, time.Now())
	require.True(t, errors.Is(err, ErrURLExpired), "got %v", err)
}

func TestSignerTamperedAndExpiredAreCheckedSignatureFirst(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	_, q := mintPut(t, s)
	q = clone(q)
	q.Set("sz", "1")
	// Both wrong: the caller must be told only that the signature is bad.
	_, err := s.Verify("PUT", testKey, q, time.Now().Add(24*time.Hour))
	require.ErrorIs(t, err, ErrURLInvalid)
	require.NotErrorIs(t, err, ErrURLExpired)
}

func TestSignerPreservesBasePathPrefix(t *testing.T) {
	t.Parallel()
	s := NewSigner("https://example.test/flow/api///", []byte("k"))
	raw, err := s.DownloadURL("a/b", "f", testExpiry)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(raw, "https://example.test/flow/api"+BlobPath+"/a/b?"), raw)

	s.SetBase("http://127.0.0.1:9999/")
	raw, err = s.DownloadURL("a/b", "f", testExpiry)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(raw, "http://127.0.0.1:9999"+BlobPath+"/a/b?"), raw)

	// The base is not signed: the same grant verifies wherever it is served.
	key, q := split(t, raw)
	_, err = s.Verify("GET", key, q, time.Now())
	require.NoError(t, err)
}

func TestSignerPutURLRefusesUnboundWrites(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	upper := strings.ToUpper(testSha)

	cases := []struct {
		name string
		size int64
		sha  string
	}{
		{"zero size", 0, testSha},
		{"negative size", -1, testSha},
		{"empty sha", testSize, ""},
		{"short sha", testSize, testSha[:63]},
		{"long sha", testSize, testSha + "0"},
		{"uppercase sha", testSize, upper},
		{"non hex sha", testSize, strings.Repeat("g", 64)},
		{"sha with separator", testSize, testSha[:63] + "&"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := s.PutURL(testKey, testCT, tc.size, tc.sha, testExpiry)
			require.Error(t, err)
			require.Empty(t, raw)
		})
	}
}

func TestSignerUnconfigured(t *testing.T) {
	t.Parallel()
	s := NewSigner("", []byte("k"))
	_, err := s.PutURL(testKey, testCT, testSize, testSha, testExpiry)
	require.ErrorIs(t, err, ErrSignerUnconfigured)
	_, err = s.DownloadURL(testKey, "f", testExpiry)
	require.ErrorIs(t, err, ErrSignerUnconfigured)

	var c Client
	_, err = c.SignedPutURL(testKey, testCT, testSize, testSha, testExpiry)
	require.ErrorIs(t, err, ErrSignerUnconfigured)
	_, err = c.SignedDownloadURL(testKey, "f", testExpiry)
	require.ErrorIs(t, err, ErrSignerUnconfigured)
}

func TestCanonicalIsInjective(t *testing.T) {
	t.Parallel()
	pairs := [][2][]string{
		{{"ab", "c"}, {"a", "bc"}},
		{{"a", ""}, {"", "a"}},
		{{"1:a", "b"}, {"1", "a", "b"}},
		{{"a|b", "c"}, {"a", "b|c"}},
		{{"", ""}, {""}},
		{{"3:abc"}, {"abc"}},
		{{"a\n", "b"}, {"a", "\nb"}},
	}
	for _, p := range pairs {
		require.NotEqual(t, canonical(p[0]...), canonical(p[1]...), "%q vs %q", p[0], p[1])
	}
}

// A filename holding the delimiters and length prefixes of the fields before it
// must not let a caller move a signed value into a neighbouring field.
func TestSignerFieldValuesCannotShiftBetweenFields(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	_, q := mintPut(t, s)

	shifts := []struct {
		name string
		edit func(q url.Values)
	}{
		{"ct moved into fn", func(q url.Values) { q.Set("fn", q.Get("ct")); q.Del("ct") }},
		{"sha moved into fn", func(q url.Values) { q.Set("fn", q.Get("sha")); q.Del("sha") }},
		{"ct and sz fused into ct", func(q url.Values) {
			q.Set("ct", q.Get("ct")+"|"+q.Get("sz"))
			q.Del("sz")
		}},
		{"sz and sha fused into fn", func(q url.Values) {
			q.Set("fn", q.Get("sz")+q.Get("sha"))
			q.Del("sz")
			q.Del("sha")
		}},
		{"canonical framing inside ct", func(q url.Values) {
			q.Set("ct", q.Get("ct")+"4:4096")
			q.Del("sz")
		}},
	}
	for _, sh := range shifts {
		t.Run(sh.name, func(t *testing.T) {
			t.Parallel()
			q := clone(q)
			sh.edit(q)
			_, err := s.Verify("PUT", testKey, q, time.Now())
			require.ErrorIs(t, err, ErrURLInvalid)
		})
	}

	// And a filename that imitates the framing of every field is still just a
	// filename: it round-trips as itself and verifies only as signed.
	hostile := `1:x` + "0:" + `3:PUT` + "|" + "\n" + `fn`
	raw, err := s.DownloadURL(testKey, hostile, testExpiry)
	require.NoError(t, err)
	key, dq := split(t, raw)
	grant, err := s.Verify("GET", key, dq, time.Now())
	require.NoError(t, err)
	require.Equal(t, hostile, grant.Filename)
	dq = clone(dq)
	dq.Set("fn", hostile+"0:")
	_, err = s.Verify("GET", key, dq, time.Now())
	require.ErrorIs(t, err, ErrURLInvalid)
}
