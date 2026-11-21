package calendaroccurrence

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// fakeDriver answers the statements CancelOverride issues and records the
// exception list it writes back.
type fakeDriver struct {
	mu           sync.Mutex
	masterAllDay bool
	written      string
}

type fakeConn struct{ d *fakeDriver }
type fakeStmt struct {
	c *fakeConn
	q string
}
type fakeRows struct {
	cols []string
	row  []driver.Value
	done bool
}

func (d *fakeDriver) Open(string) (driver.Conn, error)    { return &fakeConn{d}, nil }
func (c *fakeConn) Prepare(q string) (driver.Stmt, error) { return &fakeStmt{c, q}, nil }
func (c *fakeConn) Close() error                          { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)             { return nil, io.EOF }
func (s *fakeStmt) Close() error                          { return nil }
func (s *fakeStmt) NumInput() int                         { return -1 }

func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.c.d.mu.Lock()
	defer s.c.d.mu.Unlock()
	if strings.Contains(s.q, "recurrence_exceptions") {
		switch v := args[0].(type) {
		case []byte:
			s.c.d.written = string(v)
		case string:
			s.c.d.written = v
		}
	}
	return driver.RowsAffected(1), nil
}

func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	switch {
	case strings.Contains(s.q, "-- name: FindCalendarEventRecurrenceLink"):
		return &fakeRows{
			cols: []string{"p", "o", "r"},
			row:  []driver.Value{int64(7), time.Date(2030, 3, 5, 0, 0, 0, 0, time.UTC), []byte(`{"freq":"daily"}`)},
		}, nil
	case strings.Contains(s.q, "-- name: LockCalendarEventSeries"):
		return &fakeRows{
			cols: []string{"id", "r", "e", "x"},
			row:  []driver.Value{int64(7), []byte(`{"freq":"daily"}`), nil, []byte(`[]`)},
		}, nil
	case strings.Contains(s.q, "-- name: FindCalendarEventAllDay"):
		return &fakeRows{cols: []string{"all_day"}, row: []driver.Value{s.c.d.masterAllDay}}, nil
	}
	return nil, io.EOF
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(dest, r.row)
	return nil
}

// The exception goes into the master's list, so it is spelled by the
// master's shape whatever the cancelled override's own all_day says.
func TestCancelOverrideSpellsTheEntryByTheMasterShape(t *testing.T) {
	cases := []struct {
		name         string
		masterAllDay bool
		rowAllDay    bool
		want         string
	}{
		{"all-day master, timed override", true, false, `["2030-03-05"]`},
		{"timed master, all-day override", false, true, `["2030-03-05T00:00:00Z"]`},
	}
	for i, c := range cases {
		fd := &fakeDriver{masterAllDay: c.masterAllDay}
		name := "cancel-override-fake-" + string(rune('a'+i))
		sql.Register(name, fd)
		db, err := sql.Open(name, "")
		if err != nil {
			t.Fatal(err)
		}
		q := Queries{Calendar: calendar.New(db)}
		row := calendar.FindCalendarEventByPublicIdRow{
			PublicID:           types.New(),
			AllDay:             c.rowAllDay,
			RecurrenceParentID: sql.NullInt32{Int32: 7, Valid: true},
		}
		if err := CancelOverride(context.Background(), q, 1, row); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if fd.written != c.want {
			t.Errorf("%s: wrote %s, want %s", c.name, fd.written, c.want)
		}
		_ = db.Close()
	}
}
