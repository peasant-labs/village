//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/config"
)

func TestHealthProbeAppliedMigrations(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	for _, c := range loadHealthProbeFixtures(t) {
		if c.Stage != "n" {
			continue
		}
		switch c.Name {
		case "health-migrations-report-applied-version", "health-migrations-not-compiled-target",
			"health-carries-no-transcript-fact", "health-response-members":
		default:
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			setHealthBuildRevision(t, c.BuildRevision)
			targetPool := pool
			if c.Name == "health-migrations-not-compiled-target" {
				// A connection-private registry represents an older applied state,
				// independently of the binary's compiled migration list. It vanishes
				// with this pool and cannot change the migrated test database.
				cfg := pool.Config().Copy()
				cfg.MaxConns = 1
				cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
					if _, err := conn.Exec(ctx, "CREATE TEMP TABLE schema_migrations (version integer PRIMARY KEY)"); err != nil {
						return err
					}
					_, err := conn.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES ($1)", c.AppliedVersion)
					return err
				}
				var err error
				targetPool, err = pgxpool.NewWithConfig(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer targetPool.Close()
			}
			h := NewWithTitlePipeline(&config.Config{}, targetPool, nil, nil)
			body, _ := healthResponse(t, h)
			var actual int64
			if err := json.Unmarshal(body["migrations"], &actual); err != nil {
				t.Fatalf("missing or invalid applied migration: %v", err)
			}
			var expected int64
			if err := targetPool.QueryRow(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&expected); err != nil {
				t.Fatal(err)
			}
			if actual != expected {
				t.Fatalf("health migration = %d, database applied = %d", actual, expected)
			}
			if c.Name == "health-migrations-not-compiled-target" && (actual != int64(c.AppliedVersion) || actual >= int64(c.LaterVersion)) {
				t.Fatalf("older applied version %d did not remain below target %d", actual, c.LaterVersion)
			}
			if c.Name == "health-carries-no-transcript-fact" || c.Name == "health-response-members" {
				want := map[string]bool{"status": true, "buildRevision": true, "migrations": true, "serves": true}
				got := make(map[string]bool, len(body))
				for key := range body {
					got[key] = true
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("health members = %v, want closed set %v", got, want)
				}
				var status string
				if err := json.Unmarshal(body["status"], &status); err != nil || status != "ok" {
					t.Fatalf("unexpected status %q: %v", status, err)
				}
				var revision string
				if err := json.Unmarshal(body["buildRevision"], &revision); err != nil || revision != c.BuildRevision {
					t.Fatalf("unexpected revision %q: %v", revision, err)
				}
				if got := healthTraits(t, body); !reflect.DeepEqual(got, []string{"publish-revision-n"}) {
					t.Fatalf("unexpected health traits %v", got)
				}
			}
		})
	}
}
