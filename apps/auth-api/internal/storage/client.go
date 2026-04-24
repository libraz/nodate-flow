// Package storage wraps an S3-compatible object store (MinIO / AWS S3)
// for the auth-api avatar-upload endpoints. Unlike the flow-api storage
// client which exposes presigned URLs, this client streams uploads and
// downloads server-side so the auth-api can validate images and proxy
// cache-bustable avatar URLs without leaking backend credentials.
package storage

import (
	"context"
	"fmt"
	"io"

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

// ObjectInfo describes an object returned by [Client.GetObject]. Only the
// fields that the avatar proxy needs are surfaced; add more as callers
// require them.
type ObjectInfo struct {
	// Size is the byte length of the object body.
	Size int64
	// ContentType is the stored MIME type (e.g. "image/jpeg").
	ContentType string
	// ETag is the storage-side entity tag, already quoted per the
	// minio-go convention so it can be echoed verbatim in an HTTP
	// ETag header.
	ETag string
}

// Client is a thin wrapper around the minio-go SDK that provides
// server-side streaming upload/download plus bucket bootstrapping.
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

// PutObject uploads the given reader to the configured bucket under the
// provided key. The caller is responsible for supplying a valid
// contentType; passing an empty string makes minio-go fall back to
// "application/octet-stream".
func (c *Client) PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	if _, err := c.mc.PutObject(ctx, c.bucket, key, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	}); err != nil {
		return fmt.Errorf("storage: put object: %w", err)
	}
	return nil
}

// GetObject fetches the object at key and returns a reader plus metadata.
// The caller MUST Close the returned reader. Metadata is retrieved via
// a HEAD-equivalent StatObject request so the HTTP handler can set
// Content-Length / Content-Type / ETag before streaming the body.
func (c *Client) GetObject(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("storage: get object: %w", err)
	}
	stat, err := obj.Stat()
	if err != nil {
		// Close the object before bubbling up; callers only get a reader
		// when the metadata fetch succeeds.
		_ = obj.Close()
		return nil, ObjectInfo{}, fmt.Errorf("storage: stat object: %w", err)
	}
	return obj, ObjectInfo{
		Size:        stat.Size,
		ContentType: stat.ContentType,
		ETag:        stat.ETag,
	}, nil
}

// RemoveObject deletes the object at key. It is a no-op on the remote
// side when the key does not exist.
func (c *Client) RemoveObject(ctx context.Context, key string) error {
	if err := c.mc.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: remove object: %w", err)
	}
	return nil
}

// Bucket returns the configured bucket name.
func (c *Client) Bucket() string {
	return c.bucket
}
