//go:build integration

package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
)

func TestRouteBytesPostgres(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	account := uuid.New()
	login := "golden-" + strings.ReplaceAll(account.String(), "-", "")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,github_id,github_username,provider_user_id)
		VALUES($1,$2,$3::text,$3::text)`, account, time.Now().UnixNano(), login); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, account); err != nil {
			t.Errorf("remove test user: %v", err)
		}
	})
	orgNull, orgAvatar := login+"-null", login+"-avatar"
	if _, err := pool.Exec(ctx, `INSERT INTO user_github_orgs(user_id,org_login,org_id,visible,avatar_url)
		VALUES ($1,$2,1,true,NULL),($1,$3,2,true,'https://example.test/avatar.png')`, account, orgNull, orgAvatar); err != nil {
		t.Fatal(err)
	}
	const secret = "route-bytes-test-only-secret"
	token, err := auth.CreateToken(secret, account, login)
	if err != nil {
		t.Fatal(err)
	}
	routes := New(&config.Config{JWTSecret: secret, FrontendURL: "https://example.test"}, pool, nil, nil)
	values := map[string]string{"org_null": orgNull, "org_avatar": orgAvatar}
	replace := func(value string) string {
		for k, v := range values {
			value = strings.ReplaceAll(value, "{{"+k+"}}", v)
		}
		return value
	}
	for _, c := range routeByteCases(t) {
		if !c.Database {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			req := httptest.NewRequest(c.Method, replace(c.Path), strings.NewReader(c.RequestBody))
			req.Header.Set("Authorization", "Bearer "+token)
			for k, v := range c.Headers {
				req.Header.Set(k, replace(v))
			}
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, req)
			if c.Name == "create-api-key" {
				var got struct {
					ID  string `json:"id"`
					Key string `json:"key"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if !regexp.MustCompile(`^peasant_[0-9a-f]{64}$`).MatchString(got.Key) {
					t.Fatalf("bad key shape %q", got.Key)
				}
				var id uuid.UUID
				var created time.Time
				if err := pool.QueryRow(ctx, `SELECT id,created_at FROM api_keys WHERE user_id=$1`, account).Scan(&id, &created); err != nil {
					t.Fatal(err)
				}
				if got.ID != id.String() {
					t.Fatalf("returned key id %q does not match stored %s", got.ID, id)
				}
				values["id"], values["key"], values["created_at"] = got.ID, got.Key, created.Format(time.RFC3339Nano)
			}
			if w.Code != c.Status || w.Body.String() != replace(c.Body) {
				t.Fatalf("status/body = %d %q; want %d %q", w.Code, w.Body.String(), c.Status, replace(c.Body))
			}
			for k, v := range c.Want {
				if got := w.Header().Get(k); got != replace(v) {
					t.Errorf("%s = %q, want %q", k, got, replace(v))
				}
			}
			if c.Name == "list-api-keys" && (strings.Contains(w.Body.String(), values["key"]) || strings.Contains(w.Body.String(), "key_hash")) {
				t.Fatal("list exposed the plaintext key or key hash")
			}
			if c.Name == "peasant-revoke-api-key" {
				var revoked *time.Time
				if err := pool.QueryRow(ctx, `SELECT revoked_at FROM api_keys WHERE id=$1`, values["id"]).Scan(&revoked); err != nil || revoked == nil {
					t.Fatal(fmt.Errorf("revocation was not recorded: %v", err))
				}
			}
		})
	}
}
