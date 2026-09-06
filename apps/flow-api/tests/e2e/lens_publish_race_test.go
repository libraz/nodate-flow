package e2e

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/lenses"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	sharedtoken "github.com/libraz/nodate-flow/packages/go-shared/token"
)

// lensRaceDB is a generated.DBTX decorator that opens the check-then-act
// window the publish and unpublish handlers race against: the first time
// a statement carrying marker is executed, a competing write runs to
// completion first, so the handler's own statement matches no row.
// Embedding *sql.DB leaves every other DBTX method untouched.
type lensRaceDB struct {
	*sql.DB
	marker  string
	once    sync.Once
	compete func(ctx context.Context)
}

// ExecContext fires the competing write once, immediately before the
// handler's guarded statement reaches the server.
func (d *lensRaceDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if strings.Contains(query, "UPDATE lenses") && strings.Contains(query, d.marker) {
		d.once.Do(func() { d.compete(ctx) })
	}
	return d.DB.ExecContext(ctx, query, args...)
}

// raceOutcome carries the competing write's result off the goroutine it
// ran on. Asserting there would call FailNow on the server's goroutine,
// which stops that goroutine instead of the test.
type raceOutcome struct {
	affected int64
	err      error
}

// requireWon asserts the competing write is the one that changed the row,
// so a refusal below is the handler losing the race rather than a
// competitor that never ran.
func (o raceOutcome) requireWon(t *testing.T, label string) {
	t.Helper()
	require.NoError(t, o.err, "the competing %s must have run", label)
	require.Equal(t, int64(1), o.affected, "the competing %s must be the one that wins", label)
}

// internalID reads an internal row id by public_id. The lens handlers are
// called in process here, so the test needs the ids the HTTP surface
// never exposes.
func internalID(t *testing.T, table, publicID string) uint32 {
	t.Helper()
	pub, err := uuid.Parse(publicID)
	require.NoError(t, err)
	var id uint32
	require.NoError(t, testDB.QueryRowContext(context.Background(),
		"SELECT id FROM "+table+" WHERE public_id = ?", pub[:]).Scan(&id))
	return id
}

// countRows returns the number of rows matching a single-column filter.
func countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}

// runInWorkspaceContext invokes fn with the request context the lens
// handlers read their workspace and actor from. The context is built by
// the real membership middleware rather than assembled by hand, so the
// role the handlers gate on is the one the router would have resolved.
func runInWorkspaceContext(t *testing.T, tt *helpers.TestTenant, fn func(ctx context.Context)) {
	t.Helper()
	userID := internalID(t, "users", tt.UserPublicID)

	asActor := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithActor(r.Context(), userID)))
		})
	}
	router := chi.NewRouter()
	router.Group(func(sub chi.Router) {
		sub.Use(asActor)
		sub.Use(middleware.RequireWorkspaceMember(testDB))
		sub.Post("/workspaces/{wsId}/run", func(_ http.ResponseWriter, r *http.Request) {
			fn(r.Context())
		})
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/workspaces/"+tt.WorkspacePublicID+"/run", "", nil) //#nosec G107 -- httptest URL
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode, "the membership gate must have resolved the workspace")
}

// TestLensPublish_LosesTheRace_RefusesAndRecordsNothing pins the window
// between the handler's read of is_public and its guarded UPDATE: when
// another publish lands first, the handler's statement matches no row and
// the hash it minted was never stored. It must refuse — a token whose
// hash is nowhere unlocks nothing — and it must write neither the
// lens.shared event nor the lens.publish audit row, because both would
// describe a share this call did not create.
func TestLensPublish_LosesTheRace_RefusesAndRecordsNothing(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	lensID := createLens(t, tt.WorkspacePublicID, tt.AccessToken, "Race View "+randomHex(4))
	wsID := internalID(t, "workspaces", tt.WorkspacePublicID)
	lensPub, err := types.Parse(lensID)
	require.NoError(t, err)

	// The competing publish is the one that wins, and its token is the one
	// the URL should keep resolving to.
	winnerToken, winnerHash, err := sharedtoken.MintToken()
	require.NoError(t, err)

	// The competing write runs on the server goroutine, so its outcome is
	// carried back and asserted on the test goroutine.
	var competed raceOutcome
	raceDB := &lensRaceDB{DB: testDB, marker: "is_public = FALSE", compete: func(ctx context.Context) {
		competed.affected, competed.err = generated.New(testDB).SetLensPublic(ctx, generated.SetLensPublicParams{
			PublicTokenHash: sql.NullString{String: winnerHash, Valid: true},
			WorkspaceID:     wsID,
			PublicID:        lensPub,
		})
	}}

	// Only the handler's own statements go through the decorator: the
	// recorder reads the plain handle so the competing write is opened
	// exactly once, against the guarded UPDATE under test.
	deps := lenses.Deps{
		DB:        testDB,
		Queries:   generated.New(raceDB),
		Mutations: mutationlog.New(testDB, generated.New(testDB)),
	}

	var out *lenses.PublishLensOutput
	var handlerErr error
	runInWorkspaceContext(t, tt, func(ctx context.Context) {
		out, handlerErr = lenses.Publish(deps)(ctx, &lenses.PublishLensInput{
			WsID:   tt.WorkspacePublicID,
			LensID: lensID,
		})
	})

	competed.requireWon(t, "publish")

	var prob *handlerutil.ProblemDetails
	require.ErrorAs(t, handlerErr, &prob, "a publish that stored no hash must be reported as an error")
	require.Equal(t, "WS.LENS.ALREADY_PUBLIC", prob.Type,
		"losing the race reads the same as publishing a lens that is already published")
	require.Nil(t, out, "no token may be handed out for a write that changed no row")

	require.Zero(t, countRows(t, "SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'lens.shared'", wsID),
		"no event may claim a share this call did not create")
	require.Zero(t, countRows(t, "SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = 'lens.publish'", wsID),
		"no audit row may claim a publish this call did not perform")

	// The live URL is the winner's, which is what makes the refusal the
	// honest answer rather than a lost update.
	status, body := doJSONStatus(t, http.MethodGet, testServerURL+"/public/lenses/"+winnerToken, "", nil)
	require.Equalf(t, http.StatusOK, status, "the winning publish's URL must resolve: body=%s", string(body))
}

// TestLensPublish_WinsTheRace_MintsAndRecords is the control for the
// refusal above: with no competing write the same handler publishes, the
// returned token opens the public page, and both records are written.
// Without it the refusal assertion would pass on a handler that refuses
// every publish.
func TestLensPublish_WinsTheRace_MintsAndRecords(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	lensID := createLens(t, tt.WorkspacePublicID, tt.AccessToken, "Race Control "+randomHex(4))
	wsID := internalID(t, "workspaces", tt.WorkspacePublicID)

	deps := lenses.Deps{
		DB:        testDB,
		Queries:   generated.New(testDB),
		Mutations: mutationlog.New(testDB, generated.New(testDB)),
	}

	var out *lenses.PublishLensOutput
	var handlerErr error
	runInWorkspaceContext(t, tt, func(ctx context.Context) {
		out, handlerErr = lenses.Publish(deps)(ctx, &lenses.PublishLensInput{
			WsID:   tt.WorkspacePublicID,
			LensID: lensID,
		})
	})

	require.NoError(t, handlerErr)
	require.NotNil(t, out)
	require.NotEmpty(t, out.Body.PublicToken, "a publish that landed must return its token")

	status, body := doJSONStatus(t, http.MethodGet, testServerURL+"/public/lenses/"+out.Body.PublicToken, "", nil)
	require.Equalf(t, http.StatusOK, status, "the minted token must open the public page: body=%s", string(body))

	require.Equal(t, 1, countRows(t, "SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'lens.shared'", wsID),
		"a publish that landed must append exactly one event")
	require.Equal(t, 1, countRows(t, "SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = 'lens.publish'", wsID),
		"a publish that landed must be audited exactly once")
}

// TestLensUnpublish_LosesTheRace_RefusesAndRecordsNothing is the mirror
// of the publish race: when another call revokes the share first, this
// one took nothing down, so it must refuse rather than report a
// revocation and record one.
func TestLensUnpublish_LosesTheRace_RefusesAndRecordsNothing(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	lensID := createLens(t, tt.WorkspacePublicID, tt.AccessToken, "Unrace View "+randomHex(4))
	wsID := internalID(t, "workspaces", tt.WorkspacePublicID)
	lensPub, err := types.Parse(lensID)
	require.NoError(t, err)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"
	var published struct {
		PublicToken string `json:"publicToken"`
	}
	doJSON(t, http.MethodPost, base+"/"+lensID+"/publish", tt.AccessToken, nil, &published)
	require.NotEmpty(t, published.PublicToken)

	var competed raceOutcome
	raceDB := &lensRaceDB{DB: testDB, marker: "is_public = TRUE", compete: func(ctx context.Context) {
		competed.affected, competed.err = generated.New(testDB).SetLensPrivate(ctx, generated.SetLensPrivateParams{
			WorkspaceID: wsID,
			PublicID:    lensPub,
		})
	}}

	// Only the handler's own statements go through the decorator: the
	// recorder reads the plain handle so the competing write is opened
	// exactly once, against the guarded UPDATE under test.
	deps := lenses.Deps{
		DB:        testDB,
		Queries:   generated.New(raceDB),
		Mutations: mutationlog.New(testDB, generated.New(testDB)),
	}

	var handlerErr error
	runInWorkspaceContext(t, tt, func(ctx context.Context) {
		_, handlerErr = lenses.Unpublish(deps)(ctx, &lenses.UnpublishLensInput{
			WsID:   tt.WorkspacePublicID,
			LensID: lensID,
		})
	})

	competed.requireWon(t, "unpublish")

	var prob *handlerutil.ProblemDetails
	require.ErrorAs(t, handlerErr, &prob, "an unpublish that revoked nothing must be reported as an error")
	require.Equal(t, "WS.LENS.ALREADY_PRIVATE", prob.Type,
		"losing the race reads the same as unpublishing a lens that is already private")

	require.Zero(t, countRows(t, "SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'lens.unshared'", wsID),
		"no event may claim a revocation this call did not perform")
	require.Zero(t, countRows(t, "SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = 'lens.unpublish'", wsID),
		"no audit row may claim a revocation this call did not perform")
}

// TestLensUnpublish_WinsTheRace_RevokesAndRecords is the control for the
// unpublish refusal: with no competing write the share is taken down, the
// URL stops resolving, and both records are written.
func TestLensUnpublish_WinsTheRace_RevokesAndRecords(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	lensID := createLens(t, tt.WorkspacePublicID, tt.AccessToken, "Unrace Control "+randomHex(4))
	wsID := internalID(t, "workspaces", tt.WorkspacePublicID)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"
	var published struct {
		PublicToken string `json:"publicToken"`
	}
	doJSON(t, http.MethodPost, base+"/"+lensID+"/publish", tt.AccessToken, nil, &published)

	deps := lenses.Deps{
		DB:        testDB,
		Queries:   generated.New(testDB),
		Mutations: mutationlog.New(testDB, generated.New(testDB)),
	}

	var handlerErr error
	runInWorkspaceContext(t, tt, func(ctx context.Context) {
		_, handlerErr = lenses.Unpublish(deps)(ctx, &lenses.UnpublishLensInput{
			WsID:   tt.WorkspacePublicID,
			LensID: lensID,
		})
	})
	require.NoError(t, handlerErr)

	status, _ := doJSONStatus(t, http.MethodGet, testServerURL+"/public/lenses/"+published.PublicToken, "", nil)
	require.Equal(t, http.StatusNotFound, status, "a revoked share URL must stop resolving")

	require.Equal(t, 1, countRows(t, "SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'lens.unshared'", wsID),
		"a revocation that landed must append exactly one event")
	require.Equal(t, 1, countRows(t, "SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = 'lens.unpublish'", wsID),
		"a revocation that landed must be audited exactly once")
}
