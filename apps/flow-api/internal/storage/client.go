// Package storage wraps an S3-compatible object store (MinIO / AWS S3)
// and exposes presigned-URL operations for file uploads and downloads.
package storage

import (
	"context"
	"fmt"
	"net/url"
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
func (c *Client) PresignPut(ctx context.Context, key string, expires time.Duration) (string, error) {
	u, err := c.mc.PresignedPutObject(ctx, c.bucket, key, expires)
	if err != nil {
		return "", fmt.Errorf("storage: presign put: %w", err)
	}
	return u.String(), nil
}

// PresignGet returns a presigned GET URL for downloading an object.
// The response-content-disposition header is forced to "attachment" so
// browsers always download rather than render inline.
func (c *Client) PresignGet(ctx context.Context, key string, filename string, expires time.Duration) (string, error) {
	params := make(url.Values)
	params.Set("response-content-disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	u, err := c.mc.PresignedGetObject(ctx, c.bucket, key, expires, params)
	if err != nil {
		return "", fmt.Errorf("storage: presign get: %w", err)
	}
	return u.String(), nil
}

// Bucket returns the configured bucket name.
func (c *Client) Bucket() string {
	return c.bucket
}
