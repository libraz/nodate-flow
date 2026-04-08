package helpers

import (
	"database/sql"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/crypto"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/router"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/stream"
)

// TestServer is a running httptest.Server bound to the full
// nodate-flow route set (via internal/http/router.Build) against a real
// database handle so integration tests can drive the API end to end.
type TestServer struct {
	BaseURL string
	Server  *httptest.Server
	DB      *sql.DB
}

// testCipherKey is a fixed 32-byte master key used only by
// StartTestServer to construct a deterministic Cipher for tests. It MUST
// NOT be used outside the test harness.
var testCipherKey = []byte("test-cipher-key-aaaaaaaaaaaaaaaa")

// StartTestServer boots an httptest.Server that mounts the full
// nodate-flow API router against the supplied *sql.DB. A fixed test
// cipher is always constructed so AI provider and MCP tests can exercise
// encryption end to end. The server is torn down via t.Cleanup.
func StartTestServer(t *testing.T, db *sql.DB) *TestServer {
	t.Helper()
	require.NotNil(t, db, "StartTestServer requires a non-nil *sql.DB")

	queries := generated.New(db)

	jwtIssuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	require.NoError(t, err, "init jwt issuer")

	cipher, err := crypto.New(testCipherKey)
	require.NoError(t, err, "init test cipher")

	notifier := stream.NewInProcessNotifier()
	tap := stream.NewEventbusTap(notifier)
	eventbus.SetNotifyHook(tap.Publish)

	handler := router.Build(router.Deps{
		DB:                 db,
		Queries:            queries,
		JWT:                jwtIssuer,
		Cipher:             cipher,
		GhWebhookSecret:    "",
		SlackSigningSecret: "",
		DefaultWorkspaceID: "",
		AiMock:             os.Getenv("NF_AI_MOCK") != "" && os.Getenv("NF_AI_MOCK") != "0" && os.Getenv("NF_AI_MOCK") != "false",
		StreamNotifier:     notifier,
		StreamRemember:     tap.RememberWorkspace,
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		eventbus.SetNotifyHook(nil)
		srv.Close()
	})

	return &TestServer{BaseURL: srv.URL, Server: srv, DB: db}
}

// NewTestServer is the same as StartTestServer but without a *testing.T
// dependency. Callers must invoke the returned cleanup func when done.
func NewTestServer(db *sql.DB) (*TestServer, func(), error) {
	if db == nil {
		return nil, nil, fmt.Errorf("NewTestServer requires a non-nil *sql.DB")
	}
	queries := generated.New(db)
	jwtIssuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		return nil, nil, fmt.Errorf("init jwt issuer: %w", err)
	}
	cipher, err := crypto.New(testCipherKey)
	if err != nil {
		return nil, nil, fmt.Errorf("init test cipher: %w", err)
	}
	notifier := stream.NewInProcessNotifier()
	tap := stream.NewEventbusTap(notifier)
	eventbus.SetNotifyHook(tap.Publish)

	handler := router.Build(router.Deps{
		DB:                 db,
		Queries:            queries,
		JWT:                jwtIssuer,
		Cipher:             cipher,
		GhWebhookSecret:    "",
		SlackSigningSecret: "",
		DefaultWorkspaceID: "",
		AiMock:             os.Getenv("NF_AI_MOCK") != "" && os.Getenv("NF_AI_MOCK") != "0" && os.Getenv("NF_AI_MOCK") != "false",
		StreamNotifier:     notifier,
		StreamRemember:     tap.RememberWorkspace,
	})
	srv := httptest.NewServer(handler)
	cleanup := func() {
		eventbus.SetNotifyHook(nil)
		srv.Close()
	}
	return &TestServer{BaseURL: srv.URL, Server: srv, DB: db}, cleanup, nil
}
