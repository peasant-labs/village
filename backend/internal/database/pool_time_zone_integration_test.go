//go:build integration

package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPoolConfigReadsTimesInUTC proves a pool built by PoolConfig scans stored
// times into UTC, alone or in an array, whatever zone the database session or
// the test process is in. The session is pinned to a non-UTC zone so the proof
// does not depend on how the test database is configured.
func TestPoolConfigReadsTimesInUTC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://test:test@localhost:5432/village_test?sslmode=disable"
	}
	cfg, err := PoolConfig(dsn)
	if err != nil {
		t.Fatalf("PoolConfig: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "America/Denver"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Skipf("PostgreSQL unavailable while opening the pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("PostgreSQL unavailable; preflight TEST_DATABASE_URL before trusting this skip: %v", err)
	}

	const stored = "2026-01-02T03:04:05Z"
	want, err := time.Parse(time.RFC3339, stored)
	if err != nil {
		t.Fatal(err)
	}

	var single pgtype.Timestamptz
	var asTime time.Time
	var list []time.Time
	if err := pool.QueryRow(ctx,
		"SELECT $1::timestamptz, $1::timestamptz, ARRAY[$1::timestamptz, $1::timestamptz]",
		stored,
	).Scan(&single, &asTime, &list); err != nil {
		t.Fatalf("scan stored times: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("array scan returned %d times, want 2", len(list))
	}
	got := map[string]time.Time{
		"pgtype.Timestamptz":   single.Time,
		"time.Time":            asTime,
		"first array element":  list[0],
		"second array element": list[1],
	}
	for name, at := range got {
		if at.Location() != time.UTC {
			t.Errorf("%s scanned in %v, want UTC", name, at.Location())
		}
		if !at.Equal(want) {
			t.Errorf("%s scanned %v, want the instant %v", name, at, want)
		}
		if s := at.Format(time.RFC3339); s != stored {
			t.Errorf("%s prints %q, want %q", name, s, stored)
		}
	}
}
