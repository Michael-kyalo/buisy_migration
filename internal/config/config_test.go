package config

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

type fakeSecrets map[string]string

func (f fakeSecrets) GetSecretString(_ context.Context, id string) (string, error) {
	if s, ok := f[id]; ok {
		return s, nil
	}
	return "", errors.New("not found")
}

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestGuard(t *testing.T) {
	if err := Guard(Live, false); !errors.Is(err, ErrLiveNotConfirmed) {
		t.Fatalf("live without confirm: got %v", err)
	}
	if err := Guard(Live, true); err != nil {
		t.Fatalf("live with confirm: %v", err)
	}
	if err := Guard(Development, false); err != nil {
		t.Fatalf("development: %v", err)
	}
}

func TestParseEnv(t *testing.T) {
	if e, err := ParseEnv(" Training "); err != nil || e != Training {
		t.Fatalf("got %q, %v", e, err)
	}
	if _, err := ParseEnv("prod"); err == nil {
		t.Fatal("expected error for unknown env")
	}
}

func TestNormalizeMySQL(t *testing.T) {
	m, err := NormalizeMySQL("ro:pw@tcp(buisy.example:3306)/")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"parseTime=true", "charset=utf8mb4"} {
		if !strings.Contains(m.DSN, want) {
			t.Errorf("DSN %q missing %s", m.DSN, want)
		}
	}
	// The driver omits loc from the DSN when it is UTC (its default); check the parsed value.
	if mc, err := mysql.ParseDSN(m.DSN); err != nil || mc.Loc != time.UTC || !mc.ParseTime {
		t.Errorf("parsed DSN: loc=%v parseTime=%v err=%v", mc.Loc, mc.ParseTime, err)
	}
	if mc, _ := NormalizeMySQL("ro:pw@tcp(h:3306)/db?loc=Local&parseTime=false"); !strings.Contains(mc.DSN, "parseTime=true") || strings.Contains(mc.DSN, "loc=Local") {
		t.Errorf("explicit loc/parseTime must be overridden: %s", mc.DSN)
	}
	if m.Database != DefaultSourceDatabase || m.Addr != "buisy.example:3306" {
		t.Errorf("got database %q addr %q", m.Database, m.Addr)
	}
}

func TestLoadNeverFallsBackAcrossEnvs(t *testing.T) {
	l := Loader{Getenv: envFrom(map[string]string{
		"MIGRATE_DELIVERYDB_URL_DEVELOPMENT":    "postgres://u:p@dev/development",
		"MIGRATE_DELIVERYDB_URL":                "postgres://u:p@dev/development",
		"MIGRATE_CUSTOMERAPPDB_URL_DEVELOPMENT": "postgres://u:p@dev/customerappdb",
		"BUISY_MYSQL_DSN_DEVELOPMENT":           "u:p@tcp(dev:3306)/burnmfgsystems_db",
	})}
	if _, err := l.Load(context.Background(), Live); err == nil {
		t.Fatal("live must not pick up development or unsuffixed settings")
	}
	cfg, err := l.Load(context.Background(), Development)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := cfg.Target(DeliveryDB)
	c, _ := cfg.Target(CustomerAppDB)
	if d.Host != "dev" || d.Database != "development" || c.Database != "customerappdb" {
		t.Errorf("targets = %+v", cfg.Targets)
	}
}

func TestLoadRequiresEveryTarget(t *testing.T) {
	l := Loader{Getenv: envFrom(map[string]string{
		"MIGRATE_DELIVERYDB_URL_DEVELOPMENT": "postgres://u:p@dev/development",
		"BUISY_MYSQL_DSN_DEVELOPMENT":        "u:p@tcp(dev:3306)/burnmfgsystems_db",
	})}
	_, err := l.Load(context.Background(), Development)
	if err == nil || !strings.Contains(err.Error(), "MIGRATE_CUSTOMERAPPDB_URL_DEVELOPMENT") {
		t.Fatalf("missing customerappdb settings must be reported, got %v", err)
	}
}

func TestLoadFromSecrets(t *testing.T) {
	l := Loader{
		Getenv: envFrom(map[string]string{
			"MIGRATE_DELIVERYDB_SECRET_ARN_TRAINING":    "arn:pg",
			"MIGRATE_CUSTOMERAPPDB_SECRET_ARN_TRAINING": "arn:pg", // same server, same secret
			"MIGRATE_CUSTOMERAPPDB_DATABASE_TRAINING":   "customerappdb",
			"BUISY_MYSQL_SECRET_ARN_TRAINING":           "arn:my",
		}),
		Secrets: fakeSecrets{
			"arn:pg": `{"username":"mig","password":"p@ss/word","host":"pg.internal","port":5432,"dbname":"training"}`,
			"arn:my": `{"username":"ro","password":"x","host":"my.internal","port":"3306","dbname":"burnmfgsystems_db"}`,
		},
	}
	cfg, err := l.Load(context.Background(), Training)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := cfg.Target(DeliveryDB)
	c, _ := cfg.Target(CustomerAppDB)
	if d.Host != "pg.internal" || d.Database != "training" {
		t.Errorf("deliverydb = %+v", d)
	}
	if c.Host != "pg.internal" || c.Database != "customerappdb" {
		t.Errorf("customerappdb must override the secret's dbname: %+v", c)
	}
	if !strings.Contains(d.URL, "sslmode=require") {
		t.Errorf("target URL %q should default to sslmode=require", d.URL)
	}
	if cfg.Source.Addr != "my.internal:3306" {
		t.Errorf("source = %+v", cfg.Source)
	}
}

func TestLoadKeycloak(t *testing.T) {
	l := Loader{Getenv: envFrom(map[string]string{
		"KEYCLOAK_MODE":                      "api",
		"KEYCLOAK_REALM":                     "burn",
		"KEYCLOAK_BASE_URL_DEVELOPMENT":      "https://kc.dev/",
		"KEYCLOAK_CLIENT_ID":                 "migrator",
		"KEYCLOAK_CLIENT_SECRET_DEVELOPMENT": "s",
	})}
	kc, err := l.LoadKeycloak(context.Background(), Development)
	if err != nil {
		t.Fatal(err)
	}
	if kc.BaseURL != "https://kc.dev" || kc.TokenRealm != "burn" {
		t.Errorf("keycloak = %+v", kc)
	}
	if _, err := l.LoadKeycloak(context.Background(), Live); err == nil {
		t.Fatal("live keycloak must not use development base URL or secret")
	}
}
