package handler

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/account-invalid-timestamps.yaml
var accountTimestampYAML []byte

func TestAccountResponsesRefuseMissingTimestamps(t *testing.T) {
	cases, err := decodeFixtureRows[struct {
		Name string `yaml:"name"`
		Kind string `yaml:"kind"`
	}](accountTimestampYAML)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]struct{}{}
	for _, c := range cases {
		if _, exists := seen[c.Name]; c.Name == "" || exists {
			t.Fatalf("empty or duplicate case name %q", c.Name)
		}
		seen[c.Name] = struct{}{}
		t.Run(c.Name, func(t *testing.T) {
			valid := pgtype.Timestamptz{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Valid: true}
			user := sqlc.User{CreatedAt: valid, UpdatedAt: valid}
			key := sqlc.ApiKey{CreatedAt: valid}
			switch c.Kind {
			case "account-created":
				user.CreatedAt.Valid = false
			case "account-updated":
				user.UpdatedAt.Valid = false
			case "key-created":
				key.CreatedAt.Valid = false
			default:
				t.Fatalf("unknown kind %q", c.Kind)
			}
			q := &mockQuerier{
				getUserByID: func(context.Context, pgtype.UUID) (sqlc.User, error) { return user, nil },
				createAPIKey: func(context.Context, sqlc.CreateAPIKeyParams) (sqlc.ApiKey, error) {
					return key, nil
				},
			}
			h := newTestHandler(q, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
			if c.Kind == "key-created" {
				req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/api-keys", strings.NewReader("{}"))
			}
			req = req.WithContext(withTestUser(req.Context()))
			rec := httptest.NewRecorder()
			if c.Kind == "key-created" {
				h.CreateAPIKey(rec, req)
			} else {
				h.Me(rec, req)
			}
			if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "0001-01-01T00:00:00Z") || !strings.Contains(rec.Body.String(), "non-null") {
				t.Fatalf("status %d body %s: expected a contract error without a zero timestamp", rec.Code, rec.Body.String())
			}
		})
	}
	assertExactTitleFixtureNames(t, "account-invalid-timestamps", seen, []string{"missing-account-created-at", "missing-account-updated-at", "missing-key-created-at"})
}
