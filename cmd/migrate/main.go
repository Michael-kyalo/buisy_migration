// Command migrate copies Buisy MySQL data into Nexus Postgres staging, runs the
// SQL transforms, and validates the result.
//
//	migrate --env=development|training|live [--steps=copy,transform,validate]
//	        [--tables=nav_sku,users,...,keycloak] [--since=YYYY-MM-DD] [--limit=N] [--dry-run] [--confirm-live]
//	migrate profile --env=... [--confirm-live]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	migration "github.com/burnmfg/buisy-migration"
	"github.com/burnmfg/buisy-migration/internal/app"
	"github.com/burnmfg/buisy-migration/internal/config"
	copystep "github.com/burnmfg/buisy-migration/internal/copy"
	"github.com/burnmfg/buisy-migration/internal/keycloak"
	"github.com/burnmfg/buisy-migration/internal/runlog"
)

const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	isProfile := len(args) > 0 && args[0] == "profile"
	if isProfile {
		args = args[1:]
	}
	name := "migrate"
	if isProfile {
		name = "migrate profile"
	}
	fsFlags := flag.NewFlagSet(name, flag.ContinueOnError)
	fsFlags.SetOutput(stderr)
	envFlag := fsFlags.String("env", "", "target environment: development, training or live")
	confirmLive := fsFlags.Bool("confirm-live", false, "required to run against live")
	sqlDir := fsFlags.String("sql-dir", "", "read SQL from this directory instead of the embedded copy")
	logFormat := fsFlags.String("log-format", "json", "json or text")
	var steps, tables, since *string
	var dryRun *bool
	var batchSize, workers *int
	var limit *int64
	if !isProfile {
		steps = fsFlags.String("steps", strings.Join(app.Steps, ","), "comma-separated steps: copy,transform,validate")
		tables = fsFlags.String("tables", "", "comma-separated subset of tables to copy (default: all, plus keycloak)")
		since = fsFlags.String("since", "", "YYYY-MM-DD: copy only inventory and customers dated on or after this day; other tables copy in full")
		limit = fsFlags.Int64("limit", 0, "copy at most N rows from each inventory table and from customers (lowest ids first); other tables copy in full")
		dryRun = fsFlags.Bool("dry-run", false, "run transforms in a rolled-back transaction and print what would change")
		batchSize = fsFlags.Int("batch-size", 10000, "rows per keyset batch")
		workers = fsFlags.Int("workers", 2, "parallel Postgres COPY writers")
	}
	if err := fsFlags.Parse(args); err != nil {
		return exitUsage
	}

	log := runlog.NewLogger(stderr, *logFormat)
	usage := func(err error) int {
		fmt.Fprintln(stderr, "error:", err)
		return exitUsage
	}

	env, err := config.ParseEnv(*envFlag)
	if err != nil {
		return usage(err)
	}
	var sqlFS fs.FS = migration.SQL
	if *sqlDir != "" {
		sqlFS = os.DirFS(*sqlDir)
	}
	opts := app.Options{ConfirmLive: *confirmLive, SQL: sqlFS, Out: stdout, Log: log}
	if !isProfile {
		opts.DryRun = *dryRun
		opts.BatchSize = *batchSize
		opts.Workers = *workers
		if opts.BatchSize < 1 || opts.Workers < 1 {
			return usage(errors.New("--batch-size and --workers must be at least 1"))
		}
		if opts.Limit = *limit; opts.Limit < 0 {
			return usage(errors.New("--limit must be 0 (no limit) or more"))
		}
		if *since != "" {
			if opts.Since, err = time.Parse(time.DateOnly, *since); err != nil {
				return usage(fmt.Errorf("--since must be YYYY-MM-DD, got %q", *since))
			}
		}
		if opts.Steps, err = parseList(*steps, app.Steps); err != nil {
			return usage(fmt.Errorf("--steps: %w", err))
		}
		allTables := append(slices.Clone(copystep.SourceTables), copystep.KeycloakTable)
		if opts.Tables, err = parseList(*tables, allTables); err != nil {
			return usage(fmt.Errorf("--tables: %w", err))
		}
		if len(opts.Tables) == 0 {
			opts.Tables = allTables
		}
	}

	loader := config.Loader{Getenv: os.Getenv, Secrets: &config.AWSSecrets{}}
	cfg, err := loader.Load(ctx, env)
	if err != nil {
		return usage(err)
	}
	kc := func(ctx context.Context) (keycloak.Source, error) {
		kcfg, err := loader.LoadKeycloak(ctx, env)
		if err != nil {
			return nil, err
		}
		return keycloak.New(kcfg)
	}

	if isProfile {
		err = app.Profile(ctx, cfg, kc, opts)
	} else {
		err = app.Run(ctx, cfg, kc, opts)
	}
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, config.ErrLiveNotConfirmed), errors.Is(err, app.ErrFilterNeedsCopy):
		return usage(err)
	default:
		log.Error("run failed", "err", err)
		fmt.Fprintln(stderr, "error:", err)
		return exitFail
	}
}

// parseList splits a comma-separated flag and checks each item is allowed.
// The result keeps the order of allowed, not of the flag.
func parseList(raw string, allowed []string) ([]string, error) {
	seen := map[string]bool{}
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !slices.Contains(allowed, s) {
			return nil, fmt.Errorf("unknown value %q (allowed: %s)", s, strings.Join(allowed, ", "))
		}
		seen[s] = true
	}
	var out []string
	for _, a := range allowed {
		if seen[a] {
			out = append(out, a)
		}
	}
	return out, nil
}
