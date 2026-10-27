// Package copy streams Buisy tables, unchanged, into the buisy_nexus_staging
// schema of each target database that needs them. It does no reshaping: that
// belongs in transforms/.
//
// All tables are read through one MySQL connection holding a consistent
// snapshot, so every target sees the same moment. Each table is read once and
// its batches are written to every target that needs it. Reading is therefore
// sequential; the parallelism is on the Postgres side, where Workers goroutines
// load batches with COPY while the reader fetches the next one.
package copy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/burnmfg/buisy-migration/internal/keycloak"
	"github.com/burnmfg/buisy-migration/internal/report"
	"github.com/burnmfg/buisy-migration/internal/runlog"
	"github.com/burnmfg/buisy-migration/internal/source"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
)

// SourceTables are all Buisy tables the tool knows how to copy.
var SourceTables = []string{
	"nav_sku", "users", "nav_sale_customer", "nav_inventory", "nav_inventory__cookware", "nav_dimension",
}

// KeycloakTable is the --tables name that selects the Keycloak user load.
const KeycloakTable = "keycloak"

// DateColumns are the tables --since and --limit apply to, and the date column
// --since filters on. Tables not listed (SKUs, users, dimensions) are always
// copied in full, because filtered inventory and customers still reference them.
var DateColumns = map[string]string{
	"nav_inventory":           "inv_entry_date",
	"nav_inventory__cookware": "inv_entry_date",
	"nav_sale_customer":       "scust_entry_date",
}

// ExcludedColumns are never read from Buisy: credentials and tokens have no
// place in Nexus, so they are not copied into staging either.
var ExcludedColumns = map[string][]string{
	"users": {"user_password", "token", "token_date", "user_six_digit_code", "firebase"},
}

type Options struct {
	Tables    []string  // selected source tables, optionally plus KeycloakTable
	Since     time.Time // zero: copy everything; otherwise only DateColumns rows on or after it
	Limit     int64     // 0: no limit; otherwise at most this many rows per DateColumns table, lowest keys first
	BatchSize int
	Workers   int
}

// Target is one Postgres database and the source tables it needs.
type Target struct {
	Name   string
	PG     *pgxpool.Pool
	Rec    *runlog.Recorder
	Tables []string // source tables, optionally plus KeycloakTable
}

type Copier struct {
	Source   *source.DB
	Targets  []Target
	Keycloak keycloak.Source // required when a selected target needs KeycloakTable
	Log      *slog.Logger
	Out      io.Writer // receives the --since / --limit report
	Opts     Options
}

// sink is one table's destination in one target.
type sink struct {
	target  *Target
	runID   int64
	written atomic.Int64
}

type tableState struct {
	Table
	sinks   []*sink
	read    int64
	fixes   int64
	started time.Time

	// Set when --since or --limit applies to this table.
	dateColumn  string
	limit       int64 // 0: no limit
	sourceTotal int64 // rows in the source table, filtered or not
	matching    int64 // rows passing --since (all rows without it)
	noDate      int64 // rows skipped by --since because their date is NULL or zero
}

// partial reports whether the run copies only part of the DateColumns tables.
func (o Options) partial() bool { return !o.Since.IsZero() || o.Limit > 0 }

type batch struct {
	table *tableState
	sink  *sink
	rows  [][]any
}

// selected returns the targets that need name, if name was selected.
func (c *Copier) selected(name string) []*Target {
	if !slices.Contains(c.Opts.Tables, name) {
		return nil
	}
	var out []*Target
	for i := range c.Targets {
		if slices.Contains(c.Targets[i].Tables, name) {
			out = append(out, &c.Targets[i])
		}
	}
	return out
}

func (c *Copier) Run(ctx context.Context) error {
	var tables []string
	for _, t := range SourceTables {
		if len(c.selected(t)) > 0 {
			tables = append(tables, t)
		}
	}
	if len(tables) > 0 {
		if err := c.copyTables(ctx, tables); err != nil {
			return err
		}
	}
	if targets := c.selected(KeycloakTable); len(targets) > 0 {
		return c.copyKeycloak(ctx, targets)
	}
	return nil
}

func (c *Copier) copyTables(ctx context.Context, names []string) error {
	snap, err := c.Source.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("open mysql snapshot: %w", err)
	}
	defer snap.Close()

	// Discover and reset every staging table, in every target, before any data moves.
	states := make([]*tableState, 0, len(names))
	for _, name := range names {
		t, err := Discover(ctx, snap, c.Source.Database, name)
		if err != nil {
			return err
		}
		t.Columns = slices.DeleteFunc(t.Columns, func(col Column) bool {
			return slices.Contains(ExcludedColumns[name], col.Name)
		})
		for _, col := range t.Columns {
			if _, _, known := classify(col); !known {
				c.Log.Warn("unknown mysql type, copying as text", "table", t.Name, "column", col.Name, "type", col.ColumnType)
			}
		}
		if len(t.Key) == 0 {
			c.Log.Warn("no primary or NOT NULL unique key; streaming without keyset pagination", "table", t.Name)
		}
		st := &tableState{Table: t}
		if col := DateColumns[name]; col != "" && c.Opts.partial() {
			st.dateColumn = col
			st.limit = c.Opts.Limit
			// Counted in the same snapshot as the copy, so the report adds up exactly.
			count := "SELECT COUNT(*), 0, COUNT(*) FROM " + quoteMySQL(t.Source)
			if !c.Opts.Since.IsZero() {
				if err := st.applySince(col, c.Opts.Since); err != nil {
					return err
				}
				count = "SELECT COUNT(*), " +
					"COALESCE(SUM(CASE WHEN " + noDate(st.DateExpr) + " THEN 1 ELSE 0 END), 0), " +
					"COALESCE(SUM(CASE WHEN " + st.Filter + " THEN 1 ELSE 0 END), 0) " +
					"FROM " + quoteMySQL(t.Source)
			}
			if err := snap.QueryRow(ctx, count, st.FilterArgs...).Scan(&st.sourceTotal, &st.noDate, &st.matching); err != nil {
				return fmt.Errorf("count %s for --since/--limit: %w", t.Name, err)
			}
		}
		for _, tgt := range c.selected(name) {
			recreated, err := prepareStaging(ctx, tgt.PG, t)
			if err != nil {
				return fmt.Errorf("%s: prepare buisy_nexus_staging.%s: %w", tgt.Name, t.Name, err)
			}
			c.Log.Info("staging table ready", "target", tgt.Name, "table", t.Name, "columns", len(t.Columns),
				"key", t.Key, "recreated", recreated)
			st.sinks = append(st.sinks, &sink{target: tgt})
		}
		states = append(states, st)
	}

	g, gctx := errgroup.WithContext(ctx)
	batches := make(chan batch, c.Opts.Workers)
	for range c.Opts.Workers {
		g.Go(func() error {
			for b := range batches {
				n, err := b.sink.target.PG.CopyFrom(gctx, b.table.ident(), b.table.columnNames(), pgx.CopyFromRows(b.rows))
				if err != nil {
					return fmt.Errorf("%s: copy into buisy_nexus_staging.%s: %w", b.sink.target.Name, b.table.Name, err)
				}
				b.sink.written.Add(n)
			}
			return nil
		})
	}
	g.Go(func() error {
		defer close(batches)
		for _, st := range states {
			for _, s := range st.sinks {
				id, err := s.target.Rec.Start(gctx, "copy", st.Name)
				if err != nil {
					return err
				}
				s.runID = id
			}
			st.started = time.Now()
			// Every target gets the same rows; CopyFrom only reads them.
			emit := func(rows [][]any) error {
				for _, s := range st.sinks {
					select {
					case batches <- batch{table: st, sink: s, rows: rows}:
					case <-gctx.Done():
						return gctx.Err()
					}
				}
				return nil
			}
			if err := c.stream(gctx, snap, st, emit); err != nil {
				return fmt.Errorf("read %s: %w", st.Name, err)
			}
			c.Log.Info("read table", "table", st.Name, "rows", st.read, "text_fixes", st.fixes,
				"duration", time.Since(st.started).String())
		}
		return nil
	})
	runErr := g.Wait()

	for _, st := range states {
		for _, s := range st.sinks {
			if s.runID == 0 {
				continue
			}
			var sinkErr error
			if runErr != nil {
				sinkErr = runErr
			} else if s.written.Load() != st.read {
				sinkErr = fmt.Errorf("%s: %s: read %d rows but wrote %d", s.target.Name, st.Name, st.read, s.written.Load())
			}
			counts := runlog.Counts{Read: st.read, Written: s.written.Load()}
			if err := s.target.Rec.Finish(context.WithoutCancel(ctx), s.runID, counts, sinkErr, ""); err != nil {
				c.Log.Error("record copy run", "target", s.target.Name, "table", st.Name, "err", err)
			}
			if sinkErr == nil {
				c.Log.Info("copied table", "target", s.target.Name, "table", st.Name, "rows", st.read,
					"duration", time.Since(st.started).String())
			}
			if runErr == nil {
				runErr = sinkErr
			}
		}
	}
	if runErr == nil && c.Opts.partial() {
		c.reportPartial(states)
	}
	return runErr
}

// reportPartial prints what --since / --limit kept and skipped, so no row
// disappears silently. Per table: source_rows = copied + before_since +
// no_date_skipped + over_limit.
func (c *Copier) reportPartial(states []*tableState) {
	tbl := report.Table{Columns: []string{"table", "date_column", "source_rows", "copied", "before_since", "no_date_skipped", "over_limit"}}
	for _, st := range states {
		if st.dateColumn == "" {
			continue
		}
		before := st.sourceTotal - st.matching - st.noDate
		tbl.Add([]any{st.Name, st.dateColumn, st.sourceTotal, st.read, before, st.noDate, st.matching - st.read})
		if st.noDate > 0 {
			c.Log.Warn("rows without a date were skipped by --since", "table", st.Name, "column", st.dateColumn, "rows", st.noDate)
		}
	}
	var filters []string
	if !c.Opts.Since.IsZero() {
		filters = append(filters, "--since="+c.Opts.Since.Format(time.DateOnly))
	}
	if c.Opts.Limit > 0 {
		filters = append(filters, fmt.Sprintf("--limit=%d", c.Opts.Limit))
	}
	fmt.Fprintf(c.Out, "\nPARTIAL COPY %s: only these tables were filtered; all others were copied in full.\n",
		strings.Join(filters, " "))
	tbl.Print(c.Out)
}

// stream reads a table in keyset-paginated batches (never OFFSET), handing each
// batch to emit. Tables without a usable key are streamed with one query.
func (c *Copier) stream(ctx context.Context, snap *source.Snapshot, st *tableState, emit func([][]any) error) error {
	scanners := make([]*scanner, len(st.Columns))
	dests := make([]any, len(st.Columns))
	keyIdx := make([]int, 0, len(st.Key))
	for i, col := range st.Columns {
		k, _, _ := classify(col)
		scanners[i] = newScanner(k)
		dests[i] = scanners[i].dest
		if slices.Contains(st.Key, col.Name) {
			keyIdx = append(keyIdx, i)
		}
	}

	readRows := func(query string, args ...any) (n int, last []any, err error) {
		rows, err := snap.Query(ctx, query, args...)
		if err != nil {
			return 0, nil, err
		}
		defer rows.Close()
		buf := make([][]any, 0, c.Opts.BatchSize)
		for rows.Next() {
			if err := rows.Scan(dests...); err != nil {
				return n, nil, err
			}
			row := make([]any, len(scanners))
			for i, s := range scanners {
				v, fixes, err := s.value()
				if err != nil {
					return n, nil, fmt.Errorf("column %s: %w", st.Columns[i].Name, err)
				}
				row[i] = v
				st.fixes += int64(fixes)
			}
			buf = append(buf, row)
			n++
			if len(buf) == c.Opts.BatchSize {
				if err := emit(buf); err != nil {
					return n, nil, err
				}
				buf = make([][]any, 0, c.Opts.BatchSize)
			}
		}
		if err := rows.Err(); err != nil {
			return n, nil, err
		}
		if len(buf) > 0 {
			if err := emit(buf); err != nil {
				return n, nil, err
			}
		}
		if n > 0 && len(keyIdx) > 0 {
			last = make([]any, len(keyIdx))
			for i, idx := range keyIdx {
				last[i] = scanners[idx].keyValue()
			}
		}
		return n, last, nil
	}

	if len(st.Key) == 0 {
		query, args := st.fullQuery(), slices.Clone(st.FilterArgs)
		if st.limit > 0 {
			query += " LIMIT ?"
			args = append(args, st.limit)
		}
		n, _, err := readRows(query, args...)
		st.read += int64(n)
		return err
	}

	// Key values must come out in Key order, which may differ from column order.
	slices.SortFunc(keyIdx, func(a, b int) int {
		return slices.Index(st.Key, st.Columns[a].Name) - slices.Index(st.Key, st.Columns[b].Name)
	})
	var last []any
	for {
		// With --limit, the last batch is shortened so exactly the first N rows
		// (in key order) are read.
		size := int64(c.Opts.BatchSize)
		if st.limit > 0 {
			size = min(size, st.limit-st.read)
			if size <= 0 {
				return nil
			}
		}
		args := slices.Clone(st.FilterArgs)
		query := st.batchQuery(last == nil)
		args = append(args, last...)
		args = append(args, size)
		n, next, err := readRows(query, args...)
		st.read += int64(n)
		if err != nil {
			return err
		}
		if int64(n) < size {
			return nil
		}
		last = next
	}
}

// copyKeycloak fetches the realm's users once and loads them into every target that needs them.
func (c *Copier) copyKeycloak(ctx context.Context, targets []*Target) error {
	if c.Keycloak == nil {
		return errors.New("keycloak source is not configured")
	}
	started := time.Now()
	users, err := c.Keycloak.Users(ctx)
	if err == nil && len(users) == 0 {
		err = fmt.Errorf("%s returned no users; check the realm", c.Keycloak.Describe())
	}
	if err != nil {
		return fmt.Errorf("keycloak users: %w", err)
	}
	for _, tgt := range targets {
		id, err := tgt.Rec.Start(ctx, "copy", "keycloak_users")
		if err != nil {
			return err
		}
		n, err := loadKeycloak(ctx, tgt.PG, users)
		if ferr := tgt.Rec.Finish(context.WithoutCancel(ctx), id, runlog.Counts{Read: int64(len(users)), Written: n}, err, ""); ferr != nil {
			c.Log.Error("record keycloak run", "target", tgt.Name, "err", ferr)
		}
		if err != nil {
			return fmt.Errorf("%s: keycloak users: %w", tgt.Name, err)
		}
		c.Log.Info("copied keycloak users", "target", tgt.Name, "source", c.Keycloak.Describe(), "rows", n,
			"duration", time.Since(started).String())
	}
	return nil
}

func loadKeycloak(ctx context.Context, pg *pgxpool.Pool, users []keycloak.User) (int64, error) {
	tx, err := pg.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "TRUNCATE buisy_nexus_staging.keycloak_users"); err != nil {
		return 0, err
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"buisy_nexus_staging", "keycloak_users"},
		[]string{"id", "username", "email", "enabled"}, pgx.CopyFromRows(keycloak.Rows(users)))
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}
