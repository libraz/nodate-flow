package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Errors a relayed request can be turned away with. They are distinguished for
// the log, not for the caller: both answer 403, because telling an unsigned
// caller which one it was tells it whether it guessed a real key.
var (
	// ErrURLInvalid means the signature did not cover the request as it arrived.
	ErrURLInvalid = errors.New("blob url signature is invalid")
	// ErrURLExpired means the signature was good but the URL has aged out.
	ErrURLExpired = errors.New("blob url has expired")
	// ErrSignerUnconfigured means no signer or public base URL was given, so
	// there is no address a browser could be sent to.
	ErrSignerUnconfigured = errors.New("blob url base is not configured")
)

// BlobPath is where the relay is mounted. The signer builds it into every URL
// it mints, so the route and the URLs cannot drift apart.
const BlobPath = "/blobs"

// Signer mints and checks the short-lived URLs a browser uses to read and
// write blobs.
//
// Object storage has no route from the internet: the bytes are relayed by this
// API. What the browser gets is a URL back to this API carrying its own
// authorization, an HMAC over the method, object key and expiry and, for a
// write, the exact size, media type and SHA-256 the object must have. The
// authorization travels in the URL because neither caller can send a header: an
// attachment link is a plain navigation and the upload is a bare fetch PUT.
type Signer struct {
	base string
	key  []byte
}

// NewSigner returns a signer that mints URLs under base, the public origin of
// this API (NF_FLOW_PUBLIC_URL), and signs them with key. A trailing slash is
// trimmed and any path prefix is preserved.
func NewSigner(base string, key []byte) *Signer {
	return &Signer{base: strings.TrimRight(base, "/"), key: key}
}

// SetBase points the signer at a different public origin. It exists for tests,
// which cannot know the address their server will be given until it is
// listening.
func (s *Signer) SetBase(base string) {
	s.base = strings.TrimRight(base, "/")
}

// Grant is what a verified blob URL permits.
type Grant struct {
	// ContentType is the media type a write must declare and is stored as.
	// Empty on a read.
	ContentType string
	// ByteSize is the exact length a write must have. Zero on a read.
	ByteSize int64
	// Sha256Hex is the digest a write's bytes must have. Empty on a read.
	Sha256Hex string
	// Filename is the name a downloaded object is saved under.
	Filename string
	// ExpiresAt is when the URL stops being accepted. Set by Verify and unused
	// when minting.
	ExpiresAt time.Time
}

// params is everything a signature covers besides the method and the key.
type params struct {
	exp int64
	Grant
}

// DownloadURL returns a URL that reads the object as an attachment named
// filename.
func (s *Signer) DownloadURL(key, filename string, expires time.Duration) (string, error) {
	return s.mint("GET", key, params{
		exp:   time.Now().Add(expires).Unix(),
		Grant: Grant{Filename: filename},
	})
}

// PutURL returns a URL that writes exactly byteSize bytes of contentType whose
// SHA-256 is sha256Hex.
//
// All three are bound into the signature, so a request that differs is turned
// away before a byte is stored. A non-positive size or a digest that is not 64
// lowercase hex characters is refused rather than signed without the binding:
// an unbound URL is an unlimited, unverified write to the bucket.
func (s *Signer) PutURL(key, contentType string, byteSize int64, sha256Hex string, expires time.Duration) (string, error) {
	if byteSize <= 0 {
		return "", errors.New("blob put url: byte size must be positive")
	}
	if !isLowerHex64(sha256Hex) {
		return "", errors.New("blob put url: sha256 must be 64 lowercase hex characters")
	}
	return s.mint("PUT", key, params{
		exp:   time.Now().Add(expires).Unix(),
		Grant: Grant{ContentType: contentType, ByteSize: byteSize, Sha256Hex: sha256Hex},
	})
}

// Verify checks a relayed request against the signature it carries and answers
// with what that signature permits.
//
// Every field of the transfer is signed, so a caller cannot widen it by editing
// the URL. method is the signed method: GET for both GET and HEAD.
func (s *Signer) Verify(method, key string, q url.Values, now time.Time) (Grant, error) {
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil {
		return Grant{}, ErrURLInvalid
	}
	var byteSize int64
	if raw := q.Get("sz"); raw != "" {
		if byteSize, err = strconv.ParseInt(raw, 10, 64); err != nil {
			return Grant{}, ErrURLInvalid
		}
	}
	p := params{
		exp: exp,
		Grant: Grant{
			ContentType: q.Get("ct"),
			ByteSize:    byteSize,
			Sha256Hex:   q.Get("sha"),
			Filename:    q.Get("fn"),
		},
	}

	// Constant time: a comparison that stops at the first wrong byte reports
	// how much of a guess was right.
	if !hmac.Equal([]byte(s.sign(method, key, p)), []byte(q.Get("sig"))) {
		return Grant{}, ErrURLInvalid
	}
	// Checked after the signature so an unsigned caller learns nothing from
	// the difference between the two answers.
	expiresAt := time.Unix(exp, 0)
	if !now.Before(expiresAt) {
		return Grant{}, ErrURLExpired
	}
	p.ExpiresAt = expiresAt
	return p.Grant, nil
}

func (s *Signer) mint(method, key string, p params) (string, error) {
	if s.base == "" {
		return "", ErrSignerUnconfigured
	}
	q := url.Values{}
	q.Set("exp", strconv.FormatInt(p.exp, 10))
	if p.ContentType != "" {
		q.Set("ct", p.ContentType)
	}
	if p.ByteSize != 0 {
		q.Set("sz", strconv.FormatInt(p.ByteSize, 10))
	}
	if p.Sha256Hex != "" {
		q.Set("sha", p.Sha256Hex)
	}
	if p.Filename != "" {
		q.Set("fn", p.Filename)
	}
	q.Set("sig", s.sign(method, key, p))
	return s.base + BlobPath + "/" + escapeKey(key) + "?" + q.Encode(), nil
}

func (s *Signer) sign(method, key string, p params) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(canonical(
		method,
		key,
		strconv.FormatInt(p.exp, 10),
		p.ContentType,
		strconv.FormatInt(p.ByteSize, 10),
		p.Sha256Hex,
		p.Filename,
	))
	return hex.EncodeToString(mac.Sum(nil))
}

// canonical length-prefixes each field so no set of values can produce the same
// bytes as a different set: a value holding a delimiter cannot borrow
// characters from the field after it.
func canonical(fields ...string) []byte {
	var b strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&b, "%d:%s", len(f), f)
	}
	return []byte(b.String())
}

// escapeKey percent-encodes each segment of an object key while leaving the
// separators alone, so the key arrives back as the path it was signed as.
func escapeKey(key string) string {
	segments := strings.Split(key, "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	return strings.Join(segments, "/")
}

func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
