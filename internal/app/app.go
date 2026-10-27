// Package app wires configuration, connections and steps together. The CLI and
// the integration test both drive the tool through it.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"slices"
	"time"

	"github.com/burnmfg/buisy-migration/internal/config"
	copystep "github.com/burnmfg/buisy-migration/internal/copy"
	"github.com/burnmfg/buisy-migration/internal/keycloak"
	"github.com/burnmfg/buisy-migration/internal/profile"
	"github.com/burnmfg/buisy-migration/internal/runlog"
	"github.com/burnmfg/buisy-migration/internal/source"
	"github.com/burnmfg/buisy-migration/internal/target"
	"github.com/burnmfg/buisy-migration/internal/transform"
	"github.com/burnmfg/buisy-migration/internal/validate"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Steps in execution order.
var Steps = []string{"copy", "transform", "validate"}

// ErrValidationFailed means validation ran and at least one check failed.
var ErrValidationFailed = errors.New("validation failed")

// ErrFilterNeedsCopy: --since and --limit filter the copy step, so they are meaningless without one.
var ErrFilterNeedsCopy = errors.New("--since and --limit filter the copy step: include copy in --steps and don't combine them with --dry-run")

// Spec says what each target database receives. Routing only: the reshaping
// itself is in transforms/common and transforms/<name>.
type Spec struct {
	SourceTables []string // Buisy tables (and keycloak) copied into its staging schema
	TargetTables []string // tables its transforms write; for dry-run reporting
}

var Specs = map[string]Spec{
	config.DeliveryDB: {
		SourceTables: []string{"nav_sku", "users", "nav_inventory", "nav_inventory__cookware", "nav_dimension", copystep.KeycloakTable},
		TargetTables: []string{"skus", "agent_skus", "location_users", "inventory_products"},
	},
	config.CustomerAppDB: {
		SourceTables: []string{"users", "nav_sale_customer", copystep.KeycloakTable},
		TargetTables: []string{"contacts", "accounts"},
	},
}

type Options struct {
	Steps       []string
	Tables      []string
	Since       time.Time // zero for a full migration; see copystep.DateColumns
	Limit       int64     // 0 for no limit; rows per copystep.DateColumns table
	DryRun      bool
	ConfirmLive bool
	BatchSize   int
	Workers     int
	SQL         fs.FS
	Out         io.Writer
	Log         *slog.Logger
}

// KeycloakFactory builds the Keycloak source lazily, only if a step needs it.
type KeycloakFactory func(ctx context.Context) (keycloak.Source, error)

// PrintTargets announces where the run will write, before anything else happens.
func PrintTargets(w io.Writer, cfg *config.Config) {
	fmt.Fprintf(w, "env:    %s\n", cfg.Env)
	for _, t := range cfg.Targets {
		fmt.Fprintf(w, "target: %-14s host=%s database=%s (postgres)\n", t.Name, t.Host, t.Database)
	}
	fmt.Fprintf(w, "source: host=%s database=%s (mysql, read-only)\n", cfg.Source.Addr, cfg.Source.Database)
}

// conn is one connected target database for the duration of a run.
type conn struct {
	config.Target
	spec Spec
	pg   *pgxpool.Pool
	rec  *runlog.Recorder
}

func Run(ctx context.Context, cfg *config.Config, kc KeycloakFactory, opts Options) error {
	// --since and --limit are applied while copying from Buisy; transforms and
	// validation then work on whatever is staged.
	partial := !opts.Since.IsZero() || opts.Limit > 0
	if partial && (!slices.Contains(opts.Steps, "copy") || opts.DryRun) {
		return ErrFilterNeedsCopy
	}
	PrintTargets(opts.Out, cfg)
	if err := config.Guard(cfg.Env, opts.ConfirmLive); err != nil {
		return err
	}
	var since string
	if !opts.Since.IsZero() {
		since = opts.Since.Format(time.DateOnly)
		fmt.Fprintf(opts.Out, "partial: --since=%s (inventory and customers only)\n", since)
	}
	if opts.Limit > 0 {
		fmt.Fprintf(opts.Out, "partial: --limit=%d rows per inventory and customer table\n", opts.Limit)
	}
	runID := runlog.NewRunID()
	log := opts.Log.With("run_id", runID, "env", string(cfg.Env))
	log.Info("run starting", "steps", opts.Steps, "dry_run", opts.DryRun, "since", since)

	// Connect, verify, lock and bootstrap every target before any step runs.
	var conns []*conn
	defer func() {
		for _, c := range conns {
			c.pg.Close()
		}
	}()
	for _, t := range cfg.Targets {
		spec, ok := Specs[t.Name]
		if !ok {
			return fmt.Errorf("no spec for target %q", t.Name)
		}
		pg, err := target.Connect(ctx, t.URL, int32(opts.Workers+2))
		if err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
		conns = append(conns, &conn{Target: t, spec: spec, pg: pg,
			rec: &runlog.Recorder{PG: pg, RunID: runID, Env: string(cfg.Env), Since: since, Limit: opts.Limit}})
		if err := target.VerifyDatabase(ctx, pg, t.Database); err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
		unlock, err := target.Lock(ctx, pg)
		if err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
		defer unlock()
		if err := target.Bootstrap(ctx, pg, opts.SQL); err != nil {
			return fmt.Errorf("%s: bootstrap buisy_nexus_staging: %w", t.Name, err)
		}
	}

	if slices.Contains(opts.Steps, "copy") {
		started := time.Now()
		if opts.DryRun {
			log.Info("dry run: copy step skipped; buisy_nexus_staging is left as is")
		} else if err := runCopy(ctx, cfg, kc, conns, log, opts); err != nil {
			return fmt.Errorf("copy: %w", err)
		} else {
			log.Info("step complete", "step", "copy", "duration", time.Since(started).String())
		}
	}
	if slices.Contains(opts.Steps, "transform") {
		for _, c := range conns {
			started := time.Now()
			tr := &transform.Runner{
				Target: c.Name, TargetTables: c.spec.TargetTables, PG: c.pg, SQL: opts.SQL, Rec: c.rec,
				Log: log.With("step", "transform", "target", c.Name), Out: opts.Out, DryRun: opts.DryRun,
			}
			if err := tr.Run(ctx); err != nil {
				return fmt.Errorf("transform %s: %w", c.Name, err)
			}
			log.Info("step complete", "step", "transform", "target", c.Name, "duration", time.Since(started).String())
		}
	}
	if slices.Contains(opts.Steps, "validate") {
		if opts.DryRun {
			log.Info("dry run: validate step skipped; the transforms were rolled back")
			return nil
		}
		failed := 0
		for _, c := range conns {
			v := &validate.Runner{Target: c.Name, PG: c.pg, SQL: opts.SQL,
				Log: log.With("step", "validate", "target", c.Name), Out: opts.Out, Env: string(cfg.Env)}
			res, err := v.Run(ctx)
			if err != nil {
				return fmt.Errorf("validate %s: %w", c.Name, err)
			}
			failed += res.Failed
		}
		if failed > 0 {
			return ErrValidationFailed
		}
	}
	log.Info("run complete")
	return nil
}

func runCopy(ctx context.Context, cfg *config.Config, kc KeycloakFactory, conns []*conn, log *slog.Logger, opts Options) error {
	src, err := source.Open(ctx, cfg.Source.DSN, cfg.Source.Database)
	if err != nil {
		return err
	}
	defer src.Close()
	c := &copystep.Copier{
		Source: src, Log: log.With("step", "copy"), Out: opts.Out,
		Opts: copystep.Options{Tables: opts.Tables, Since: opts.Since, Limit: opts.Limit, BatchSize: opts.BatchSize, Workers: opts.Workers},
	}
	for _, cn := range conns {
		c.Targets = append(c.Targets, copystep.Target{Name: cn.Name, PG: cn.pg, Rec: cn.rec, Tables: cn.spec.SourceTables})
	}
	if slices.Contains(opts.Tables, copystep.KeycloakTable) {
		if c.Keycloak, err = kc(ctx); err != nil {
			return err
		}
	}
	return c.Run(ctx)
}

// Profile runs the read-only profiling checks. Cross-checks run against
// deliverydb, which holds the locations they compare with.
func Profile(ctx context.Context, cfg *config.Config, kc KeycloakFactory, opts Options) error {
	PrintTargets(opts.Out, cfg)
	if err := config.Guard(cfg.Env, opts.ConfirmLive); err != nil {
		return err
	}
	delivery, ok := cfg.Target(config.DeliveryDB)
	if !ok {
		return errors.New("deliverydb is not configured")
	}
	kcSource, err := kc(ctx)
	if err != nil {
		return err
	}
	src, err := source.Open(ctx, cfg.Source.DSN, cfg.Source.Database)
	if err != nil {
		return err
	}
	defer src.Close()
	pgConn, err := pgx.Connect(ctx, delivery.URL)
	if err != nil {
		return fmt.Errorf("connect to deliverydb: %w", err)
	}
	defer pgConn.Close(context.WithoutCancel(ctx))
	p := &profile.Runner{Source: src, PG: pgConn, Keycloak: kcSource, SQL: opts.SQL, Log: opts.Log.With("step", "profile"), Out: opts.Out}
	return p.Run(ctx)
}
