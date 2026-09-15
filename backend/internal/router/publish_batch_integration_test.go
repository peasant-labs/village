//go:build integration

package router

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/database"
)

func TestRegisteredPublishBatchLeavesDatabaseUnchanged(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to a disposable PostgreSQL database; the encrypted aggregate rejects this skip")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var value string
		err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
		 'transcripts',(SELECT jsonb_agg(to_jsonb(t) ORDER BY t.id) FROM transcripts t),
		 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.seq) FROM transcript_governance_events_audit a),
		 'attempts',(SELECT jsonb_agg(to_jsonb(s) ORDER BY s.id) FROM transcript_share_attempts s),
		 'shares',(SELECT jsonb_agg(to_jsonb(s) ORDER BY s.transcript_id,s.group_id) FROM transcript_shares s)
		)::text`).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	for _, c := range batchRefusalCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			before := snapshot()
			assertRegisteredBatchRefusal(t, pool, c)
			if snapshot() != before {
				t.Fatal("registered batch refusal changed transcript, audit, attempt, or share rows")
			}
		})
	}
}
