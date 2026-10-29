package helpers

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StorageInstance is a running S3-compatible storage container and the
// connection parameters that the storage.Client / NF_S3_* env vars need.
type StorageInstance struct {
	Container testcontainers.Container
	// Endpoint is the host:port suitable for NF_S3_ENDPOINT.
	Endpoint  string
	AccessKey string
	SecretKey string
}

const (
	// Same image compose.yml pins.
	storageImage     = "versity/versitygw:v1.10.0"
	storageAccessKey = "minioadmin"
	storageSecretKey = "minioadmin"
	storagePort      = "7070/tcp"
)

var (
	sharedStorageOnce sync.Once
	sharedStorageInst *StorageInstance
	sharedStorageErr  error
)

// StartSharedStorage returns a process-wide storage instance. Subsequent
// callers receive the same handle. The container is never explicitly
// terminated; testcontainers-ryuk reaps it when the process exits.
func StartSharedStorage(t *testing.T) *StorageInstance {
	t.Helper()
	sharedStorageOnce.Do(func() {
		sharedStorageInst, sharedStorageErr = startStorage(context.Background())
	})
	require.NoError(t, sharedStorageErr, "shared storage container failed to start")
	require.NotNil(t, sharedStorageInst)
	return sharedStorageInst
}

// EnsureSharedStorage is the same as StartSharedStorage but without a
// *testing.T dependency, so it can be called from TestMain.
func EnsureSharedStorage() (*StorageInstance, error) {
	sharedStorageOnce.Do(func() {
		sharedStorageInst, sharedStorageErr = startStorage(context.Background())
	})
	return sharedStorageInst, sharedStorageErr
}

// StartIsolatedStorage returns a brand new storage container, terminated
// when the test ends.
func StartIsolatedStorage(t *testing.T) *StorageInstance {
	t.Helper()
	inst, err := startStorage(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = inst.Container.Terminate(context.Background())
	})
	return inst
}

// startStorage boots a Versity S3 Gateway (posix backend on tmpfs) with
// the same credentials as compose.yml.
func startStorage(ctx context.Context) (*StorageInstance, error) {
	startCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	container, err := testcontainers.GenericContainer(startCtx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: storageImage,
			Cmd:   []string{"posix", "/data"},
			Env: map[string]string{
				"ROOT_ACCESS_KEY": storageAccessKey,
				"ROOT_SECRET_KEY": storageSecretKey,
				"VGW_HEALTH":      "/health",
			},
			ExposedPorts: []string{storagePort},
			Tmpfs:        map[string]string{"/data": ""},
			WaitingFor: wait.ForHTTP("/health").
				WithPort(storagePort).
				WithStartupTimeout(time.Minute),
		},
		Started: true,
	})
	if err != nil {
		return nil, fmt.Errorf("start storage container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("storage host: %w", err)
	}
	port, err := container.MappedPort(ctx, storagePort)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("storage mapped port: %w", err)
	}

	return &StorageInstance{
		Container: container,
		Endpoint:  fmt.Sprintf("%s:%s", host, port.Port()),
		AccessKey: storageAccessKey,
		SecretKey: storageSecretKey,
	}, nil
}
