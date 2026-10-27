// Package target holds connection-level helpers for the Nexus Postgres database.
package target

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
)

// lockKey is the advisory lock that stops two runs migrating the same database at once.
const lockKey int64 = 0x6275697379 // "buisy"

func Connect(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to target postgres: %w", err)
	}
	return pool, nil
}

// VerifyDatabase catches a connection string for one env pasted into another's variable.
func VerifyDatabase(ctx context.Context, pool *pgxpool.Pool, expected string) error {
	var got string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&got); err != nil {
		return err
	}
	if got != expected {
		return fmt.Errorf("connected to database %q, expected %q", got, expected)
	}
	return nil
}

// Lock takes a session advisory lock on a dedicated connection for the life of the run.
func Lock(ctx context.Context, pool *pgxpool.Pool) (release func(), err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockKey).Scan(&ok); err != nil {
		conn.Release()
		return nil, err
	}
	if !ok {
		conn.Release()
		return nil, errors.New("another migration run holds the lock on this database")
	}
	return func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey)
		conn.Release()
	}, nil
}

// Bootstrap creates the buisy_nexus_staging schema and control tables.
func Bootstrap(ctx context.Context, pool *pgxpool.Pool, sqlFS fs.FS) error {
	b, err := fs.ReadFile(sqlFS, "sql/bootstrap.sql")
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Conn().PgConn().Exec(ctx, string(b)).ReadAll()
	return err
}
