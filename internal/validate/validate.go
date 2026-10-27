// Package validate runs validate/*.sql read-only against the target and prints
// a report. Files named *_check_* must return a boolean "ok" column; any row
// where ok is not true fails the run. Other files are printed as reports.
package validate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"strings"

	"github.com/burnmfg/buisy-migration/internal/report"
	"github.com/burnmfg/buisy-migration/internal/sqlfiles"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Runner struct {
	Target string // target database name; selects validate/<Target>
	PG     *pgxpool.Pool
	SQL    fs.FS
	Log    *slog.Logger
	Out    io.Writer
	Env    string
}

// Result counts failing check rows; Failed > 0 means the run should exit non-zero.
type Result struct {
	Checks int
	Failed int
}

func (r *Runner) Run(ctx context.Context) (Result, error) {
	var res Result
	files, err := sqlfiles.ListMerged(r.SQL, "validate/common", "validate/"+r.Target)
	if err != nil {
		return res, err
	}

	var runID string
	err = r.PG.QueryRow(ctx, `
		SELECT run_id::text FROM buisy_nexus_staging.migration_runs
		WHERE env = $1 AND step = 'transform' AND detail IS NULL AND status = 'ok'
		ORDER BY started_at DESC LIMIT 1`, r.Env).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, fmt.Errorf("%s: no successful transform run to validate; run the transform step first", r.Target)
	}
	if err != nil {
		return res, err
	}

	tx, err := r.PG.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('migrate.transform_run_id', $1, true)", runID); err != nil {
		return res, err
	}

	fmt.Fprintf(r.Out, "\nVALIDATION REPORT: %s (transform run %s)\n", r.Target, runID)
	for _, f := range files {
		isCheck := strings.Contains(f.Name, "_check_")
		tbl, failed, err := runFile(ctx, tx, f, isCheck)
		if err != nil {
			return res, fmt.Errorf("%s: %w", f.Path(), err)
		}
		label := "REPORT"
		if isCheck {
			res.Checks++
			res.Failed += failed
			label = "PASS"
			if failed > 0 {
				label = fmt.Sprintf("FAIL (%d)", failed)
			}
		}
		fmt.Fprintf(r.Out, "\n== %s  [%s]\n", f.Stem(), label)
		if len(tbl.Rows) == 0 {
			fmt.Fprintln(r.Out, "  (no rows)")
		} else {
			tbl.Print(r.Out)
		}
		r.Log.Info("validation file", "file", f.Name, "rows", len(tbl.Rows), "failed_rows", failed)
	}
	if res.Failed > 0 {
		fmt.Fprintf(r.Out, "\nVALIDATION FAILED (%s): %d failing row(s) across checks\n", r.Target, res.Failed)
	} else {
		fmt.Fprintf(r.Out, "\nVALIDATION PASSED (%s): %d check file(s)\n", r.Target, res.Checks)
	}
	return res, nil
}

func runFile(ctx context.Context, tx pgx.Tx, f sqlfiles.File, isCheck bool) (report.Table, int, error) {
	var tbl report.Table
	rows, err := tx.Query(ctx, f.SQL)
	if err != nil {
		return tbl, 0, err
	}
	defer rows.Close()
	okIdx := -1
	for i, fd := range rows.FieldDescriptions() {
		tbl.Columns = append(tbl.Columns, fd.Name)
		if fd.Name == "ok" {
			okIdx = i
		}
	}
	if isCheck && okIdx < 0 {
		return tbl, 0, errors.New(`check files must return a boolean "ok" column`)
	}
	failed := 0
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return tbl, 0, err
		}
		if isCheck {
			if ok, _ := vals[okIdx].(bool); !ok {
				failed++
			}
		}
		tbl.Add(vals)
	}
	return tbl, failed, rows.Err()
}
