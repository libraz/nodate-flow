package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
)

// TestConcurrentRegistrationLoserIsToldTheAddressIsTaken covers the
// answer given to whoever loses a sign-up race.
//
// The lookup that precedes the insert is advisory: two requests for the
// same address can both find nothing and both proceed, and only
// uniq_users_email decides which one exists. The loser was told the
// service had failed — which is wrong, unactionable, and hides the one
// fact they can use, that the account is already there and the next step
// is signing in. There is no way to close the window by looking harder
// first; the unique index is the decision, so the answer has to be read
// off it.
func TestConcurrentRegistrationLoserIsToldTheAddressIsTaken(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		on   string
	}{
		// Both unique keys are reachable: users.email decides first, and
		// identities(provider, subject) is the same race one step later.
		{"users.email", "INSERT INTO users"},
		{"identities.provider_subject", "INSERT INTO identities"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := openRegisterStubDB(t, &registerStub{duplicateOn: tc.on})
			defer db.Close()

			_, err := Register(Deps{
				Queries:          generated.New(db),
				RegistrationOpen: true,
			})(context.Background(), registerInput())

			var pd *handlerutil.ProblemDetails
			require.ErrorAs(t, err, &pd)
			require.NotEqual(t, apierrors.InternalUnexpected.Code, pd.Type,
				"losing a sign-up race is not a server fault and must not be reported as one")
			require.Equal(t, apierrors.AuthRegisterEmailAlreadyTaken.Code, pd.Type)
		})
	}
}

// TestRegistrationStillReportsARealInsertFailure is the boundary: only a
// unique-constraint violation means the address is taken. Any other
// driver failure is a fault on our side and must keep saying so, or a
// broken database starts telling every new user their address is
// already registered.
func TestRegistrationStillReportsARealInsertFailure(t *testing.T) {
	t.Parallel()

	db := openRegisterStubDB(t, &registerStub{failOn: "INSERT INTO users"})
	defer db.Close()

	_, err := Register(Deps{
		Queries:          generated.New(db),
		RegistrationOpen: true,
	})(context.Background(), registerInput())

	var pd *handlerutil.ProblemDetails
	require.ErrorAs(t, err, &pd)
	require.Equal(t, apierrors.InternalUnexpected.Code, pd.Type)
}

func registerInput() *RegisterInput {
	in := &RegisterInput{}
	in.Body.Email = "taken@example.test"
	in.Body.Password = "a-long-enough-password"
	in.Body.DisplayName = "Second Arrival"
	return in
}

// --- stub driver ------------------------------------------------------

// registerStub answers the two statements the register path reaches
// before it needs anything else: the advisory email lookup (which finds
// nothing, exactly as it does for the loser of the race) and the insert
// that the unique index refuses.
type registerStub struct {
	duplicateOn string
	failOn      string
}

func (s *registerStub) Open(string) (driver.Conn, error) { return &registerConn{s: s}, nil }

type registerConn struct{ s *registerStub }

func (c *registerConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *registerConn) Close() error                        { return nil }
func (c *registerConn) Begin() (driver.Tx, error)           { return registerTx{}, nil }

type registerTx struct{}

func (registerTx) Commit() error   { return nil }
func (registerTx) Rollback() error { return nil }

func (c *registerConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if c.s.failOn != "" && strings.Contains(query, c.s.failOn) {
		return nil, errors.New("connection lost")
	}
	if c.s.duplicateOn != "" && strings.Contains(query, c.s.duplicateOn) {
		// 1062 / ER_DUP_ENTRY is what InnoDB returns to the second
		// writer; the message shape is the driver's.
		return nil, &mysql.MySQLError{
			Number:  1062,
			Message: "Duplicate entry 'taken@example.test' for key 'uniq_users_email'",
		}
	}
	return insertResult{}, nil
}

// insertResult stands in for a successful INSERT. driver.RowsAffected
// refuses LastInsertId, and RegisterUser reads it — without a real id
// the handler would fail before the identity insert it is being driven
// towards.
type insertResult struct{}

func (insertResult) LastInsertId() (int64, error) { return 1, nil }
func (insertResult) RowsAffected() (int64, error) { return 1, nil }

func (c *registerConn) QueryContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	// The advisory lookup finds nothing — which is precisely the state
	// both racing requests observe.
	return &registerRows{}, nil
}

type registerRows struct{}

func (r *registerRows) Columns() []string         { return nil }
func (r *registerRows) Close() error              { return nil }
func (r *registerRows) Next([]driver.Value) error { return io.EOF }

var registerStubSeq atomic.Uint64

func openRegisterStubDB(t *testing.T, s *registerStub) *sql.DB {
	t.Helper()

	// database/sql keeps a process-wide driver registry, so each test
	// needs its own name to stay parallel-safe.
	name := "auth-register-stub-" + time.Now().Format("150405.000000000") + "-" +
		string(rune('a'+registerStubSeq.Add(1)%26))
	sql.Register(name, s)
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	return db
}
