// Package config resolves connection settings for one environment from
// environment variables, with optional AWS Secrets Manager lookups.
//
// Connection and credential variables are always suffixed with the
// environment (e.g. MIGRATE_DELIVERYDB_URL_LIVE) and never fall back to another
// environment's value, so a missing live setting can't silently reuse dev.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

type Env string

const (
	Development Env = "development"
	Training    Env = "training"
	Live        Env = "live"
)

// DefaultSourceDatabase is the Buisy schema name.
const DefaultSourceDatabase = "burnmfgsystems_db"

var ErrLiveNotConfirmed = errors.New("refusing to run against live without --confirm-live")

func ParseEnv(s string) (Env, error) {
	switch e := Env(strings.ToLower(strings.TrimSpace(s))); e {
	case Development, Training, Live:
		return e, nil
	}
	return "", fmt.Errorf("--env must be development, training or live, got %q", s)
}

func (e Env) suffix() string { return strings.ToUpper(string(e)) }

// Guard refuses live runs that were not explicitly confirmed.
func Guard(env Env, confirmLive bool) error {
	if env == Live && !confirmLive {
		return ErrLiveNotConfirmed
	}
	return nil
}

type Postgres struct {
	URL      string
	Host     string
	Database string
}

type MySQL struct {
	DSN      string // normalised: parseTime, utf8mb4, loc=UTC
	Addr     string
	Database string
}

type Keycloak struct {
	Mode         string // "db" or "api"
	Realm        string
	DB           Postgres // db mode
	BaseURL      string   // api mode
	TokenRealm   string   // api mode; realm the client authenticates against
	ClientID     string
	ClientSecret string
}

// Target database names. Each is a separate Postgres database with its own
// connection settings, staging schema, transforms and validation.
const (
	DeliveryDB    = "deliverydb"    // skus, agent_skus, location_users, inventory_products
	CustomerAppDB = "customerappdb" // contacts, accounts
)

// TargetNames lists the targets in the order they are migrated.
var TargetNames = []string{DeliveryDB, CustomerAppDB}

type Target struct {
	Name string
	Postgres
}

type Config struct {
	Env     Env
	Targets []Target // in TargetNames order
	Source  MySQL
}

// Target returns the named target's settings.
func (c *Config) Target(name string) (Target, bool) {
	for _, t := range c.Targets {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

// envPrefix is the variable prefix for a target, e.g. MIGRATE_DELIVERYDB.
func envPrefix(target string) string { return "MIGRATE_" + strings.ToUpper(target) }

// SecretGetter returns the SecretString of a Secrets Manager secret.
type SecretGetter interface {
	GetSecretString(ctx context.Context, id string) (string, error)
}

type Loader struct {
	Getenv  func(string) string
	Secrets SecretGetter
}

// Load resolves every target Postgres database and the source MySQL for env.
func (l Loader) Load(ctx context.Context, env Env) (*Config, error) {
	cfg := &Config{Env: env}
	for _, name := range TargetNames {
		pg, err := l.postgres(ctx, envPrefix(name), env)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		cfg.Targets = append(cfg.Targets, Target{Name: name, Postgres: pg})
	}
	source, err := l.mysql(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	cfg.Source = source
	return cfg, nil
}

// LoadKeycloak resolves the Keycloak user source for env. It is separate from
// Load because only the copy and profile steps need it.
func (l Loader) LoadKeycloak(ctx context.Context, env Env) (Keycloak, error) {
	kc := Keycloak{
		Mode:  l.shared("KEYCLOAK_MODE", env),
		Realm: l.shared("KEYCLOAK_REALM", env),
	}
	if kc.Realm == "" {
		return kc, errors.New("KEYCLOAK_REALM is not set")
	}
	switch kc.Mode {
	case "db":
		pg, err := l.postgres(ctx, "KEYCLOAK_DB", env)
		if err != nil {
			return kc, fmt.Errorf("keycloak db: %w", err)
		}
		kc.DB = pg
	case "api":
		kc.BaseURL = strings.TrimRight(l.perEnv("KEYCLOAK_BASE_URL", env), "/")
		if kc.BaseURL == "" {
			return kc, fmt.Errorf("KEYCLOAK_BASE_URL_%s is not set", env.suffix())
		}
		kc.TokenRealm = l.shared("KEYCLOAK_TOKEN_REALM", env)
		if kc.TokenRealm == "" {
			kc.TokenRealm = kc.Realm
		}
		kc.ClientID = l.shared("KEYCLOAK_CLIENT_ID", env)
		secret, err := l.valueOrSecret(ctx, "KEYCLOAK_CLIENT_SECRET", env)
		if err != nil {
			return kc, err
		}
		kc.ClientSecret = secret
		if kc.ClientID == "" || kc.ClientSecret == "" {
			return kc, fmt.Errorf("KEYCLOAK_CLIENT_ID and KEYCLOAK_CLIENT_SECRET_%s are required in api mode", env.suffix())
		}
	default:
		return kc, fmt.Errorf("KEYCLOAK_MODE must be db or api, got %q", kc.Mode)
	}
	return kc, nil
}

// perEnv reads NAME_<ENV> only.
func (l Loader) perEnv(name string, env Env) string {
	return strings.TrimSpace(l.Getenv(name + "_" + env.suffix()))
}

// shared reads NAME_<ENV>, falling back to NAME. Only for non-credential settings.
func (l Loader) shared(name string, env Env) string {
	if v := l.perEnv(name, env); v != "" {
		return v
	}
	return strings.TrimSpace(l.Getenv(name))
}

// valueOrSecret reads NAME_<ENV>, or the secret named by NAME_SECRET_ARN_<ENV>.
func (l Loader) valueOrSecret(ctx context.Context, name string, env Env) (string, error) {
	if v := l.perEnv(name, env); v != "" {
		return v, nil
	}
	arn := l.perEnv(name+"_SECRET_ARN", env)
	if arn == "" {
		return "", nil
	}
	return l.secret(ctx, arn)
}

func (l Loader) secret(ctx context.Context, arn string) (string, error) {
	if l.Secrets == nil {
		return "", errors.New("secret ARN given but no Secrets Manager client configured")
	}
	s, err := l.Secrets.GetSecretString(ctx, arn)
	if err != nil {
		return "", fmt.Errorf("read secret %s: %w", arn, err)
	}
	return s, nil
}

// dbSecret is the JSON shape of an RDS-managed database secret.
type dbSecret struct {
	Username string          `json:"username"`
	Password string          `json:"password"`
	Host     string          `json:"host"`
	Port     json.RawMessage `json:"port"`
	DBName   string          `json:"dbname"`
}

func (s dbSecret) port(def string) string {
	p := strings.Trim(string(s.Port), `" `)
	if p == "" {
		return def
	}
	return p
}

func parseDBSecret(raw string) (dbSecret, error) {
	var s dbSecret
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, fmt.Errorf("secret is not RDS-style JSON: %w", err)
	}
	if s.Host == "" || s.Username == "" {
		return s, errors.New("secret is missing host or username")
	}
	return s, nil
}

// postgres resolves PREFIX_URL_<ENV>, or PREFIX_SECRET_ARN_<ENV> (RDS JSON)
// with an optional PREFIX_DATABASE_<ENV> overriding the secret's dbname.
func (l Loader) postgres(ctx context.Context, prefix string, env Env) (Postgres, error) {
	raw := l.perEnv(prefix+"_URL", env)
	if raw == "" {
		arn := l.perEnv(prefix+"_SECRET_ARN", env)
		if arn == "" {
			return Postgres{}, fmt.Errorf("set %s_URL_%s or %s_SECRET_ARN_%s", prefix, env.suffix(), prefix, env.suffix())
		}
		secret, err := l.secret(ctx, arn)
		if err != nil {
			return Postgres{}, err
		}
		s, err := parseDBSecret(secret)
		if err != nil {
			return Postgres{}, err
		}
		// Lets two databases on one server share a secret, e.g. deliverydb and customerappdb.
		if db := l.perEnv(prefix+"_DATABASE", env); db != "" {
			s.DBName = db
		}
		sslmode := l.shared("MIGRATE_PG_SSLMODE", env)
		if sslmode == "" {
			sslmode = "require"
		}
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(s.Username, s.Password),
			Host:     net.JoinHostPort(s.Host, s.port("5432")),
			Path:     "/" + s.DBName,
			RawQuery: url.Values{"sslmode": {sslmode}}.Encode(),
		}
		raw = u.String()
	}
	return ParsePostgres(raw)
}

// ParsePostgres validates a Postgres URL/DSN and extracts host and database.
func ParsePostgres(raw string) (Postgres, error) {
	pc, err := pgx.ParseConfig(raw)
	if err != nil {
		return Postgres{}, fmt.Errorf("invalid postgres connection string: %w", err)
	}
	if pc.Database == "" {
		return Postgres{}, errors.New("postgres connection string has no database name")
	}
	return Postgres{URL: raw, Host: pc.Host, Database: pc.Database}, nil
}

// mysql resolves BUISY_MYSQL_DSN_<ENV>, or BUISY_MYSQL_SECRET_ARN_<ENV> (RDS JSON).
func (l Loader) mysql(ctx context.Context, env Env) (MySQL, error) {
	dsn := l.perEnv("BUISY_MYSQL_DSN", env)
	if dsn == "" {
		arn := l.perEnv("BUISY_MYSQL_SECRET_ARN", env)
		if arn == "" {
			return MySQL{}, fmt.Errorf("set BUISY_MYSQL_DSN_%s or BUISY_MYSQL_SECRET_ARN_%s", env.suffix(), env.suffix())
		}
		secret, err := l.secret(ctx, arn)
		if err != nil {
			return MySQL{}, err
		}
		s, err := parseDBSecret(secret)
		if err != nil {
			return MySQL{}, err
		}
		mc := mysql.NewConfig()
		mc.User = s.Username
		mc.Passwd = s.Password
		mc.Net = "tcp"
		mc.Addr = net.JoinHostPort(s.Host, s.port("3306"))
		mc.DBName = s.DBName
		if tls := l.shared("BUISY_MYSQL_TLS", env); tls != "" {
			mc.TLSConfig = tls
		}
		dsn = mc.FormatDSN()
	}
	return NormalizeMySQL(dsn)
}

// NormalizeMySQL forces the session settings the copy step relies on:
// parseTime=true, charset=utf8mb4, loc=UTC, and a default database name.
func NormalizeMySQL(dsn string) (MySQL, error) {
	mc, err := mysql.ParseDSN(dsn)
	if err != nil {
		return MySQL{}, fmt.Errorf("invalid mysql DSN: %w", err)
	}
	mc.ParseTime = true
	mc.Loc = time.UTC
	if err := mc.Apply(mysql.Charset("utf8mb4", "")); err != nil {
		return MySQL{}, err
	}
	if mc.DBName == "" {
		mc.DBName = DefaultSourceDatabase
	}
	return MySQL{DSN: mc.FormatDSN(), Addr: mc.Addr, Database: mc.DBName}, nil
}
