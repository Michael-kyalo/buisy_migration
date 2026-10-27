// Package keycloak reads the users of one realm, either straight from the
// Keycloak Postgres database or through the admin REST API.
package keycloak

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/burnmfg/buisy-migration/internal/config"
	"github.com/jackc/pgx/v5"
)

type User struct {
	ID       string
	Username string
	Email    string
	Enabled  bool
}

type Source interface {
	Users(ctx context.Context) ([]User, error)
	Describe() string
}

func New(cfg config.Keycloak) (Source, error) {
	switch cfg.Mode {
	case "db":
		return &DBSource{URL: cfg.DB.URL, Host: cfg.DB.Host, Realm: cfg.Realm}, nil
	case "api":
		return &APISource{
			BaseURL: cfg.BaseURL, Realm: cfg.Realm, TokenRealm: cfg.TokenRealm,
			ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
			HTTP: &http.Client{Timeout: 30 * time.Second},
		}, nil
	}
	return nil, fmt.Errorf("unknown keycloak mode %q", cfg.Mode)
}

// DBSource reads user_entity, joined to realm by name: realm_id is the realm's
// internal id, which is not always its name.
type DBSource struct {
	URL   string
	Host  string
	Realm string
}

func (s *DBSource) Describe() string { return fmt.Sprintf("keycloak db %s realm %s", s.Host, s.Realm) }

func (s *DBSource) Users(ctx context.Context) ([]User, error) {
	conn, err := pgx.Connect(ctx, s.URL)
	if err != nil {
		return nil, fmt.Errorf("connect to keycloak db: %w", err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `
		SELECT u.id, coalesce(u.username, ''), coalesce(u.email, ''), u.enabled
		FROM user_entity u
		JOIN realm r ON r.id = u.realm_id
		WHERE r.name = $1 AND u.service_account_client_link IS NULL`, s.Realm)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) {
		var u User
		err := r.Scan(&u.ID, &u.Username, &u.Email, &u.Enabled)
		return u, err
	})
}

// APISource pages through /admin/realms/{realm}/users using a client-credentials token.
type APISource struct {
	BaseURL      string
	Realm        string
	TokenRealm   string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
}

const pageSize = 500

func (s *APISource) Describe() string {
	return fmt.Sprintf("keycloak api %s realm %s", s.BaseURL, s.Realm)
}

func (s *APISource) Users(ctx context.Context) ([]User, error) {
	token, err := s.token(ctx)
	if err != nil {
		return nil, err
	}
	var users []User
	for first := 0; ; first += pageSize {
		u := fmt.Sprintf("%s/admin/realms/%s/users?first=%d&max=%d&briefRepresentation=true",
			s.BaseURL, url.PathEscape(s.Realm), first, pageSize)
		var page []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Email    string `json:"email"`
			Enabled  bool   `json:"enabled"`
		}
		if err := s.getJSON(ctx, u, token, &page); err != nil {
			return nil, err
		}
		for _, p := range page {
			if strings.HasPrefix(p.Username, "service-account-") {
				continue
			}
			users = append(users, User{ID: p.ID, Username: p.Username, Email: p.Email, Enabled: p.Enabled})
		}
		if len(page) < pageSize {
			return users, nil
		}
	}
}

func (s *APISource) token(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {s.ClientID},
		"client_secret": {s.ClientSecret},
	}
	endpoint := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", s.BaseURL, url.PathEscape(s.TokenRealm))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("keycloak token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("keycloak token: HTTP %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.AccessToken, nil
}

func (s *APISource) getJSON(ctx context.Context, u, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Static is a fixed user list, for tests.
type Static []User

func (s Static) Users(context.Context) ([]User, error) { return s, nil }
func (s Static) Describe() string                      { return "static keycloak users" }

// Rows adapts users for pgx CopyFrom into (id, username, email, enabled).
func Rows(users []User) [][]any {
	rows := make([][]any, len(users))
	for i, u := range users {
		rows[i] = []any{u.ID, nullable(u.Username), nullable(u.Email), u.Enabled}
	}
	return rows
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
