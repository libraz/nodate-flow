package calendars

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// TestAcceptInviteCostDoesNotGrowWithAttendees is the behavioural half of
// the guard on this handler.
//
// calendar_event_invites.attendee_id is an internal FK, and the attendee
// list query used not to return it, so the handler recovered it by
// asking the database about each listed attendee in turn. On an
// all-hands event that is a few hundred round trips for one request —
// on the *unauthenticated* accept endpoint, which anyone holding a link
// can drive as often as they like.
//
// What makes that a defect is the shape of the growth, not any single
// timing, so the assertion is the shape: the same request against five
// attendees and against three hundred must cost the database the same
// number of statements. Counting them needs a database, and the property
// is about the count rather than the data, so the handler is driven
// against a stub driver that records what it was asked.
//
// invite_attendee_lookup_test.go states the same rule syntactically.
// This one holds if the loop is disguised — moved into a helper, hidden
// behind a condition — because it never looks at the source.
func TestAcceptInviteCostDoesNotGrowWithAttendees(t *testing.T) {
	t.Parallel()

	small := acceptInviteStatementCount(t, 5)
	large := acceptInviteStatementCount(t, 300)

	require.Equal(t, small, large,
		"accepting an invite must cost the same whether the event has 5 attendees or 300; "+
			"a per-attendee lookup makes an unauthenticated request scale with the guest list")
	require.LessOrEqual(t, large, 6,
		"the accept path is two reads, two writes and a transaction; got %d statements", large)
}

// acceptInviteStatementCount runs one accept against an event with n
// attendees and returns how many statements the handler issued.
func acceptInviteStatementCount(t *testing.T, n int) int {
	t.Helper()

	// The invite points at the *last* attendee, so a handler that stops
	// at the first row it can explain does not accidentally pass.
	stub := &inviteStub{attendees: n, attendeeID: uint32(n)} //#nosec G115 -- n is a test constant
	db := openStubDB(t, stub)
	defer db.Close()

	deps := Deps{DB: db, CalendarQueries: calendar.New(db)}
	in := &AcceptEventInviteInput{}
	in.Body.Token = "token"
	in.Body.Rsvp = "accepted"

	_, err := AcceptEventInvite(deps)(context.Background(), in)
	require.NoError(t, err)

	return stub.count()
}

// --- stub driver ------------------------------------------------------

type inviteStub struct {
	attendees  int
	attendeeID uint32

	mu         sync.Mutex
	statements []string
}

func (s *inviteStub) record(q string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statements = append(s.statements, q)
}

func (s *inviteStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.statements)
}

func (s *inviteStub) Open(string) (driver.Conn, error) { return &inviteConn{s: s}, nil }

type inviteConn struct{ s *inviteStub }

func (c *inviteConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *inviteConn) Close() error                        { return nil }
func (c *inviteConn) Begin() (driver.Tx, error)           { return inviteTx{}, nil }

type inviteTx struct{}

func (inviteTx) Commit() error   { return nil }
func (inviteTx) Rollback() error { return nil }

// The exec side is recorded too: the count under test is of statements,
// and a per-attendee write would be as costly as a per-attendee read.
func (c *inviteConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.s.record(query)
	return driver.RowsAffected(1), nil
}

func (c *inviteConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.s.record(query)
	switch {
	case strings.Contains(query, "FROM calendar_event_invites"):
		return c.s.inviteRow(), nil
	case strings.Contains(query, "INNER JOIN users u"):
		return c.s.attendeeRows(), nil
	case strings.Contains(query, "FROM calendar_event_attendees"):
		// The per-attendee lookup. It is answered rather than refused so
		// that a handler which reintroduces it still completes, and the
		// test fails on the statement count — the actual defect — rather
		// than on an error the stub invented.
		return c.s.singleAttendeeRow(args), nil
	default:
		return &stubRows{}, nil
	}
}

func (s *inviteStub) inviteRow() driver.Rows {
	pub := types.New()
	return &stubRows{
		cols: []string{
			"id", "public_id", "workspace_id", "calendar_id", "event_id", "attendee_id",
			"email", "token_hash", "expires_at", "accepted_at", "sent_at",
			"notes", "enabled", "updated_at", "created_at",
		},
		n: 1,
		row: func(int) []driver.Value {
			return []driver.Value{
				int64(1), append([]byte(nil), pub[:]...), int64(1), int64(1), int64(1), int64(s.attendeeID),
				"invitee@example.test", []byte("hash"), time.Now().Add(time.Hour), nil, nil,
				nil, true, nil, time.Now(),
			}
		},
	}
}

func (s *inviteStub) attendeeRows() driver.Rows {
	return &stubRows{
		cols: []string{
			"id", "public_id", "user_id", "rsvp", "can_edit",
			"user_public_id", "display_name", "avatar_url", "created_at",
		},
		n: s.attendees,
		row: func(i int) []driver.Value {
			pub := types.New()
			user := types.New()
			return []driver.Value{
				int64(i), append([]byte(nil), pub[:]...), int64(i), []byte("pending"), false,
				append([]byte(nil), user[:]...), "Attendee", nil, time.Now(),
			}
		},
	}
}

// singleAttendeeRow answers FindCalendarEventAttendee(event_id, user_id).
// The stub seeds attendee i with user_id i, so echoing the requested
// user id back as the row id keeps the two lookups consistent.
func (s *inviteStub) singleAttendeeRow(args []driver.NamedValue) driver.Rows {
	var userID int64
	if len(args) > 0 {
		if v, ok := args[len(args)-1].Value.(int64); ok {
			userID = v
		}
	}
	return &stubRows{
		cols: []string{"id", "rsvp", "can_edit", "enabled"},
		n:    1,
		row: func(int) []driver.Value {
			return []driver.Value{userID, []byte("pending"), false, true}
		},
	}
}

type stubRows struct {
	cols []string
	row  func(i int) []driver.Value
	n    int
	i    int
}

func (r *stubRows) Columns() []string { return r.cols }
func (r *stubRows) Close() error      { return nil }

func (r *stubRows) Next(dest []driver.Value) error {
	if r.i >= r.n {
		return io.EOF
	}
	r.i++
	copy(dest, r.row(r.i))
	return nil
}

var inviteStubSeq atomic.Uint64

func openStubDB(t *testing.T, s *inviteStub) *sql.DB {
	t.Helper()

	// database/sql keeps a process-wide driver registry, so each test
	// needs its own name to stay parallel-safe.
	name := "calendars-invite-stub-" + time.Now().Format("150405.000000000") + "-" +
		string(rune('a'+inviteStubSeq.Add(1)%26))
	sql.Register(name, s)
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	return db
}
