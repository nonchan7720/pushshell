//go:build js && wasm

package main

import (
	"context"
	"database/sql/driver"
	"errors"
	"time"

	"github.com/syumai/workers-go/cloudflare/d1"
)

// This file adapts github.com/syumai/workers-go/cloudflare/d1 so time.Time
// query arguments and results round-trip correctly, which the D1 driver
// cannot do on its own.
//
// driver.Value legitimately includes time.Time (see the database/sql/driver
// package docs), and ent's SQLite dialect relies on exactly that: every
// created_at/updated_at field (internal/ent/schema's field.Time, see
// device.go/device_login.go) is sent and scanned as a plain time.Time. But
// d1's Stmt hands query arguments to JS via syscall/js.Value.Call
// (cloudflare/d1/stmt.go), and js.ValueOf panics on anything outside Go's
// small set of JS-representable kinds (bool/int*/uint*/float*/string/
// []byte/nil) — time.Time is not one of them. Left unhandled, every
// Device/DeviceLogin write panics inside the D1 driver (confirmed with
// `mise run worker:dev` + `POST /v1/devices`: "syscall/js: ValueOf: invalid
// value" from cloudflare/d1/stmt.go's QueryContext, called through
// entgo.io/ent/dialect/sql/sqlgraph's insertLastID/RETURNING-based insert).
//
// The fix: convert time.Time -> a string (timeLayout, UTC) right before it
// would reach d1's Stmt, and parse it back on the way out, so
// internal/ent's generated code (device.go's assignValues, which scans into
// sql.NullTime) keeps seeing a real time.Time and is itself untouched.
// openTimeSafeD1Connector wraps d1.OpenConnector, in place of calling it
// directly, everywhere this package opens D1.

// timeLayout is the wire format time.Time values round-trip through D1 as.
// Nothing in this backend filters or sorts by created_at/updated_at in SQL
// (internal/handler only ever returns them verbatim — see toAPIDevice in
// internal/handler/handler.go), so RFC3339Nano's imperfect lexicographic
// ordering (variable fractional-second digit count) does not matter here;
// it parses back unambiguously, which is what's required.
const timeLayout = time.RFC3339Nano

// openTimeSafeD1Connector is a drop-in replacement for d1.OpenConnector(name)
// whose Conns convert time.Time query arguments/results (see package doc).
func openTimeSafeD1Connector(name string) (driver.Connector, error) {
	c, err := d1.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return &timeConnector{Connector: c}, nil
}

type timeConnector struct {
	driver.Connector
}

func (c *timeConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &timeConn{Conn: conn}, nil
}

type timeConn struct {
	driver.Conn
}

var (
	_ driver.NamedValueChecker  = (*timeConn)(nil)
	_ driver.ConnPrepareContext = (*timeConn)(nil)
)

// CheckNamedValue converts a time.Time argument to a timeLayout-formatted
// string (see package doc); every other value is left to database/sql's own
// default conversion by returning driver.ErrSkip (driverArgsConnLocked in
// Go's database/sql/convert.go falls through to defaultCheckNamedValue,
// i.e. driver.DefaultParameterConverter, on ErrSkip).
func (c *timeConn) CheckNamedValue(nv *driver.NamedValue) error {
	if t, ok := nv.Value.(time.Time); ok {
		nv.Value = t.UTC().Format(timeLayout)
		return nil
	}
	return driver.ErrSkip
}

// PrepareContext wraps the resulting Stmt in timeStmt, so QueryContext's
// returned Rows can convert timestamp columns back on the way out.
func (c *timeConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var (
		stmt driver.Stmt
		err  error
	)
	if pc, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err = pc.PrepareContext(ctx, query)
	} else {
		stmt, err = c.Conn.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &timeStmt{Stmt: stmt}, nil
}

// Prepare mirrors PrepareContext for callers that only have the
// non-context driver.Conn.Prepare (database/sql falls back to it when a
// Conn does not implement driver.ConnPrepareContext; d1.Conn does not).
func (c *timeConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &timeStmt{Stmt: stmt}, nil
}

type timeStmt struct {
	driver.Stmt
}

var (
	_ driver.StmtQueryContext = (*timeStmt)(nil)
	_ driver.StmtExecContext  = (*timeStmt)(nil)
)

func (s *timeStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, errors.New("worker: underlying D1 statement does not support QueryContext")
	}
	rows, err := qc.QueryContext(ctx, args)
	if err != nil {
		return nil, err
	}
	return &timeRows{Rows: rows}, nil
}

func (s *timeStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := s.Stmt.(driver.StmtExecContext)
	if !ok {
		return nil, errors.New("worker: underlying D1 statement does not support ExecContext")
	}
	return ec.ExecContext(ctx, args)
}

// timeRows converts any column value that round-trips as a timeLayout
// string back into time.Time, so it reaches ent's generated scanning code
// (sql.NullTime.Scan -> database/sql's convertAssign, which assigns
// time.Time -> *time.Time directly but does not itself parse a string into
// one) as the type it expects.
type timeRows struct {
	driver.Rows
}

func (r *timeRows) Next(dest []driver.Value) error {
	if err := r.Rows.Next(dest); err != nil {
		return err
	}
	for i, v := range dest {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if t, err := time.Parse(timeLayout, s); err == nil {
			dest[i] = t
		}
	}
	return nil
}
