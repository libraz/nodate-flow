// Package storage wraps an S3-compatible object store (MinIO / AWS S3).
//
// The store is reachable only from the private network. Browsers never talk to
// it: the Client mints HMAC-signed URLs that address the API's own blob relay,
// and the relay moves the bytes through Open and Put. See Signer.
package storage

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrChecksumMismatch means the store rejected a write because the bytes did
// not hash to the digest the caller declared. The object was not stored.
var ErrChecksumMismatch = errors.New("storage: body does not match the declared sha256")

// Config holds the connection parameters for the S3-compatible store.
type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
	// Signer mints the browser-facing blob URLs. Nil leaves the client able to
	// store and read blobs server-side but unable to hand any to a browser.
	Signer *Signer
}

// Client is a thin wrapper around the minio-go SDK that mints signed blob URLs,
// streams objects in and out for the relay, and bootstraps the bucket.
type Client struct {
	mc     *minio.Client
	bucket string
	signer *Signer
}

// NewClient creates a new S3 storage client. It does not connect or
// verify credentials; call [Client.EnsureBucket] after construction to
// create the bucket if it does not exist.
func NewClient(cfg Config) (*Client, error) {
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		// minio-go refuses a caller-supplied x-amz-checksum-* header unless
		// the client opts into trailing-header support. Put relies on that
		// header to have the store verify the body digest.
		TrailingHeaders: true,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: new client: %w", err)
	}
	return &Client{mc: mc, bucket: cfg.Bucket, signer: cfg.Signer}, nil
}

// EnsureBucket creates the configured bucket if it does not already
// exist. Safe to call on every startup.
func (c *Client) EnsureBucket(ctx context.Context) error {
	exists, err := c.mc.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("storage: bucket exists check: %w", err)
	}
	if exists {
		return nil
	}
	if err := c.mc.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("storage: make bucket: %w", err)
	}
	return nil
}

// SignedPutURL returns a URL the browser uploads an object through: exactly
// byteSize bytes of contentType whose SHA-256 is sha256Hex. See Signer.PutURL.
func (c *Client) SignedPutURL(key, contentType string, byteSize int64, sha256Hex string, expires time.Duration) (string, error) {
	if c.signer == nil {
		return "", ErrSignerUnconfigured
	}
	return c.signer.PutURL(key, contentType, byteSize, sha256Hex, expires)
}

// SignedDownloadURL returns a URL the browser saves the object through as an
// attachment named filename. The name is emitted in RFC 5987 form by the relay
// so non-ASCII names survive the round trip through HTTP headers.
func (c *Client) SignedDownloadURL(key, filename string, expires time.Duration) (string, error) {
	if c.signer == nil {
		return "", ErrSignerUnconfigured
	}
	return c.signer.DownloadURL(key, filename, expires)
}

// Object is an open object body. It is seekable so the relay can answer Range
// requests without reading from the start.
type Object struct {
	obj *minio.Object
	// Size is the stored length in bytes.
	Size int64
	// ModTime is the object's last-modified time.
	ModTime time.Time
}

// Read implements io.Reader.
func (o *Object) Read(p []byte) (int, error) { return o.obj.Read(p) }

// Seek implements io.Seeker.
func (o *Object) Seek(offset int64, whence int) (int64, error) { return o.obj.Seek(offset, whence) }

// Close releases the underlying connection.
func (o *Object) Close() error { return o.obj.Close() }

// Open returns the object's bytes for reading, and whether it exists.
//
// The metadata is read up front because minio-go opens lazily: a missing
// object would otherwise surface partway through a response that had already
// claimed success.
func (c *Client) Open(ctx context.Context, key string) (*Object, bool, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("storage: open object: %w", err)
	}
	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		resp := minio.ToErrorResponse(err)
		if resp.StatusCode == 404 || resp.Code == "NoSuchKey" {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("storage: stat opened object: %w", err)
	}
	return &Object{obj: obj, Size: stat.Size, ModTime: stat.LastModified}, true, nil
}

// Put stores exactly byteSize bytes read from r under key, and has the store
// itself reject the write if the bytes do not hash to sha256Hex.
//
// The digest goes out as x-amz-checksum-sha256 on a single PUT, so the store
// verifies the whole body before the object becomes visible. Multipart is
// disabled because a part-wise upload would carry no whole-object SHA-256 to
// check. A rejected digest returns ErrChecksumMismatch.
func (c *Client) Put(ctx context.Context, key string, r io.Reader, byteSize int64, contentType, sha256Hex string) error {
	raw, err := hex.DecodeString(sha256Hex)
	if err != nil || len(raw) != 32 {
		return errors.New("storage: put: sha256 must be 64 hex characters")
	}
	_, err = c.mc.PutObject(ctx, c.bucket, key, r, byteSize, minio.PutObjectOptions{
		ContentType:      contentType,
		DisableMultipart: true,
		UserMetadata:     map[string]string{"x-amz-checksum-sha256": base64.StdEncoding.EncodeToString(raw)},
	})
	if err != nil {
		switch minio.ToErrorResponse(err).Code {
		case "BadDigest", "XAmzContentChecksumMismatch", "XAmzContentSHA256Mismatch", "InvalidDigest":
			return ErrChecksumMismatch
		}
		return fmt.Errorf("storage: put object: %w", err)
	}
	return nil
}

// RemoveObject deletes the object at key. It is a no-op on the remote
// side when the key does not exist (S3 DELETE returns 204 either way).
// Used by ref-count GC after the last referencing row goes away.
func (c *Client) RemoveObject(ctx context.Context, key string) error {
	if err := c.mc.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: remove object: %w", err)
	}
	return nil
}

// RemoveObjects deletes the given keys from the bucket in bulk. Errors
// for individual keys are aggregated; if any deletion fails the method
// returns the first error after attempting the rest. minio-go's
// RemoveObjects API uses a goroutine + chan to stream up to 1000 keys
// per S3 batch request.
func (c *Client) RemoveObjects(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	objCh := make(chan minio.ObjectInfo, len(keys))
	go func() {
		defer close(objCh)
		for _, k := range keys {
			select {
			case <-ctx.Done():
				return
			case objCh <- minio.ObjectInfo{Key: k}:
			}
		}
	}()
	var firstErr error
	for rerr := range c.mc.RemoveObjects(ctx, c.bucket, objCh, minio.RemoveObjectsOptions{}) {
		if rerr.Err != nil && firstErr == nil {
			firstErr = fmt.Errorf("storage: remove object %q: %w", rerr.ObjectName, rerr.Err)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	return nil
}

// Bucket returns the configured bucket name.
func (c *Client) Bucket() string {
	return c.bucket
}

// KeyForWorkspace builds the canonical content-addressed key for a
// workspace-scoped blob. wsPublicIDHex must be the workspace's public_id
// rendered as 32 hex chars (no dashes); sha256Hex must be the lowercase
// 64-char hex digest of the file body. Two uploads of the same bytes in
// the same workspace produce the same key, which is what lets the
// ref-counted storage_objects table dedup at the DB layer.
func KeyForWorkspace(wsPublicIDHex, sha256Hex string) string {
	return fmt.Sprintf("workspace/%s/%s", wsPublicIDHex, sha256Hex)
}

// KeyForUser builds the canonical content-addressed key for a
// user-scoped blob (currently avatars). userPublicIDHex must be the
// user's public_id as 32 hex chars; sha256Hex must be the lowercase
// 64-char hex digest of the file body.
func KeyForUser(userPublicIDHex, sha256Hex string) string {
	return fmt.Sprintf("user/%s/%s", userPublicIDHex, sha256Hex)
}

// ContentDispositionAttachment formats an HTTP Content-Disposition header
// per RFC 5987 / RFC 6266 with both an ASCII fallback `filename=` and a
// UTF-8 `filename*=` parameter. Browsers that only understand the legacy
// form get a sanitized name (non-ASCII bytes replaced with '_'); modern
// browsers honour the percent-encoded UTF-8 parameter and render the
// original Japanese / emoji / accented filename verbatim.
func ContentDispositionAttachment(filename string) string {
	ascii := asciiFallbackName(filename)
	encoded := percentEncodeRFC5987(filename)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, encoded)
}

// asciiFallbackName replaces every non-ASCII byte and every byte that is
// unsafe inside a quoted-string Content-Disposition value with '_'. The
// returned string is always pure printable ASCII so the legacy
// `filename=` parameter is a valid HTTP header value even when the
// original name contains UTF-8.
func asciiFallbackName(filename string) string {
	if filename == "" {
		return "download"
	}
	var b strings.Builder
	b.Grow(len(filename))
	for i := 0; i < len(filename); i++ {
		c := filename[i]
		// Reject controls, DEL, non-ASCII, and characters that would
		// require quoting/escaping inside a quoted-string. Backslash
		// and double-quote are unsafe; the rest of the disallow list
		// matches the RFC 5987 attr-char production for safety.
		if c < 0x20 || c == 0x7f || c >= 0x80 ||
			c == '"' || c == '\\' || c == '/' {
			b.WriteByte('_')
			continue
		}
		b.WriteByte(c)
	}
	out := b.String()
	if out == "" {
		return "download"
	}
	return out
}

// percentEncodeRFC5987 percent-encodes the filename for use as the value
// of an RFC 5987 / RFC 8187 ext-value parameter (e.g. `filename*`). Only
// the unreserved characters defined by RFC 3986 plus the `attr-char`
// additions allowed by RFC 5987 are passed through verbatim; everything
// else (including '*', '\”, '(', ')', '%' and every non-ASCII byte of
// the UTF-8 encoding) is encoded as %HH using the original UTF-8 bytes.
//
// We do not use url.PathEscape because it leaves several characters
// (notably `*`, `'`, `(`, `)`, `:`, `@`) un-encoded that RFC 5987 does
// not permit inside an ext-value, and would also encode '~' which is in
// the RFC 5987 attr-char allow-list and is more readable as ~ than %7E.
func percentEncodeRFC5987(filename string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(filename) * 3)
	for i := 0; i < len(filename); i++ {
		c := filename[i]
		switch {
		case c >= 'A' && c <= 'Z',
			c >= 'a' && c <= 'z',
			c >= '0' && c <= '9',
			// RFC 5987 attr-char: alpha / digit plus
			// !#$&+-.^_`|~ (the safe ones).
			c == '!' || c == '#' || c == '$' || c == '&' ||
				c == '+' || c == '-' || c == '.' || c == '^' ||
				c == '_' || c == '`' || c == '|' || c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&0x0f])
		}
	}
	return b.String()
}
