//go:build integration

package database

import (
	"context"
	"testing"
)

// TestMigration038DeliveryIdempotency proves the delivery ledger's primary key
// is what makes a replayed delivery a no-op: the first insert takes the row,
// a second insert of the same id affects zero rows, and a different id still
// inserts.
func TestMigration038DeliveryIdempotency(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)

	first, err := pool.Exec(ctx,
		`INSERT INTO github_webhook_deliveries (delivery_id) VALUES ($1) ON CONFLICT (delivery_id) DO NOTHING`, "delivery-1")
	if err != nil {
		t.Fatalf("first delivery insert: %v", err)
	}
	if got := first.RowsAffected(); got != 1 {
		t.Fatalf("first delivery inserted %d rows, want 1", got)
	}

	replay, err := pool.Exec(ctx,
		`INSERT INTO github_webhook_deliveries (delivery_id) VALUES ($1) ON CONFLICT (delivery_id) DO NOTHING`, "delivery-1")
	if err != nil {
		t.Fatalf("replay insert: %v", err)
	}
	if got := replay.RowsAffected(); got != 0 {
		t.Fatalf("replay inserted %d rows, want 0", got)
	}

	second, err := pool.Exec(ctx,
		`INSERT INTO github_webhook_deliveries (delivery_id) VALUES ($1) ON CONFLICT (delivery_id) DO NOTHING`, "delivery-2")
	if err != nil {
		t.Fatalf("second delivery insert: %v", err)
	}
	if got := second.RowsAffected(); got != 1 {
		t.Fatalf("second delivery inserted %d rows, want 1", got)
	}

	var rows int
	var nullReceived int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE received_at IS NULL) FROM github_webhook_deliveries`).Scan(&rows, &nullReceived); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || nullReceived != 0 {
		t.Fatalf("delivery rows = %d with %d null received_at, want 2 with 0", rows, nullReceived)
	}
}
