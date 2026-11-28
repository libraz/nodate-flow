package storage

import (
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizePreviewableType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"image/png", "image/png", true},
		{"image/jpeg", "image/jpeg", true},
		{"image/gif", "image/gif", true},
		{"image/webp", "image/webp", true},
		{"image/avif", "image/avif", true},
		{"image/png; charset=binary", "image/png", true},
		{"IMAGE/PNG", "image/png", true},
		{" image/png ", "image/png", true},
		{"image/png;", "image/png", true},
		{"image/png ; charset=binary", "image/png", true},
		{"image/jpg", "image/jpeg", true},
		{"image/pjpeg", "image/jpeg", true},
		{"image/x-png", "image/png", true},
		{"image/png, text/html", "", false},
		{"image/svg+xml", "", false},
		{"image/bmp", "", false},
		{"image/heic", "", false},
		{"application/pdf", "", false},
		{"text/html", "", false},
		{"application/octet-stream", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := NormalizePreviewableType(tc.in)
		require.Equalf(t, tc.ok, ok, "%q", tc.in)
		require.Equalf(t, tc.want, got, "%q", tc.in)
		require.Equalf(t, tc.ok, IsPreviewableImage(tc.in), "%q", tc.in)
	}
}

func TestPreviewURLRoundsExpiryToWindow(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	const window = 15 * 60
	start := int64(1_800_000_000) // a multiple of the window
	require.Zero(t, start%window)

	expOf := func(now int64) (string, int64, time.Time) {
		raw, expiresAt, err := s.PreviewURL(testKey, "a.png", "image/png", time.Unix(now, 0))
		require.NoError(t, err)
		u, err := url.Parse(raw)
		require.NoError(t, err)
		exp, err := strconv.ParseInt(u.Query().Get("exp"), 10, 64)
		require.NoError(t, err)
		return raw, exp, expiresAt
	}

	first, exp, at := expOf(start)
	require.Equal(t, start+2*window, exp)
	require.Equal(t, exp, at.Unix())

	last, exp, _ := expOf(start + window - 1)
	require.Equal(t, start+2*window, exp)
	require.Equal(t, first, last, "one window mints one URL")

	next, exp, _ := expOf(start + window)
	require.Equal(t, start+3*window, exp)
	require.NotEqual(t, first, next)
}

func TestPreviewURLRefusesWhatCannotBePreviewed(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	for _, ct := range []string{"image/svg+xml", "text/html", "application/pdf", "image/png, text/html", ""} {
		_, _, err := s.PreviewURL(testKey, "a", ct, time.Now())
		require.ErrorIsf(t, err, ErrNotPreviewable, "%q", ct)
	}
	_, _, err := NewSigner("", []byte("k")).PreviewURL(testKey, "a", "image/png", time.Now())
	require.ErrorIs(t, err, ErrSignerUnconfigured)
}

// The values below were taken from the signer before inline URLs existed. A
// plain download URL must keep matching them or every URL already handed out
// stops verifying.
func TestPlainDownloadSignatureIsUnchanged(t *testing.T) {
	t.Parallel()
	s := NewSigner("https://flow.example/api", []byte("baseline-signing-key-0123456789abcdef"))
	p := params{exp: 1800000000, Grant: Grant{Filename: "写真 1.png"}}

	require.Equal(t,
		"e5306386ee02f7fe59026685f7e3240a2089f914d75bb5452a12db456d2bf518",
		s.sign("GET", "ws/1/ab/cdef", p))

	got, err := s.mint("GET", "ws/1/ab/cdef", p)
	require.NoError(t, err)
	require.Equal(t,
		"https://flow.example/api/blobs/ws/1/ab/cdef?exp=1800000000&fn=%E5%86%99%E7%9C%9F+1.png&sig=e5306386ee02f7fe59026685f7e3240a2089f914d75bb5452a12db456d2bf518",
		got)
}

func TestInlineAndPlainSignaturesDoNotCollide(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	const exp = 1800000000
	// The 8-field inline canonical for fn "a" ends "...1:a6:inline"; a plain
	// 7-field one whose filename ends in "6:inline" must not reproduce it.
	inline := s.sign("GET", testKey, params{exp: exp, Grant: Grant{Filename: "a", Inline: true}})
	for _, fn := range []string{"a6:inline", "1:a6:inline", "6:inline", "a"} {
		plain := s.sign("GET", testKey, params{exp: exp, Grant: Grant{Filename: fn}})
		require.NotEqualf(t, inline, plain, "fn %q", fn)
	}
}

func TestVerifyAcceptsInlinePreview(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	now := time.Unix(1_800_000_000, 0)
	raw, expiresAt, err := s.PreviewURL(testKey, "写真 1.png", "image/jpg", now)
	require.NoError(t, err)
	key, q := split(t, raw)
	require.Equal(t, "image/jpeg", q.Get("ct"))
	require.Equal(t, "1", q.Get("inl"))

	grant, err := s.Verify("GET", key, q, now)
	require.NoError(t, err)
	require.True(t, grant.Inline)
	require.Equal(t, "image/jpeg", grant.ContentType)
	require.Equal(t, "写真 1.png", grant.Filename)
	require.True(t, grant.ExpiresAt.Equal(expiresAt))

	_, err = s.Verify("GET", key, q, expiresAt)
	require.ErrorIs(t, err, ErrURLExpired)
}

func TestVerifyRejectsMalformedInline(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	now := time.Unix(1_800_000_000, 0)
	raw, _, err := s.PreviewURL(testKey, "a.png", "image/png", now)
	require.NoError(t, err)
	key, q := split(t, raw)

	cases := map[string]func(q url.Values){
		"inl 0":         func(q url.Values) { q.Set("inl", "0") },
		"inl true":      func(q url.Values) { q.Set("inl", "true") },
		"inl empty":     func(q url.Values) { q.Set("inl", "") },
		"inl repeated":  func(q url.Values) { q["inl"] = []string{"1", "1"} },
		"inl removed":   func(q url.Values) { q.Del("inl") },
		"ct changed":    func(q url.Values) { q.Set("ct", "image/gif") },
		"ct removed":    func(q url.Values) { q.Del("ct") },
		"ct to html":    func(q url.Values) { q.Set("ct", "text/html") },
		"filename edit": func(q url.Values) { q.Set("fn", "b.png") },
	}
	for name, edit := range cases {
		q := clone(q)
		edit(q)
		_, err := s.Verify("GET", key, q, now)
		require.ErrorIsf(t, err, ErrURLInvalid, name)
	}

	t.Run("inl added to a plain download", func(t *testing.T) {
		t.Parallel()
		dl, err := s.DownloadURL(testKey, "a.png", testExpiry)
		require.NoError(t, err)
		dkey, dq := split(t, dl)
		dq.Set("inl", "1")
		_, err = s.Verify("GET", dkey, dq, time.Now())
		require.ErrorIs(t, err, ErrURLInvalid)
	})

	t.Run("inline grant is not a write grant", func(t *testing.T) {
		t.Parallel()
		_, err := s.Verify("PUT", key, q, now)
		require.ErrorIs(t, err, ErrURLInvalid)
	})
}

// A signature the server itself produced is still refused when it asks for
// something the signer never issues.
func TestVerifyRefusesCorrectlySignedUnissuedShapes(t *testing.T) {
	t.Parallel()
	s := newTestSigner()
	exp := time.Now().Add(time.Hour).Unix()

	cases := []struct {
		name   string
		method string
		grant  Grant
	}{
		{"inline svg", "GET", Grant{ContentType: "image/svg+xml", Filename: "a.svg", Inline: true}},
		{"inline html", "GET", Grant{ContentType: "text/html", Filename: "a.html", Inline: true}},
		{"inline alias not normalized", "GET", Grant{ContentType: "image/jpg", Filename: "a.jpg", Inline: true}},
		{"inline mixed case", "GET", Grant{ContentType: "IMAGE/PNG", Filename: "a.png", Inline: true}},
		{"inline with parameters", "GET", Grant{ContentType: "image/png; charset=binary", Filename: "a.png", Inline: true}},
		{"inline without a type", "GET", Grant{Filename: "a.png", Inline: true}},
		{"plain read carrying a type", "GET", Grant{ContentType: "image/png", Filename: "a.png"}},
		{"write carrying inline", "PUT", Grant{ContentType: "image/png", ByteSize: 10, Sha256Hex: testSha, Inline: true}},
	}
	for _, tc := range cases {
		raw, err := s.mint(tc.method, testKey, params{exp: exp, Grant: tc.grant})
		require.NoError(t, err)
		key, q := split(t, raw)
		_, err = s.Verify(tc.method, key, q, time.Now())
		require.ErrorIsf(t, err, ErrURLInvalid, tc.name)
	}

	// Control: the same shape with an allowlisted exact type is accepted.
	raw, err := s.mint("GET", testKey, params{exp: exp, Grant: Grant{ContentType: "image/png", Filename: "a.png", Inline: true}})
	require.NoError(t, err)
	key, q := split(t, raw)
	_, err = s.Verify("GET", key, q, time.Now())
	require.NoError(t, err)
}
