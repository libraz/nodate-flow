// Package storage wraps an S3-compatible object store (MinIO / AWS S3)
// and exposes presigned-URL operations for file uploads and downloads.
package storage

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config holds the connection parameters for the S3-compatible store.
type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

// Client is a thin wrapper around the minio-go SDK that provides
// presigned URL generation and bucket bootstrapping.
type Client struct {
	mc     *minio.Client
	bucket string
}

// NewClient creates a new S3 storage client. It does not connect or
// verify credentials; call [Client.EnsureBucket] after construction to
// create the bucket if it does not exist.
func NewClient(cfg Config) (*Client, error) {
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: new client: %w", err)
	}
	return &Client{mc: mc, bucket: cfg.Bucket}, nil
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

// PresignPut returns a presigned PUT URL that the caller can use to
// upload an object directly to the store. The URL expires after the
// given duration.
//
// This variant does NOT pin the body hash: minio-go's PresignedPutObject
// signs with UNSIGNED-PAYLOAD, so the bucket cannot detect a
// content-vs-claim mismatch. Prefer [Client.PresignPutWithSha256] for
// any flow that performs content-addressed dedup against the claimed
// digest, otherwise a malicious client can poison a dedup row by
// declaring sha=A and uploading bytes B.
func (c *Client) PresignPut(ctx context.Context, key string, expires time.Duration) (string, error) {
	u, err := c.mc.PresignedPutObject(ctx, c.bucket, key, expires)
	if err != nil {
		return "", fmt.Errorf("storage: presign put: %w", err)
	}
	return u.String(), nil
}

// PresignPutWithSha256 returns a presigned PUT URL that requires the
// client to send the x-amz-content-sha256 header set to the supplied
// lowercase 64-char hex digest. The header is woven into the SigV4
// signed-headers list, so the bucket rejects the upload with
// XAmzContentSHA256Mismatch / BadDigest if the actual body hash does
// not match the expected value or the header is omitted/altered.
//
// This is the variant to use whenever the server uses the claimed
// sha256 for content-addressed dedup: without it a client could declare
// sha=A and upload bytes B, poisoning the storage_objects row that the
// next legitimate uploader of A would dedup onto.
//
// The client MUST send the header verbatim alongside the PUT:
//
//	PUT <url>
//	x-amz-content-sha256: <expectedSha256Hex>
//	body: <file bytes>
//
// minio-go's PresignedPutObject signs with UNSIGNED-PAYLOAD and offers
// no way to inject signed headers, so we drop down to PresignHeader
// (the documented escape hatch in the upstream API for exactly this
// use case) and pass the header through extraHeaders.
func (c *Client) PresignPutWithSha256(ctx context.Context, key string, expectedSha256Hex string, expires time.Duration) (string, error) {
	extraHeaders := http.Header{}
	extraHeaders.Set("x-amz-content-sha256", expectedSha256Hex)
	u, err := c.mc.PresignHeader(ctx, http.MethodPut, c.bucket, key, expires, url.Values{}, extraHeaders)
	if err != nil {
		return "", fmt.Errorf("storage: presign put with sha256: %w", err)
	}
	return u.String(), nil
}

// PresignGet returns a presigned GET URL for downloading an object.
// The response-content-disposition header is forced to "attachment" so
// browsers always download rather than render inline. The filename is
// emitted in RFC 5987 form so non-ASCII names (e.g. Japanese) survive
// the round trip through HTTP headers without UTF-8 mojibake.
func (c *Client) PresignGet(ctx context.Context, key string, filename string, expires time.Duration) (string, error) {
	params := make(url.Values)
	params.Set("response-content-disposition", contentDispositionAttachment(filename))
	u, err := c.mc.PresignedGetObject(ctx, c.bucket, key, expires, params)
	if err != nil {
		return "", fmt.Errorf("storage: presign get: %w", err)
	}
	return u.String(), nil
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

// StorageKeyForWorkspace builds the canonical content-addressed key for a
// workspace-scoped blob. wsPublicIDHex must be the workspace's public_id
// rendered as 32 hex chars (no dashes); sha256Hex must be the lowercase
// 64-char hex digest of the file body. Two uploads of the same bytes in
// the same workspace produce the same key, which is what lets the
// ref-counted storage_objects table dedup at the DB layer.
func StorageKeyForWorkspace(wsPublicIDHex, sha256Hex string) string {
	return fmt.Sprintf("workspace/%s/%s", wsPublicIDHex, sha256Hex)
}

// StorageKeyForUser builds the canonical content-addressed key for a
// user-scoped blob (currently avatars). userPublicIDHex must be the
// user's public_id as 32 hex chars; sha256Hex must be the lowercase
// 64-char hex digest of the file body.
func StorageKeyForUser(userPublicIDHex, sha256Hex string) string {
	return fmt.Sprintf("user/%s/%s", userPublicIDHex, sha256Hex)
}

// contentDispositionAttachment formats an HTTP Content-Disposition header
// per RFC 5987 / RFC 6266 with both an ASCII fallback `filename=` and a
// UTF-8 `filename*=` parameter. Browsers that only understand the legacy
// form get a sanitized name (non-ASCII bytes replaced with '_'); modern
// browsers honour the percent-encoded UTF-8 parameter and render the
// original Japanese / emoji / accented filename verbatim.
func contentDispositionAttachment(filename string) string {
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
