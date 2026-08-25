//go:build integration

package handler

// Integration test for the license (legal axis) store→pull round-trip.
//
// Proves the producer-stamped license survives the REAL path end to end: the
// schemaToTranscriptParams mapper writes it, CreateTranscript persists it into
// transcripts.license_id (FK to the migration-025 seeded licenses table), and
// BOTH pull mappers re-emit it — the single-transcript pullTranscriptInfo (fed by
// the full Transcript model) and the list pullInfoFromListRow (fed by the
// ListPullableTranscripts projection that gained t.license_id). An unset license
// is stored NULL and pulled as "" (dropped by PullTranscriptInfo.License's
// omitempty). A mock-querier test could not catch a missing license_id column in
// the list projection; this hits real Postgres.
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:55432/village_test?sslmode=disable" \
//	  go test -tags=integration ./internal/handler/...

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

func TestPull_LicenseRoundTrip_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, pullTestDatabaseURL(t))
	if err != nil {
		t.Skipf("cannot create pool (%v); set TEST_DATABASE_URL", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("cannot reach test database (%v); set TEST_DATABASE_URL", err)
	}
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	owner := pullInsertUser(t, ctx, pool, 980101, "lic-owner")
	defer cleanupOwners(t, ctx, pool, owner)

	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	// store persists one transcript via the REAL mapper + CreateTranscript, so the
	// license travels req.License → params.LicenseID → transcripts.license_id.
	store := func(localID string, lic schema.License) pgtype.UUID {
		t.Helper()
		req := schema.PublishRequest{
			License:  lic,
			Identity: schema.SessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2},
			Model:    schema.ModelInfo{Harness: "claude-code", Model: "m"},
		}
		params := schemaToTranscriptParams(req, "blob/"+localID, 1, "2", sessionorigin.Unknown)
		params.OwnerID = owner
		params.LocalID = localID
		params = completeEncryptedFixtureParams(params)
		var tr sqlc.Transcript
		if err := h.inTxAs(ctx, owner, func(q Querier) error {
			var txErr error
			tr, txErr = q.CreateTranscript(ctx, params)
			return txErr
		}); err != nil {
			t.Fatalf("CreateTranscript(%s, lic=%q): %v", localID, lic, err)
		}
		return tr.ID
	}

	licensedID := store("lic-set", schema.LicenseCCBY)
	noneID := store("lic-none", "")

	// --- single pull (pullTranscriptInfo, fed by the full Transcript model) ---
	for _, tc := range []struct {
		name string
		id   pgtype.UUID
		want schema.License
	}{
		{"licensed", licensedID, schema.LicenseCCBY},
		{"unlicensed", noneID, ""},
	} {
		tr, err := h.queries.GetTranscriptByID(ctx, tc.id)
		if err != nil {
			t.Fatalf("GetTranscriptByID(%s): %v", tc.name, err)
		}
		if got := h.pullTranscriptInfo(ctx, tr).License; got != tc.want {
			t.Errorf("single pull %s: License got %q, want %q", tc.name, got, tc.want)
		}
	}

	// --- list pull (ListPullableTranscripts → pullInfoFromListRow) ---
	rows, err := h.queries.ListPullableTranscripts(ctx, sqlc.ListPullableTranscriptsParams{
		UserID: owner, Limit: 100, Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListPullableTranscripts: %v", err)
	}
	byID := make(map[uuid.UUID]schema.License, len(rows))
	for _, r := range rows {
		byID[uuid.UUID(r.ID.Bytes)] = pullInfoFromListRow(r).License
	}
	if got := byID[uuid.UUID(licensedID.Bytes)]; got != schema.LicenseCCBY {
		t.Errorf("list pull licensed: License got %q, want %q", got, schema.LicenseCCBY)
	}
	if got, ok := byID[uuid.UUID(noneID.Bytes)]; !ok || got != "" {
		t.Errorf("list pull unlicensed: License got %q present=%v, want empty+present", got, ok)
	}
}
