// Package source is the only way the tool talks to Buisy MySQL. It is
// read-only by construction: sessions are put in READ ONLY mode and the
// package exposes queries only, never Exec.
package source

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

type DB struct {
	db       *sql.DB
	Database string
}

func Open(ctx context.Context, dsn, database string) (*DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to buisy mysql: %w", err)
	}
	return &DB{db: db, Database: database}, nil
}

func (d *DB) Close() error { return d.db.Close() }

// Snapshot is a dedicated connection holding one read-only transaction with a
// consistent snapshot: every query on it sees the database at the same moment.
type Snapshot struct {
	conn *sql.Conn
}

func (d *DB) Snapshot(ctx context.Context) (*Snapshot, error) {
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	for _, stmt := range []string{
		"SET SESSION TRANSACTION READ ONLY",
		"SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ",
		"START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY",
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			conn.Close()
			return nil, fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return &Snapshot{conn: conn}, nil
}

func (s *Snapshot) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.conn.QueryContext(ctx, query, args...)
}

func (s *Snapshot) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.conn.QueryRowContext(ctx, query, args...)
}

// Close ends the snapshot transaction and returns the connection.
func (s *Snapshot) Close() error {
	_, _ = s.conn.ExecContext(context.Background(), "ROLLBACK")
	return s.conn.Close()
}
