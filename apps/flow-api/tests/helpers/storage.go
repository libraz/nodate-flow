package helpers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/testutil"
	flowstorage "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/storage"
)

// StorageBundle bundles the per-package storage clients and the raw
// minio-go handle needed for direct object inspection (HEAD / PUT / GET)
// during integration tests. Both Flow and Auth point at the same MinIO
// container and bucket so a blob written by one service is visible to
// the other.
type StorageBundle struct {
	// Flow is the storage client wired into flow-api router.Deps.
	Flow *flowstorage.Client
	// Auth is the storage client wired into auth-api router.Deps. It
	// owns PutObject / GetObject so the avatar handler can stream
	// uploads directly to MinIO.
	Auth *testutil.StorageClient
	// Raw is the underlying minio-go client. Tests use it to assert
	// that an object physically exists / does not exist after dedup
	// or GC operations.
	Raw *minio.Client
	// Bucket is the bucket name shared by Flow and Auth.
	Bucket string
}

// BuildStorageBundle constructs the per-service storage clients pointing
// at the supplied MinIO instance, ensures the bucket exists, and returns
// the bundle ready to plug into NewTestServerWithStorage. The bucket
// name is randomized so two parallel processes (or two tests using
// StartIsolatedMinIO) cannot stomp on each other's objects.
func BuildStorageBundle(t *testing.T, inst *MinIOInstance) *StorageBundle {
	t.Helper()
	bundle, err := NewStorageBundle(inst)
	require.NoError(t, err, "build storage bundle")
	return bundle
}

// NewStorageBundle is the *testing.T-free variant for TestMain callers.
func NewStorageBundle(inst *MinIOInstance) (*StorageBundle, error) {
	if inst == nil {
		return nil, errors.New("storage bundle: nil minio instance")
	}
	bucket := fmt.Sprintf("test-%d", time.Now().UnixNano())

	flowClient, err := flowstorage.NewClient(flowstorage.Config{
		Endpoint:  inst.Endpoint,
		AccessKey: inst.AccessKey,
		SecretKey: inst.SecretKey,
		Bucket:    bucket,
		UseSSL:    false,
	})
	if err != nil {
		return nil, fmt.Errorf("flow storage client: %w", err)
	}
	if err := flowClient.EnsureBucket(context.Background()); err != nil {
		return nil, fmt.Errorf("flow ensure bucket: %w", err)
	}

	authClient, err := testutil.BuildStorageClient(testutil.StorageConfig{
		Endpoint:  inst.Endpoint,
		AccessKey: inst.AccessKey,
		SecretKey: inst.SecretKey,
		Bucket:    bucket,
		UseSSL:    false,
	})
	if err != nil {
		return nil, fmt.Errorf("auth storage client: %w", err)
	}

	raw, err := minio.New(inst.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(inst.AccessKey, inst.SecretKey, ""),
		Secure: false,
	})
	if err != nil {
		return nil, fmt.Errorf("raw minio client: %w", err)
	}

	return &StorageBundle{
		Flow:   flowClient,
		Auth:   authClient,
		Raw:    raw,
		Bucket: bucket,
	}, nil
}

// ObjectExists reports whether the given key currently has a backing
// blob in MinIO. Returns false on a 404-equivalent and surfaces every
// other error so unexpected failures fail the test loudly.
func (b *StorageBundle) ObjectExists(ctx context.Context, key string) (bool, error) {
	_, err := b.Raw.StatObject(ctx, b.Bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	resp := minio.ToErrorResponse(err)
	if resp.StatusCode == 404 || resp.Code == "NoSuchKey" {
		return false, nil
	}
	return false, err
}

// MustExist asserts that the given key has a blob in MinIO right now.
// Wraps ObjectExists with require.* so the test fails immediately on a
// violation rather than threading errors back to the caller.
func (b *StorageBundle) MustExist(t *testing.T, key string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exists, err := b.ObjectExists(ctx, key)
	require.NoError(t, err, "stat object %q", key)
	require.True(t, exists, "expected object %q to exist in MinIO", key)
}

// MustNotExist asserts that the given key has NO blob in MinIO right
// now. The complement of MustExist; used by GC tests after the last
// reference is dropped.
func (b *StorageBundle) MustNotExist(t *testing.T, key string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exists, err := b.ObjectExists(ctx, key)
	require.NoError(t, err, "stat object %q", key)
	require.False(t, exists, "expected object %q to be absent from MinIO", key)
}

// PutBytes uploads raw bytes to the bundle's bucket under the given
// key. Used by the attachment dedup tests to satisfy the presigned PUT
// step; tests can also drive a real HTTP PUT against the presigned URL,
// but the SDK call is far cheaper.
func (b *StorageBundle) PutBytes(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := b.Raw.PutObject(ctx, b.Bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}
