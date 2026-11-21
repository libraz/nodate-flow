package calendaroccurrence

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
)

type attRow struct {
	event, user int64
	enabled     bool
}

// attendeeStore is an in-memory calendar_event_attendees behind
// database/sql, answering only the statements OwnAttendees and the removal
// path issue.
type attendeeStore struct{ rows []attRow }

type insertResult struct{ id int64 }

func (r insertResult) LastInsertId() (int64, error) { return r.id, nil }
func (r insertResult) RowsAffected() (int64, error) { return 1, nil }

type attConn struct{ s *attendeeStore }
type attStmt struct {
	s *attendeeStore
	q string
}
type attRows struct {
	cols []string
	data [][]driver.Value
	i    int
}

func (s *attendeeStore) Open(string) (driver.Conn, error) { return &attConn{s}, nil }
func (c *attConn) Prepare(q string) (driver.Stmt, error)  { return &attStmt{c.s, q}, nil }
func (c *attConn) Close() error                           { return nil }
func (c *attConn) Begin() (driver.Tx, error)              { return nil, io.EOF }
func (st *attStmt) Close() error                          { return nil }
func (st *attStmt) NumInput() int                         { return -1 }

func (st *attStmt) Exec(a []driver.Value) (driver.Result, error) {
	s := st.s
	switch {
	case strings.Contains(st.q, "-- name: CreateCalendarEventAttendee"):
		s.rows = append(s.rows, attRow{a[2].(int64), a[3].(int64), true})
		return driver.Result(insertResult{int64(len(s.rows))}), nil
	case strings.Contains(st.q, "-- name: DisableCalendarEventAttendee"):
		for i := range s.rows {
			if s.rows[i].event == a[0].(int64) && s.rows[i].user == a[1].(int64) {
				s.rows[i].enabled = false
			}
		}
	default:
		return nil, io.EOF
	}
	return driver.RowsAffected(1), nil
}

func (st *attStmt) Query(a []driver.Value) (driver.Rows, error) {
	s := st.s
	switch {
	case strings.Contains(st.q, "-- name: HasCalendarEventAttendeeRows"):
		has := int64(0)
		for _, r := range s.rows {
			if r.event == a[0].(int64) {
				has = 1
			}
		}
		return &attRows{cols: []string{"has_rows"}, data: [][]driver.Value{{has}}}, nil
	case strings.Contains(st.q, "-- name: ListCalendarEventAttendees"):
		out := &attRows{cols: []string{"id", "pid", "uid", "rsvp", "can_edit", "upid", "name", "avatar", "avatar_obj", "created"}}
		for _, r := range s.rows {
			if r.event == a[0].(int64) && r.enabled {
				out.data = append(out.data, []driver.Value{
					int64(1), make([]byte, 16), r.user, "accepted", false,
					make([]byte, 16), "n", nil, nil, time.Unix(0, 0).UTC(),
				})
			}
		}
		return out, nil
	}
	return nil, io.EOF
}

func (r *attRows) Columns() []string { return r.cols }
func (r *attRows) Close() error      { return nil }
func (r *attRows) Next(d []driver.Value) error {
	if r.i >= len(r.data) {
		return io.EOF
	}
	copy(d, r.data[r.i])
	r.i++
	return nil
}

func enabledUsers(s *attendeeStore, event int64) []int64 {
	var out []int64
	for _, r := range s.rows {
		if r.event == event && r.enabled {
			out = append(out, r.user)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Taking one person off an occurrence leaves the others attending it, and
// leaves the series' own list as it was.
func TestRemovingOneAttendeeFromAnOccurrenceKeepsTheOthers(t *testing.T) {
	const series, override = 10, 20
	store := &attendeeStore{rows: []attRow{{series, 1, true}, {series, 2, true}, {series, 3, true}}}
	sql.Register("own-attendees-fake", store)
	db, err := sql.Open("own-attendees-fake", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := calendar.New(db)
	ctx := context.Background()

	if err := OwnAttendees(ctx, q, 1, override, series); err != nil {
		t.Fatal(err)
	}
	if err := q.DisableCalendarEventAttendee(ctx, calendar.DisableCalendarEventAttendeeParams{
		EventID: nullID(override), UserID: 2,
	}); err != nil {
		t.Fatal(err)
	}

	if got := enabledUsers(store, override); !equal(got, []int64{1, 3}) {
		t.Errorf("occurrence attendees = %v, want [1 3]", got)
	}
	if got := enabledUsers(store, series); !equal(got, []int64{1, 2, 3}) {
		t.Errorf("series attendees = %v, want [1 2 3]", got)
	}

	// A second call leaves the override's rows alone.
	if err := OwnAttendees(ctx, q, 1, override, series); err != nil {
		t.Fatal(err)
	}
	if got := enabledUsers(store, override); !equal(got, []int64{1, 3}) {
		t.Errorf("after a second call, occurrence attendees = %v, want [1 3]", got)
	}
}
