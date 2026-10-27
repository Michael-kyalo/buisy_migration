// Package profile runs read-only data-quality checks and prints counts plus up
// to five example rows each.
//
//   - profile/mysql/*.sql run on Buisy inside a read-only snapshot.
//   - profile/extract/*.sql pull small key sets from Buisy into session TEMP
//     tables in Postgres (profile_<name>), alongside profile_keycloak_users,
//     so profile/pg/*.sql can compare them with Nexus. Temp tables vanish with
//     the session; nothing persistent is written anywhere.
package profile

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/burnmfg/buisy-migration/internal/keycloak"
	"github.com/burnmfg/buisy-migration/internal/report"
	"github.com/burnmfg/buisy-migration/internal/source"
	"github.com/burnmfg/buisy-migration/internal/sqlfiles"
	"github.com/jackc/pgx/v5"
)

const sampleRows = 5

type Runner struct {
	Source   *source.DB
	PG       *pgx.Conn // a single session, so temp tables stay visible
	Keycloak keycloak.Source
	SQL      fs.FS
	Log      *slog.Logger
	Out      io.Writer
}

func (r *Runner) Run(ctx context.Context) error {
	snap, err := r.Source.Snapshot(ctx)
	if err != nil {
		return err
	}
	defer snap.Close()

	fmt.Fprintln(r.Out, "\nPROFILE: Buisy source")
	files, err := sqlfiles.List(r.SQL, "profile/mysql")
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := r.mysqlCheck(ctx, snap, f); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}

	extracts, err := sqlfiles.List(r.SQL, "profile/extract")
	if err != nil {
		return err
	}
	for _, f := range extracts {
		if err := r.extract(ctx, snap, f); err != nil {
			return fmt.Errorf("extract %s: %w", f.Name, err)
		}
	}
	if err := r.loadKeycloak(ctx); err != nil {
		return err
	}

	fmt.Fprintln(r.Out, "\nPROFILE: Buisy vs Nexus and Keycloak")
	tx, err := r.PG.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	files, err = sqlfiles.List(r.SQL, "profile/pg")
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := r.pgCheck(ctx, tx, f); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return nil
}

func (r *Runner) mysqlCheck(ctx context.Context, snap *source.Snapshot, f sqlfiles.File) error {
	started := time.Now()
	var count int64
	if err := snap.QueryRow(ctx, sqlfiles.Wrap("SELECT COUNT(*) FROM (", f.Query(), ") AS profile_q")).Scan(&count); err != nil {
		return err
	}
	q := f.Query()
	if !f.ShowAll() {
		q = sqlfiles.Wrap("SELECT * FROM (", q, fmt.Sprintf(") AS profile_q LIMIT %d", sampleRows))
	}
	rows, err := snap.Query(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	tbl, err := sqlTable(rows)
	if err != nil {
		return err
	}
	r.print(f, count, tbl)
	r.Log.Info("profile check", "file", f.Name, "rows", count, "duration", time.Since(started).String())
	return nil
}

func (r *Runner) pgCheck(ctx context.Context, tx pgx.Tx, f sqlfiles.File) error {
	started := time.Now()
	var count int64
	if err := tx.QueryRow(ctx, sqlfiles.Wrap("SELECT count(*) FROM (", f.Query(), ") AS profile_q")).Scan(&count); err != nil {
		return err
	}
	q := f.Query()
	if !f.ShowAll() {
		q = sqlfiles.Wrap("SELECT * FROM (", q, fmt.Sprintf(") AS profile_q LIMIT %d", sampleRows))
	}
	rows, err := tx.Query(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	var tbl report.Table
	for _, fd := range rows.FieldDescriptions() {
		tbl.Columns = append(tbl.Columns, fd.Name)
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return err
		}
		tbl.Add(vals)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	r.print(f, count, tbl)
	r.Log.Info("profile check", "file", f.Name, "rows", count, "duration", time.Since(started).String())
	return nil
}

func (r *Runner) print(f sqlfiles.File, count int64, tbl report.Table) {
	if f.ShowAll() {
		fmt.Fprintf(r.Out, "\n== %s\n", f.Stem())
	} else {
		fmt.Fprintf(r.Out, "\n== %s: %d row(s)", f.Stem(), count)
		if count > int64(len(tbl.Rows)) {
			fmt.Fprintf(r.Out, ", showing %d", len(tbl.Rows))
		}
		fmt.Fprintln(r.Out)
	}
	if len(tbl.Rows) == 0 {
		fmt.Fprintln(r.Out, "  (none)")
		return
	}
	tbl.Print(r.Out)
}

func sqlTable(rows *sql.Rows) (report.Table, error) {
	cols, err := rows.Columns()
	if err != nil {
		return report.Table{}, err
	}
	tbl := report.Table{Columns: cols}
	vals := make([]any, len(cols))
	dests := make([]any, len(cols))
	for i := range vals {
		dests[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(dests...); err != nil {
			return tbl, err
		}
		tbl.Add(vals)
	}
	return tbl, rows.Err()
}

// extract streams a Buisy query into TEMP TABLE profile_<stem>, every column as text.
func (r *Runner) extract(ctx context.Context, snap *source.Snapshot, f sqlfiles.File) error {
	rows, err := snap.Query(ctx, f.Query())
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	name := "profile_" + f.Stem()
	defs := make([]string, len(cols))
	for i, c := range cols {
		cols[i] = strings.ToLower(c)
		defs[i] = pgx.Identifier{cols[i]}.Sanitize() + " text"
	}
	if _, err := r.PG.Exec(ctx, "CREATE TEMP TABLE "+pgx.Identifier{name}.Sanitize()+" ("+strings.Join(defs, ", ")+")"); err != nil {
		return err
	}
	vals := make([]any, len(cols))
	dests := make([]any, len(cols))
	for i := range vals {
		dests[i] = &vals[i]
	}
	n, err := r.PG.CopyFrom(ctx, pgx.Identifier{name}, cols, pgx.CopyFromFunc(func() ([]any, error) {
		if !rows.Next() {
			return nil, rows.Err()
		}
		if err := rows.Scan(dests...); err != nil {
			return nil, err
		}
		out := make([]any, len(vals))
		for i, v := range vals {
			out[i] = asText(v)
		}
		return out, nil
	}))
	if err != nil {
		return err
	}
	r.Log.Info("profile extract", "table", name, "rows", n)
	return nil
}

func (r *Runner) loadKeycloak(ctx context.Context) error {
	users, err := r.Keycloak.Users(ctx)
	if err != nil {
		return fmt.Errorf("keycloak users: %w", err)
	}
	if _, err := r.PG.Exec(ctx,
		"CREATE TEMP TABLE profile_keycloak_users (id text, username text, email text, enabled boolean)"); err != nil {
		return err
	}
	n, err := r.PG.CopyFrom(ctx, pgx.Identifier{"profile_keycloak_users"},
		[]string{"id", "username", "email", "enabled"}, pgx.CopyFromRows(keycloak.Rows(users)))
	if err != nil {
		return err
	}
	r.Log.Info("profile keycloak users", "source", r.Keycloak.Describe(), "rows", n)
	return nil
}

func asText(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		s := strings.ReplaceAll(string(x), "\x00", "")
		return strings.ToValidUTF8(s, "�")
	case string:
		if !utf8.ValidString(x) {
			return strings.ToValidUTF8(x, "�")
		}
		return strings.ReplaceAll(x, "\x00", "")
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	default:
		return fmt.Sprint(x)
	}
}
