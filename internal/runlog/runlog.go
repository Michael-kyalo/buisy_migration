// Package runlog sets up structured logging and records each step of a run in
// buisy_nexus_staging.migration_runs.
package runlog

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewLogger(w io.Writer, format string) *slog.Logger {
	if format == "text" {
		return slog.New(slog.NewTextHandler(w, nil))
	}
	return slog.New(slog.NewJSONHandler(w, nil))
}

// NewRunID returns a random UUID v4 identifying one invocation.
func NewRunID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type Counts struct {
	Read     int64
	Written  int64
	Rejected int64
}

const (
	StatusOK     = "ok"
	StatusFailed = "failed"
	StatusDryRun = "dry_run"
)

type Recorder struct {
	PG    *pgxpool.Pool
	RunID string
	Env   string
	Since string // YYYY-MM-DD of a partial (--since) run; empty for a full run
	Limit int64  // --limit of a partial run; 0 for none
}

// Start inserts a running row for step/detail and returns its id. An empty
// detail marks the step's summary row.
func (r *Recorder) Start(ctx context.Context, step, detail string) (int64, error) {
	var id int64
	err := r.PG.QueryRow(ctx, `
		INSERT INTO buisy_nexus_staging.migration_runs (run_id, env, step, detail, since, row_limit)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, '')::date, nullif($6, 0))
		RETURNING id`, r.RunID, r.Env, step, detail, r.Since, r.Limit).Scan(&id)
	return id, err
}

// Finish closes a row. A non-nil runErr marks it failed; status overrides ok.
func (r *Recorder) Finish(ctx context.Context, id int64, c Counts, runErr error, status string) error {
	var errText *string
	if runErr != nil {
		s := runErr.Error()
		errText = &s
		status = StatusFailed
	} else if status == "" {
		status = StatusOK
	}
	_, err := r.PG.Exec(ctx, `
		UPDATE buisy_nexus_staging.migration_runs
		SET finished_at = now(), rows_read = $2, rows_written = $3, rows_rejected = $4,
		    status = $5, error = $6
		WHERE id = $1`, id, c.Read, c.Written, c.Rejected, status, errText)
	return err
}
