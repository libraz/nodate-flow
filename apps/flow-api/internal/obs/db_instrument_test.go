package obs

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeConnector struct{ failQuery bool }

func (f fakeConnector) Connect(context.Context) (driver.Conn, error) {
	return &fakeConn{failQuery: f.failQuery}, nil
}
func (fakeConnector) Driver() driver.Driver { return nil }

type fakeConn struct{ failQuery bool }

func (*fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (*fakeConn) Close() error                        { return nil }
func (*fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (*fakeConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.ResultNoRows, nil
}
func (c *fakeConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.failQuery {
		return nil, errors.New("boom")
	}
	return nil, errors.New("unused")
}

func TestInstrumentedConnRecordsQueriesBySqlcName(t *testing.T) {
	const okName, errName = "InstrumentTestExec", "InstrumentTestQuery"
	execBefore := testutil.ToFloat64(dbQueriesTotal.WithLabelValues(okName, "ok"))
	errBefore := testutil.ToFloat64(dbQueriesTotal.WithLabelValues(errName, "error"))

	db := sql.OpenDB(WrapConnector(fakeConnector{failQuery: true}))
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec("-- name: " + okName + " :exec\nUPDATE t SET a = 1"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if _, err := db.Query("-- name: " + errName + " :many\nSELECT 1"); err == nil {
		t.Fatal("query: want error")
	}

	if got := testutil.ToFloat64(dbQueriesTotal.WithLabelValues(okName, "ok")) - execBefore; got != 1 {
		t.Errorf("ok series for %s grew by %v, want 1", okName, got)
	}
	if got := testutil.ToFloat64(dbQueriesTotal.WithLabelValues(errName, "error")) - errBefore; got != 1 {
		t.Errorf("error series for %s grew by %v, want 1", errName, got)
	}
}

func TestQueryName(t *testing.T) {
	cases := map[string]string{
		"-- name: ListTasks :many\nSELECT 1": "ListTasks",
		"-- name: Solo":                      "Solo",
		"SELECT 1":                           unnamedQuery,
		"-- name: ":                          unnamedQuery,
	}
	for in, want := range cases {
		if got := queryName(in); got != want {
			t.Errorf("queryName(%q) = %q, want %q", in, got, want)
		}
	}
}
