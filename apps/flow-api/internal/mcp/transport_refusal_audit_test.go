package mcp

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// The workspace and user the stub token belongs to. The audit row has to
// land under this workspace and no other.
const (
	refusalWorkspaceID = 42
	refusalUserID      = 7
	refusalToken       = auth.PrefixMCP + "expired-or-throttled"
)

// TestExpiredTokenRefusalIsAudited covers the refusal an operator is
// least able to see any other way.
//
// An expired token is turned away before any tool is named, so nothing
// used to reach mcp_invocations — and what the workspace timeline showed
// was an agent that had simply stopped doing anything, which reads as an
// idle agent rather than one being refused every second. The token does
// name a workspace, a user and possibly an agent, so the refusal has
// somewhere to be recorded.
func TestExpiredTokenRefusalIsAudited(t *testing.T) {
	t.Parallel()

	stub := &refusalStub{expired: true}
	h, done := newRefusalHandler(t, stub)
	defer done()

	rec := postToolsList(h)
	require.Equal(t, apierrors.McpTokenExpired.Status, rec.Code)

	rows := stub.auditRows()
	require.Len(t, rows, 1, "an expired-token refusal must leave exactly one audit row")
	require.Equal(t, int64(refusalWorkspaceID), rows[0].workspaceID)
	require.Equal(t, string(generated.McpInvocationsStatusDenied), rows[0].status)
	require.Equal(t, apierrors.McpTokenExpired.Code, rows[0].errorCode)
	require.Empty(t, rows[0].toolName, "the refusal happened before a tool was dispatched")
}

// TestUnknownTokenRefusalIsNotAudited is the boundary of the rule above:
// a token that matches no row names no workspace, so there is no tenant
// to write the row under. Recording it anywhere would mean picking a
// workspace at random.
func TestUnknownTokenRefusalIsNotAudited(t *testing.T) {
	t.Parallel()

	stub := &refusalStub{unknown: true}
	h, done := newRefusalHandler(t, stub)
	defer done()

	rec := postToolsList(h)
	require.Equal(t, apierrors.McpTokenUnknown.Status, rec.Code)
	require.Empty(t, stub.auditRows())
}

// TestThrottleRefusalIsAuditedOncePerEpisode covers the other refusal
// that left no trace, and the reason it is recorded sparingly.
//
// A capped client keeps firing — that is what being capped looks like —
// so auditing every refusal would answer a cheap in-memory rejection
// with an unbounded stream of INSERTs, which is worse than the missing
// record. One row per episode says the same thing: this token was
// throttled, here, then.
func TestThrottleRefusalIsAuditedOncePerEpisode(t *testing.T) {
	t.Parallel()

	stub := &refusalStub{}
	h, done := newRefusalHandler(t, stub)
	defer done()

	// Spend the token's whole budget through the same hashed key the
	// handler uses, so the next request is the one that trips the cap.
	key := hashToken(refusalToken)
	for range h.rl.maxReqs {
		allowed, _, _ := h.rl.allow(key)
		require.True(t, allowed)
	}

	for range 3 {
		rec := postToolsList(h)
		require.Equal(t, apierrors.RateLimitExceeded.Status, rec.Code)
	}

	rows := stub.auditRows()
	require.Len(t, rows, 1,
		"a throttling episode must be recorded once, not once per refused request")
	require.Equal(t, int64(refusalWorkspaceID), rows[0].workspaceID)
	require.Equal(t, string(generated.McpInvocationsStatusDenied), rows[0].status)
	require.Equal(t, apierrors.RateLimitExceeded.Code, rows[0].errorCode)
}

// TestThrottleEpisodeRestartsAfterTheTokenIsAdmitted states why the
// episode is scoped to the cap rather than to the process: a token that
// recovers and is later capped again is a second incident and has to be
// recorded again, or the trail shows only the first time it ever
// happened.
func TestThrottleEpisodeRestartsAfterTheTokenIsAdmitted(t *testing.T) {
	t.Parallel()

	rl := newMCPRateLimiter()
	const key = "token"

	for range rl.maxReqs {
		rl.allow(key)
	}
	_, _, first := rl.allow(key)
	require.True(t, first, "the refusal that begins an episode is the one worth recording")
	_, _, again := rl.allow(key)
	require.False(t, again, "refusals inside the same episode must not each ask for a row")

	// Drain the window so the token is admitted again, which closes the
	// episode.
	rl.mu.Lock()
	rl.tokens[key].timestamps = nil
	rl.mu.Unlock()

	// The admitted request that closes the episode is itself the first of
	// the next window, so the budget has one fewer left in it.
	allowed, _, _ := rl.allow(key)
	require.True(t, allowed)
	for range rl.maxReqs - 1 {
		rl.allow(key)
	}
	_, _, reopened := rl.allow(key)
	require.True(t, reopened, "being capped a second time is a second incident")
}

// --- harness ----------------------------------------------------------

func newRefusalHandler(t *testing.T, stub *refusalStub) (*Handler, func()) {
	t.Helper()
	db := openRefusalStubDB(t, stub)
	h := NewHandler(Deps{DB: db, Queries: generated.New(db)})
	return h, func() { _ = db.Close() }
}

func postToolsList(h *Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+refusalToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- stub driver ------------------------------------------------------

// auditRow is the subset of an mcp_invocations INSERT this file asserts
// on, read straight off the statement's bind parameters.
type auditRow struct {
	workspaceID int64
	toolName    string
	status      string
	errorCode   string
}

type refusalStub struct {
	expired bool
	unknown bool

	mu   sync.Mutex
	rows []auditRow
}

func (s *refusalStub) auditRows() []auditRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]auditRow(nil), s.rows...)
}

func (s *refusalStub) Open(string) (driver.Conn, error) { return &refusalConn{s: s}, nil }

type refusalConn struct{ s *refusalStub }

func (c *refusalConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *refusalConn) Close() error                        { return nil }
func (c *refusalConn) Begin() (driver.Tx, error)           { return refusalTx{}, nil }

type refusalTx struct{}

func (refusalTx) Commit() error   { return nil }
func (refusalTx) Rollback() error { return nil }

// ExecContext records the audit INSERT by position. The column order is
// fixed by the LogMcpInvocation statement: public_id, workspace_id,
// user_id, agent_id, task_id, tool_name, arguments, result, status,
// error_code, duration_ms, invoked_at.
func (c *refusalConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(query, "INSERT INTO mcp_invocations") && len(args) >= 10 {
		c.s.mu.Lock()
		c.s.rows = append(c.s.rows, auditRow{
			workspaceID: asInt64(args[1].Value),
			toolName:    asString(args[5].Value),
			status:      asString(args[8].Value),
			errorCode:   asString(args[9].Value),
		})
		c.s.mu.Unlock()
	}
	return driver.RowsAffected(1), nil
}

func (c *refusalConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "FROM mcp_tokens") {
		return c.s.tokenRow(), nil
	}
	return &refusalRows{}, nil
}

// tokenRow answers FindUserForMcpToken. It returns a real, complete row
// rather than an error: the refusal under test comes from the token's
// expiry or from the limiter, and a stub that invented a failure would
// prove neither.
func (s *refusalStub) tokenRow() driver.Rows {
	if s.unknown {
		return &refusalRows{}
	}
	expires := time.Now().Add(time.Hour)
	if s.expired {
		expires = time.Now().Add(-time.Hour)
	}
	tokenPub := types.New()
	userPub := types.New()
	return &refusalRows{
		cols: []string{
			"token_id", "token_public_id", "workspace_id", "user_id", "agent_id",
			"scopes_json", "expires_at", "user_public_id", "email", "display_name",
		},
		n: 1,
		row: func(int) []driver.Value {
			return []driver.Value{
				int64(1),
				append([]byte(nil), tokenPub[:]...),
				int64(refusalWorkspaceID),
				int64(refusalUserID),
				nil,
				[]byte(`["read:workspace"]`),
				expires,
				append([]byte(nil), userPub[:]...),
				"agent@example.test",
				"Agent Owner",
			}
		},
	}
}

type refusalRows struct {
	cols []string
	row  func(i int) []driver.Value
	n    int
	i    int
}

func (r *refusalRows) Columns() []string { return r.cols }
func (r *refusalRows) Close() error      { return nil }

func (r *refusalRows) Next(dest []driver.Value) error {
	if r.i >= r.n {
		return io.EOF
	}
	r.i++
	copy(dest, r.row(r.i))
	return nil
}

func asInt64(v driver.Value) int64 {
	n, _ := v.(int64)
	return n
}

func asString(v driver.Value) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return ""
	}
}

var refusalStubSeq atomic.Uint64

func openRefusalStubDB(t *testing.T, s *refusalStub) *sql.DB {
	t.Helper()

	// database/sql keeps a process-wide driver registry, so each test
	// needs its own name to stay parallel-safe.
	name := "mcp-refusal-stub-" + time.Now().Format("150405.000000000") + "-" +
		string(rune('a'+refusalStubSeq.Add(1)%26))
	sql.Register(name, s)
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	return db
}
