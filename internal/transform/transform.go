// Package transform runs transforms/*.sql against the target, in file-name
// order, each file in its own transaction. The SQL does all reshaping; this
// package only sequences files, sets run context and records outcomes.
package transform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"strconv"
	"time"

	"github.com/burnmfg/buisy-migration/internal/report"
	"github.com/burnmfg/buisy-migration/internal/runlog"
	"github.com/burnmfg/buisy-migration/internal/sqlfiles"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runner runs transforms/common plus transforms/<Target> against one database.
type Runner struct {
	Target       string   // target database name; selects transforms/<Target>
	TargetTables []string // tables the transforms write to; used for dry-run reporting
	PG           *pgxpool.Pool
	SQL          fs.FS
	Rec          *runlog.Recorder
	Log          *slog.Logger
	Out          io.Writer
	DryRun       bool
}

func (r *Runner) Run(ctx context.Context) error {
	files, err := sqlfiles.ListMerged(r.SQL, "transforms/common", "transforms/"+r.Target)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no SQL files in transforms/common or transforms/%s", r.Target)
	}
	if r.DryRun {
		return r.dryRun(ctx, files)
	}

	summaryID, err := r.Rec.Start(ctx, "transform", "")
	if err != nil {
		return err
	}
	var total runlog.Counts
	var runErr error
	for _, f := range files {
		c, err := r.runFile(ctx, f)
		total.Written += c.Written
		total.Rejected += c.Rejected
		if err != nil {
			runErr = err
			break
		}
	}
	if err := r.Rec.Finish(context.WithoutCancel(ctx), summaryID, total, runErr, ""); err != nil {
		r.Log.Error("record transform run", "err", err)
	}
	return runErr
}

func (r *Runner) runFile(ctx context.Context, f sqlfiles.File) (runlog.Counts, error) {
	started := time.Now()
	id, err := r.Rec.Start(ctx, "transform", f.Name)
	if err != nil {
		return runlog.Counts{}, err
	}
	c, err := r.execFile(ctx, f)
	if ferr := r.Rec.Finish(context.WithoutCancel(ctx), id, c, err, ""); ferr != nil {
		r.Log.Error("record transform file", "file", f.Name, "err", ferr)
	}
	if err != nil {
		r.Log.Error("transform failed; its transaction was rolled back", "file", f.Name, "err", err)
		return c, err
	}
	r.Log.Info("transform applied", "file", f.Name, "rows_written", c.Written, "rows_rejected", c.Rejected,
		"duration", time.Since(started).String())
	return c, nil
}

func (r *Runner) execFile(ctx context.Context, f sqlfiles.File) (runlog.Counts, error) {
	tx, err := r.PG.Begin(ctx)
	if err != nil {
		return runlog.Counts{}, err
	}
	defer tx.Rollback(ctx)
	c, err := r.execInTx(ctx, tx, f)
	if err != nil {
		return c, err
	}
	return c, tx.Commit(ctx)
}

// execInTx runs every statement in the file and counts rows written and rejects added.
func (r *Runner) execInTx(ctx context.Context, tx pgx.Tx, f sqlfiles.File) (runlog.Counts, error) {
	var c runlog.Counts
	if _, err := tx.Exec(ctx, "SELECT set_config('migrate.run_id', $1, true), set_config('migrate.env', $2, true)",
		r.Rec.RunID, r.Rec.Env); err != nil {
		return c, err
	}
	rejectsBefore, err := countRejects(ctx, tx, r.Rec.RunID)
	if err != nil {
		return c, err
	}
	results, err := tx.Conn().PgConn().Exec(ctx, f.SQL).ReadAll()
	if err != nil {
		return c, describe(f.Path(), err)
	}
	for i, res := range results {
		tag := res.CommandTag
		r.Log.Debug("statement", "file", f.Name, "n", i+1, "tag", tag.String())
		if tag.Insert() || tag.Update() || tag.Delete() {
			c.Written += tag.RowsAffected()
		}
	}
	rejectsAfter, err := countRejects(ctx, tx, r.Rec.RunID)
	if err != nil {
		return c, err
	}
	c.Rejected = rejectsAfter - rejectsBefore
	return c, nil
}

func countRejects(ctx context.Context, tx pgx.Tx, runID string) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, "SELECT count(*) FROM buisy_nexus_staging.migration_rejects WHERE run_id = $1", runID).Scan(&n)
	return n, err
}

// describe adds the file name and Postgres detail/hint/position to an error.
func describe(file string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		msg := fmt.Sprintf("%s: %s (SQLSTATE %s)", file, pgErr.Message, pgErr.Code)
		if pgErr.Detail != "" {
			msg += "; detail: " + pgErr.Detail
		}
		if pgErr.Hint != "" {
			msg += "; hint: " + pgErr.Hint
		}
		if pgErr.Where != "" {
			msg += "; where: " + pgErr.Where
		}
		if pgErr.Position > 0 {
			msg += "; at character " + strconv.Itoa(int(pgErr.Position))
		}
		return errors.New(msg)
	}
	return fmt.Errorf("%s: %w", file, err)
}

// dryRun runs every file inside one transaction and rolls it back. One outer
// transaction (not one per file) is needed so later files see earlier files'
// writes, as they would in a real run.
func (r *Runner) dryRun(ctx context.Context, files []sqlfiles.File) error {
	started := time.Now()
	tx, err := r.PG.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	before := map[string]int64{}
	for _, t := range r.TargetTables {
		var n int64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{t}.Sanitize()).Scan(&n); err != nil {
			return fmt.Errorf("count %s: %w", t, err)
		}
		before[t] = n
	}

	var total runlog.Counts
	for _, f := range files {
		c, err := r.execInTx(ctx, tx, f)
		if err != nil {
			return fmt.Errorf("dry run: %w", err)
		}
		total.Written += c.Written
		total.Rejected += c.Rejected
		r.Log.Info("dry run: transform executed", "file", f.Name, "rows_written", c.Written, "rows_rejected", c.Rejected)
	}

	// Rows touched by this transaction carry its xid in xmin; new rows are the
	// growth in count, the rest of the touched rows are updates.
	changes := report.Table{Columns: []string{"table", "before", "after", "would_insert", "would_update"}}
	for _, t := range r.TargetTables {
		var after, touched int64
		err := tx.QueryRow(ctx, `
			SELECT count(*), count(*) FILTER (WHERE xmin::text::bigint = txid_current() % 4294967296)
			FROM `+pgx.Identifier{t}.Sanitize()).Scan(&after, &touched)
		if err != nil {
			return err
		}
		inserted := after - before[t]
		changes.Add([]any{t, before[t], after, inserted, touched - inserted})
	}
	rejects := report.Table{Columns: []string{"source_table", "reason", "rows"}}
	rows, err := tx.Query(ctx, `
		SELECT source_table, reason, count(*) FROM buisy_nexus_staging.migration_rejects
		WHERE run_id = $1 GROUP BY 1, 2 ORDER BY 1, 3 DESC`, r.Rec.RunID)
	if err != nil {
		return err
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			rows.Close()
			return err
		}
		rejects.Add(vals)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := tx.Rollback(ctx); err != nil {
		return err
	}

	fmt.Fprintf(r.Out, "\nDRY RUN (%s): all transforms were rolled back. Changes they would make:\n", r.Target)
	changes.Print(r.Out)
	fmt.Fprintln(r.Out, "\nRejects they would record:")
	if len(rejects.Rows) == 0 {
		fmt.Fprintln(r.Out, "  (none)")
	} else {
		rejects.Print(r.Out)
	}

	id, err := r.Rec.Start(ctx, "transform", "")
	if err != nil {
		return err
	}
	r.Log.Info("dry run complete", "rows_written", total.Written, "rows_rejected", total.Rejected,
		"duration", time.Since(started).String())
	return r.Rec.Finish(ctx, id, total, nil, runlog.StatusDryRun)
}
