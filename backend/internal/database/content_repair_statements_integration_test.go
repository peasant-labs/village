//go:build integration

package database

import (
	"bytes"
	"context"
	_ "embed"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/content-repair-statements.yaml
var contentRepairStatementsYAML []byte

type contentRepairStatementFixture struct {
	Cases []struct {
		Name      string `yaml:"name"`
		PriorHash string `yaml:"prior_hash"`
		PriorSize *int64 `yaml:"prior_size"`
	} `yaml:"cases"`
}

func TestRepairTranscriptContentIdentityRawPreconditions(t *testing.T) {
	var fixture contentRepairStatementFixture
	decoder := yaml.NewDecoder(bytes.NewReader(contentRepairStatementsYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"malformed-null-size-cleared":     false,
		"malformed-negative-size-cleared": false,
	}
	for _, c := range fixture.Cases {
		if _, exists := required[c.Name]; !exists || required[c.Name] {
			t.Fatalf("unknown or duplicate repair statement case %q", c.Name)
		}
		required[c.Name] = true
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("missing repair statement fixture %q", name)
		}
	}

	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			owner := insertFenceOwner(t, ctx, tx)
			mustAbsenteeExec(t, ctx, tx, `SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)`, owner)
			id, err := insertFenceTranscript(ctx, tx, owner)
			if err != nil {
				t.Fatal(err)
			}
			sibling, err := insertFenceTranscript(ctx, tx, owner)
			if err != nil {
				t.Fatal(err)
			}
			mustAbsenteeExec(t, ctx, tx, `UPDATE transcripts SET content_hash=$1,blob_size_bytes=$2 WHERE id=$3`, c.PriorHash, c.PriorSize, id)
			parse := func(value string) pgtype.UUID {
				parsed, err := uuid.Parse(value)
				if err != nil {
					t.Fatal(err)
				}
				return pgtype.UUID{Bytes: parsed, Valid: true}
			}
			params := sqlc.RepairTranscriptContentIdentityParams{
				ID: parse(id), ContentRevision: 1, ExpectedContentRevision: 1,
				ExpectedHash: pgtype.Text{String: c.PriorHash, Valid: true},
			}
			if c.PriorSize != nil {
				params.ExpectedSize = pgtype.Int8{Int64: *c.PriorSize, Valid: true}
			}
			q := sqlc.New(tx)
			affected, err := q.RepairTranscriptContentIdentity(ctx, params)
			if err != nil || affected != 1 {
				t.Fatalf("malformed row must clear with its raw expected pair: affected=%d err=%v", affected, err)
			}
			params.ID = parse(sibling)
			affected, err = q.RepairTranscriptContentIdentity(ctx, params)
			if err != nil || affected != 0 {
				t.Fatalf("malformed precondition must not match the identity-free sibling: affected=%d err=%v", affected, err)
			}
			var cleared, untouched bool
			err = tx.QueryRow(ctx, `SELECT
				(SELECT content_hash IS NULL AND blob_size_bytes IS NULL AND segmented_identity_hash IS NULL
					AND segmented_identity_size IS NULL AND content_revision=1 FROM transcripts WHERE id=$1),
				(SELECT content_hash IS NULL AND blob_size_bytes IS NULL AND segmented_identity_hash IS NULL
					AND segmented_identity_size IS NULL AND content_revision=1 FROM transcripts WHERE id=$2)`, id, sibling).Scan(&cleared, &untouched)
			if err != nil || !cleared || !untouched {
				t.Fatalf("cleared=%t untouched=%t err=%v", cleared, untouched, err)
			}
		})
	}
}
