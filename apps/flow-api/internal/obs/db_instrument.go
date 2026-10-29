package obs

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// unnamedQuery labels statements that carry no sqlc "-- name:" header.
const unnamedQuery = "other"

// OpenInstrumentedMySQL opens a MySQL pool whose every statement, inside or
// outside a transaction, is recorded through RecordDBQuery under its sqlc
// operation name.
func OpenInstrumentedMySQL(dsn string) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	base, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(WrapConnector(base)), nil
}

// WrapConnector decorates a driver connector so each Exec, Query and
// prepared-statement execution is timed and counted.
func WrapConnector(base driver.Connector) driver.Connector {
	return instrumentedConnector{base: base}
}

// queryName extracts the operation name from the "-- name: Foo :one" header
// sqlc keeps at the top of every generated query string.
func queryName(query string) string {
	rest, ok := strings.CutPrefix(query, "-- name: ")
	if !ok {
		return unnamedQuery
	}
	name, _, _ := strings.Cut(rest, " ")
	name, _, _ = strings.Cut(name, "\n")
	if name == "" {
		return unnamedQuery
	}
	return name
}

type instrumentedConnector struct{ base driver.Connector }

func (c instrumentedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &instrumentedConn{Conn: conn}, nil
}

func (c instrumentedConnector) Driver() driver.Driver { return c.base.Driver() }

type instrumentedConn struct{ driver.Conn }

func (c *instrumentedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	res, err := execer.ExecContext(ctx, query, args)
	recordUnlessSkipped(queryName(query), start, err)
	return res, err
}

func (c *instrumentedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	rows, err := queryer.QueryContext(ctx, query, args)
	recordUnlessSkipped(queryName(query), start, err)
	return rows, err
}

func (c *instrumentedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var (
		stmt driver.Stmt
		err  error
	)
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err = p.PrepareContext(ctx, query)
	} else {
		stmt, err = c.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &instrumentedStmt{Stmt: stmt, name: queryName(query)}, nil
}

func (c *instrumentedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin() //nolint:staticcheck // fallback for drivers without ConnBeginTx
}

func (c *instrumentedConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *instrumentedConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *instrumentedConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// CheckNamedValue defers argument conversion to the wrapped driver, which
// knows types (unsigned integers, JSON) the default converter would reject.
func (c *instrumentedConn) CheckNamedValue(nv *driver.NamedValue) error {
	if ch, ok := c.Conn.(driver.NamedValueChecker); ok {
		return ch.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

type instrumentedStmt struct {
	driver.Stmt
	name string
}

func (s *instrumentedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	start := time.Now()
	var (
		res driver.Result
		err error
	)
	if e, ok := s.Stmt.(driver.StmtExecContext); ok {
		res, err = e.ExecContext(ctx, args)
	} else {
		res, err = s.Stmt.Exec(namedToValues(args)) //nolint:staticcheck // fallback for drivers without StmtExecContext
	}
	RecordDBQuery(s.name, time.Since(start), err)
	return res, err
}

func (s *instrumentedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	start := time.Now()
	var (
		rows driver.Rows
		err  error
	)
	if q, ok := s.Stmt.(driver.StmtQueryContext); ok {
		rows, err = q.QueryContext(ctx, args)
	} else {
		rows, err = s.Stmt.Query(namedToValues(args)) //nolint:staticcheck // fallback for drivers without StmtQueryContext
	}
	RecordDBQuery(s.name, time.Since(start), err)
	return rows, err
}

func (s *instrumentedStmt) CheckNamedValue(nv *driver.NamedValue) error {
	if ch, ok := s.Stmt.(driver.NamedValueChecker); ok {
		return ch.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

// recordUnlessSkipped records a completed statement. driver.ErrSkip means
// the driver declined the fast path and database/sql will re-run the
// statement through Prepare, which records it instead.
func recordUnlessSkipped(name string, start time.Time, err error) {
	if errors.Is(err, driver.ErrSkip) {
		return
	}
	RecordDBQuery(name, time.Since(start), err)
}

func namedToValues(args []driver.NamedValue) []driver.Value {
	out := make([]driver.Value, len(args))
	for i, a := range args {
		out[i] = a.Value
	}
	return out
}
