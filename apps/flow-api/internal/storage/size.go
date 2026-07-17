package storage

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
)

// StatObject returns the actual stored size, in bytes, of the object at key.
// It is used after a presigned PUT completes to verify the real upload size
// against the per-file ceiling: the byteSize a client declares at presign
// time is never trusted, because a client can declare a tiny size and then
// stream a much larger body to the presigned URL.
func (c *Client) StatObject(ctx context.Context, key string) (int64, error) {
	info, err := c.mc.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("storage: stat object: %w", err)
	}
	return info.Size, nil
}

// ExceedsUploadLimit reports whether an object's actual stored size exceeds
// the per-file upload ceiling. Only the real size (fetched via StatObject)
// is considered; the client-declared size is deliberately ignored because
// it is attacker-controlled. Callers pass maxSize from
// handlerutil.MaxUploadSize.
func ExceedsUploadLimit(actualSize, maxSize uint64) bool {
	return actualSize > maxSize
}
