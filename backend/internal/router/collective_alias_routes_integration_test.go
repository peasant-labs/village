//go:build integration

package router

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
)

// Routing, path-parameter binding, query preservation and request-family
// enforcement execute the actual router/auth/handlers on disposable PostgreSQL.
// There are no objects/transcripts to seed: this focused alias invariant needs
// only current memberships and real database reads, not another storage harness.
func TestCollectiveAliasMountedPostgresParity(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL must name a disposable PostgreSQL database for mounted alias proof")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	owner, other, group := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,github_id,github_username,provider_user_id) VALUES($1,$2,$3::text,$3::text),($4,$5,$6::text,$6::text)`, owner, time.Now().UnixNano(), "alias-owner-"+owner.String(), other, time.Now().UnixNano()+1, "alias-other-"+other.String()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM users WHERE id=ANY($1::uuid[])", []uuid.UUID{owner, other}); err != nil {
			t.Error(err)
		}
	}()
	if _, err := pool.Exec(ctx, "INSERT INTO groups(id,name,created_by,acceptance_mode,data_access) VALUES($1,'alias-fixture',$2,'open','public')", group, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO group_members(group_id,user_id,role) VALUES($1,$2,'owner')", group, owner); err != nil {
		t.Fatal(err)
	}
	const secret = "disposable-alias-jwt-secret"
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatal(err)
	}
	router := New(&config.Config{JWTSecret: secret, FrontendURL: "https://app.example.test"}, pool, nil, titles)
	for _, c := range collectiveAliasRoutes(t) {
		t.Run(c.Name, func(t *testing.T) {
			actor := owner
			if c.Actor == "other" {
				actor = other
			}
			token, err := auth.CreateToken(secret, actor, "alias-viewer")
			if err != nil {
				t.Fatal(err)
			}
			suffix := strings.NewReplacer("{id}", group.String(), "{userID}", owner.String(), "{transcriptID}", uuid.NewString(), "{owner}", "acme", "{name}", "repo").Replace(c.Suffix)
			var previous string
			for _, prefix := range []string{"/api/v1/collectives", "/api/v1/groups"} {
				req := httptest.NewRequest(c.Method, prefix+suffix, strings.NewReader(c.Body)).WithContext(ctx)
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != c.Status {
					t.Fatalf("%s %s status %d want %d: %s", c.Method, req.URL, rec.Code, c.Status, rec.Body.String())
				}
				if previous != "" && rec.Body.String() != previous {
					t.Fatalf("canonical/legacy body mismatch: %s / %s", previous, rec.Body.String())
				}
				previous = rec.Body.String()
			}
		})
	}
}
