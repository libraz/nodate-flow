package helpers

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/testutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	calgen "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/router"
	flowstorage "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/storage"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/stream"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
)

// TestServer is a running httptest.Server bound to the full
// nodate-flow route set (via internal/http/router.Build) against a real
// database handle so integration tests can drive the API end to end.
type TestServer struct {
	BaseURL string
	Server  *httptest.Server
	DB      *sql.DB
	// JWT is exposed so calendar tests that bypass /auth/register and
	// seed stub users via direct sqlc inserts can mint a token recognized
	// by the merged router.
	JWT *auth.JWTIssuer
}

// testCipherHex is a 64-char hex string (32 bytes) used only by the
// test harness to construct a deterministic Cipher and derive the
// shared JWT key. authn.DeriveEd25519Key requires hex or base64;
// crypto.New wants the raw 32 bytes.
const testCipherHex = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"

// testCipherKey is the decoded 32-byte form of testCipherHex, suitable
// for crypto.New. It MUST NOT be used outside the test harness.
var testCipherKey = mustDecodeHex(testCipherHex)

func mustDecodeHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("tests/helpers: testCipherHex is not valid hex: " + err.Error())
	}
	return b
}

// StartTestServer boots an httptest.Server that mounts both the
// auth-api and flow-api routers against the supplied *sql.DB. Auth
// routes (/auth/*, /me/*, /workspaces CRUD, /invites/*) are served by
// auth-api; everything else (projects, tasks, AI, etc.) by flow-api.
// A fixed test cipher is always constructed so AI provider and MCP
// tests can exercise encryption end to end. The server is torn down
// via t.Cleanup.
//
// Storage is left nil; presign and avatar handlers return
// INTERNAL.UNEXPECTED / AUTH.AVATAR.STORAGE_UNAVAILABLE. Callers that
// need real S3/MinIO integration should use StartTestServerWithStorage.
func StartTestServer(t *testing.T, db *sql.DB) *TestServer {
	t.Helper()
	require.NotNil(t, db, "StartTestServer requires a non-nil *sql.DB")

	// Derive a deterministic Ed25519 key so both routers share the
	// same JWT signing/verification key pair.
	jwtKey, err := authn.DeriveEd25519Key(testCipherHex, "nodate-flow:jwt:v1")
	require.NoError(t, err, "derive jwt key")

	queries := generated.New(db)
	calendarQueries := calgen.New(db)

	jwtIssuer, err := auth.NewJWTIssuer(jwtKey, "nodate-flow", "api", 15*time.Minute)
	require.NoError(t, err, "init flow jwt issuer")

	cipher, err := crypto.New(testCipherKey)
	require.NoError(t, err, "init test cipher")

	notifier := stream.NewInProcessNotifier()
	tap := stream.NewEventbusTap(notifier)
	eventbus.SetNotifyHook(tap.Publish)

	flowHandler := router.Build(router.Deps{
		DB:                 db,
		Queries:            queries,
		CalendarQueries:    calendarQueries,
		JWT:                jwtIssuer,
		Cipher:             cipher,
		GhWebhookSecret:    "",
		SlackSigningSecret: "",
		DefaultWorkspaceID: "",
		DisableRateLimit:   true,
		AiMock:             os.Getenv("NF_FLOW_AI_MOCK") != "" && os.Getenv("NF_FLOW_AI_MOCK") != "0" && os.Getenv("NF_FLOW_AI_MOCK") != "false",
		StreamNotifier:     notifier,
		StreamRemember:     tap.RememberWorkspace,
	})

	// Build auth-api router with the same DB and JWT key so both
	// routers share the same token verification.
	authHandler, err := testutil.BuildTestRouter(db, jwtKey, testCipherKey)
	require.NoError(t, err, "build auth-api test router")

	composite := newCompositeHandler(authHandler, flowHandler)
	srv := httptest.NewServer(composite)
	t.Cleanup(func() {
		eventbus.SetNotifyHook(nil)
		srv.Close()
	})

	return &TestServer{BaseURL: srv.URL, Server: srv, DB: db, JWT: jwtIssuer}
}

// NewTestServer is the same as StartTestServer but without a *testing.T
// dependency. Callers must invoke the returned cleanup func when done.
func NewTestServer(db *sql.DB) (*TestServer, func(), error) {
	if db == nil {
		return nil, nil, fmt.Errorf("NewTestServer requires a non-nil *sql.DB")
	}
	jwtKey, err := authn.DeriveEd25519Key(testCipherHex, "nodate-flow:jwt:v1")
	if err != nil {
		return nil, nil, fmt.Errorf("derive jwt key: %w", err)
	}
	queries := generated.New(db)
	calendarQueries := calgen.New(db)
	jwtIssuer, err := auth.NewJWTIssuer(jwtKey, "nodate-flow", "api", 15*time.Minute)
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

	flowHandler := router.Build(router.Deps{
		DB:                 db,
		Queries:            queries,
		CalendarQueries:    calendarQueries,
		JWT:                jwtIssuer,
		Cipher:             cipher,
		GhWebhookSecret:    "",
		SlackSigningSecret: "",
		DefaultWorkspaceID: "",
		DisableRateLimit:   true,
		AiMock:             os.Getenv("NF_FLOW_AI_MOCK") != "" && os.Getenv("NF_FLOW_AI_MOCK") != "0" && os.Getenv("NF_FLOW_AI_MOCK") != "false",
		StreamNotifier:     notifier,
		StreamRemember:     tap.RememberWorkspace,
	})

	authHandler, err := testutil.BuildTestRouter(db, jwtKey, testCipherKey)
	if err != nil {
		return nil, nil, fmt.Errorf("build auth-api test router: %w", err)
	}

	composite := newCompositeHandler(authHandler, flowHandler)
	srv := httptest.NewServer(composite)
	cleanup := func() {
		eventbus.SetNotifyHook(nil)
		srv.Close()
	}
	return &TestServer{BaseURL: srv.URL, Server: srv, DB: db, JWT: jwtIssuer}, cleanup, nil
}

// NewTestServerWithStorage is the same as NewTestServer but additionally
// plumbs the supplied StorageBundle into both the flow-api and
// auth-api routers. The bundle's Flow client serves task / calendar
// attachment presign endpoints; the bundle's Auth client serves the
// avatar upload / delete / proxy handlers. Pass nil to fall back to
// the no-storage path.
func NewTestServerWithStorage(db *sql.DB, bundle *StorageBundle) (*TestServer, func(), error) {
	if db == nil {
		return nil, nil, fmt.Errorf("NewTestServerWithStorage requires a non-nil *sql.DB")
	}
	jwtKey, err := authn.DeriveEd25519Key(testCipherHex, "nodate-flow:jwt:v1")
	if err != nil {
		return nil, nil, fmt.Errorf("derive jwt key: %w", err)
	}
	queries := generated.New(db)
	calendarQueries := calgen.New(db)
	jwtIssuer, err := auth.NewJWTIssuer(jwtKey, "nodate-flow", "api", 15*time.Minute)
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

	var flowStorage *flowstorage.Client
	if bundle != nil {
		flowStorage = bundle.Flow
	}

	flowHandler := router.Build(router.Deps{
		DB:                 db,
		Queries:            queries,
		CalendarQueries:    calendarQueries,
		JWT:                jwtIssuer,
		Cipher:             cipher,
		GhWebhookSecret:    "",
		SlackSigningSecret: "",
		DefaultWorkspaceID: "",
		DisableRateLimit:   true,
		AiMock:             os.Getenv("NF_FLOW_AI_MOCK") != "" && os.Getenv("NF_FLOW_AI_MOCK") != "0" && os.Getenv("NF_FLOW_AI_MOCK") != "false",
		StreamNotifier:     notifier,
		StreamRemember:     tap.RememberWorkspace,
		Storage:            flowStorage,
	})

	var authHandler http.Handler
	if bundle != nil {
		authHandler, err = testutil.BuildTestRouterWithStorage(db, jwtKey, testCipherKey, bundle.Auth)
	} else {
		authHandler, err = testutil.BuildTestRouter(db, jwtKey, testCipherKey)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("build auth-api test router: %w", err)
	}

	composite := newCompositeHandler(authHandler, flowHandler)
	srv := httptest.NewServer(composite)
	cleanup := func() {
		eventbus.SetNotifyHook(nil)
		srv.Close()
	}
	return &TestServer{BaseURL: srv.URL, Server: srv, DB: db, JWT: jwtIssuer}, cleanup, nil
}

// NewTestServerWithServiceToken boots a dedicated httptest.Server with
// the flow-worker service token configured on the router. Used by the
// service-token auth e2e suite so the shared test server (which has
// no token configured) is not perturbed. The returned cleanup MUST be
// invoked when the test finishes.
func NewTestServerWithServiceToken(db *sql.DB, serviceToken string) (*TestServer, func(), error) {
	if db == nil {
		return nil, nil, fmt.Errorf("NewTestServerWithServiceToken requires a non-nil *sql.DB")
	}
	jwtKey, err := authn.DeriveEd25519Key(testCipherHex, "nodate-flow:jwt:v1")
	if err != nil {
		return nil, nil, fmt.Errorf("derive jwt key: %w", err)
	}
	queries := generated.New(db)
	calendarQueries := calgen.New(db)
	jwtIssuer, err := auth.NewJWTIssuer(jwtKey, "nodate-flow", "api", 15*time.Minute)
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

	flowHandler := router.Build(router.Deps{
		DB:                 db,
		Queries:            queries,
		CalendarQueries:    calendarQueries,
		JWT:                jwtIssuer,
		Cipher:             cipher,
		GhWebhookSecret:    "",
		SlackSigningSecret: "",
		DefaultWorkspaceID: "",
		DisableRateLimit:   true,
		AiMock:             os.Getenv("NF_FLOW_AI_MOCK") != "" && os.Getenv("NF_FLOW_AI_MOCK") != "0" && os.Getenv("NF_FLOW_AI_MOCK") != "false",
		StreamNotifier:     notifier,
		StreamRemember:     tap.RememberWorkspace,
		FlowAPISignalToken: serviceToken,
	})

	authHandler, err := testutil.BuildTestRouter(db, jwtKey, testCipherKey)
	if err != nil {
		return nil, nil, fmt.Errorf("build auth-api test router: %w", err)
	}

	composite := newCompositeHandler(authHandler, flowHandler)
	srv := httptest.NewServer(composite)
	cleanup := func() {
		eventbus.SetNotifyHook(nil)
		srv.Close()
	}
	return &TestServer{BaseURL: srv.URL, Server: srv, DB: db, JWT: jwtIssuer}, cleanup, nil
}

// compositeHandler dispatches requests to the primary handler first.
// If the primary returns 404 (route not found), it falls back to the
// secondary handler. This lets the test server compose auth-api and
// flow-api routers into a single httptest.Server.
type compositeHandler struct {
	primary   http.Handler
	secondary http.Handler
}

func newCompositeHandler(primary, secondary http.Handler) *compositeHandler {
	return &compositeHandler{primary: primary, secondary: secondary}
}

func (c *compositeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := httptest.NewRecorder()
	c.primary.ServeHTTP(rec, r)
	if rec.Code == http.StatusNotFound {
		c.secondary.ServeHTTP(w, r)
		return
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}
