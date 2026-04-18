package helpers

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/minio"
)

// MinIOInstance is a running MinIO container and the connection
// parameters that the storage.Client / ND_S3_* env vars need.
type MinIOInstance struct {
	Container *minio.MinioContainer
	// Endpoint is the host:port suitable for ND_S3_ENDPOINT.
	Endpoint  string
	AccessKey string
	SecretKey string
}

const (
	minioImage     = "minio/minio:latest"
	minioAccessKey = "minioadmin"
	minioSecretKey = "minioadmin"
)

var (
	sharedMinIOOnce sync.Once
	sharedMinIOInst *MinIOInstance
	sharedMinIOErr  error
)

// StartSharedMinIO returns a process-wide MinIO instance. Subsequent
// callers receive the same handle. The container is never explicitly
// terminated; testcontainers-ryuk reaps it when the process exits.
func StartSharedMinIO(t *testing.T) *MinIOInstance {
	t.Helper()
	sharedMinIOOnce.Do(func() {
		sharedMinIOInst, sharedMinIOErr = startMinIO(context.Background())
	})
	require.NoError(t, sharedMinIOErr, "shared MinIO container failed to start")
	require.NotNil(t, sharedMinIOInst)
	return sharedMinIOInst
}

// EnsureSharedMinIO is the same as StartSharedMinIO but without a
// *testing.T dependency, so it can be called from TestMain.
func EnsureSharedMinIO() (*MinIOInstance, error) {
	sharedMinIOOnce.Do(func() {
		sharedMinIOInst, sharedMinIOErr = startMinIO(context.Background())
	})
	return sharedMinIOInst, sharedMinIOErr
}

// StartIsolatedMinIO returns a brand new MinIO container, terminated
// when the test ends.
func StartIsolatedMinIO(t *testing.T) *MinIOInstance {
	t.Helper()
	inst, err := startMinIO(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = inst.Container.Terminate(context.Background())
	})
	return inst
}

// startMinIO boots a MinIO container using the testcontainers minio
// module with the same credentials as compose.yml.
func startMinIO(ctx context.Context) (*MinIOInstance, error) {
	startCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	container, err := minio.Run(
		startCtx,
		minioImage,
		minio.WithUsername(minioAccessKey),
		minio.WithPassword(minioSecretKey),
	)
	if err != nil {
		return nil, fmt.Errorf("start minio container: %w", err)
	}

	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("minio connection string: %w", err)
	}

	return &MinIOInstance{
		Container: container,
		Endpoint:  connStr,
		AccessKey: container.Username,
		SecretKey: container.Password,
	}, nil
}
