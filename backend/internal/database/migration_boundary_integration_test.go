//go:build integration

package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type migrationTestBoundary int

const (
	migrationBoundary023 migrationTestBoundary = 23
	migrationBoundary024 migrationTestBoundary = 24
	migrationBoundary026 migrationTestBoundary = 26
	migrationBoundary027 migrationTestBoundary = 27
	migrationBoundary029 migrationTestBoundary = 29
	migrationBoundary030 migrationTestBoundary = 30
	migrationBoundary032 migrationTestBoundary = 32
	migrationBoundary035 migrationTestBoundary = 35
)

func migrateTestDatabaseThrough(t *testing.T, pool *pgxpool.Pool, boundary migrationTestBoundary) {
	t.Helper()
	if err := migrateDatabaseThrough(context.Background(), pool, boundary); err != nil {
		t.Fatalf("prepare historical migration boundary %03d: %v", boundary, err)
	}
}

func migrateDatabaseThrough(ctx context.Context, pool *pgxpool.Pool, boundary migrationTestBoundary) error {
	var found bool
	for _, m := range migrations {
		if migrationTestBoundary(m.version) > boundary {
			break
		}
		if err := runMigration(pool, m); err != nil {
			return fmt.Errorf("apply migration %03d while preparing boundary %03d: %w", m.version, boundary, err)
		}
		if migrationTestBoundary(m.version) == boundary {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("boundary %03d is not a registered migration version", boundary)
	}

	var laterVersion int
	err := pool.QueryRow(ctx, `SELECT COALESCE(MIN(version), 0) FROM schema_migrations WHERE version > $1`, int(boundary)).Scan(&laterVersion)
	if err != nil {
		return fmt.Errorf("check for schema contamination after boundary %03d: %w", boundary, err)
	}
	if laterVersion != 0 {
		return fmt.Errorf("database already contains migration %03d after requested boundary %03d; use a fresh test database rather than weakening historical fixtures", laterVersion, boundary)
	}
	return nil
}
