//go:build integration

package database

import (
	"bytes"
	"context"
	_ "embed"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/grouped_query_projection.yaml
var groupedQueryProjectionYAML []byte

func TestGeneratedGroupedQueryProjection(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name          string `yaml:"name"`
			Count         *int64 `yaml:"count"`
			Purpose       string `yaml:"purpose"`
			Relationships string `yaml:"relationships"`
			Cyclic        bool   `yaml:"cyclic"`
		} `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(groupedQueryProjectionYAML))
	d.KnownFields(true)
	if err := d.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("trailing grouped query fixture: %v", err)
	}
	names := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || names[c.Name] || c.Relationships == "" {
			t.Fatalf("invalid grouped query case %q", c.Name)
		}
		names[c.Name] = true
	}
	for _, name := range strings.Fields("absent-legacy-graph measured-zero-helper-without-owner measured-positive-self-cycle") {
		if !names[name] {
			t.Fatalf("missing grouped query fixture %q", name)
		}
	}
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	if err := RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			owner := insertFenceOwner(t, ctx, tx)
			if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
				t.Fatal(err)
			}
			id, err := insertFenceTranscript(ctx, tx, owner)
			if err != nil {
				t.Fatal(err)
			}
			const localID = "550e8400-e29b-41d4-a716-446655440000"
			if _, err := tx.Exec(ctx, `UPDATE transcripts SET local_id=$2, input_submission_count=$3, session_purpose=NULLIF($4,''), session_relationships=$5::jsonb WHERE id=$1`, id, localID, c.Count, c.Purpose, c.Relationships); err != nil {
				t.Fatal(err)
			}
			var ownerID, transcriptID pgtype.UUID
			if err := ownerID.Scan(owner); err != nil {
				t.Fatal(err)
			}
			if err := transcriptID.Scan(id); err != nil {
				t.Fatal(err)
			}
			queries := sqlc.New(tx)
			rows, err := queries.ListGroupedTranscriptCandidates(ctx, sqlc.ListGroupedTranscriptCandidatesParams{ViewerID: ownerID, Tags: []string{}})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].ID != transcriptID {
				t.Fatalf("owner-scoped generated rows=%v, want exactly the fixture transcript", rows)
			}
			if rows[0].InputSubmissionCount.Valid != (c.Count != nil) || c.Count != nil && rows[0].InputSubmissionCount.Int64 != *c.Count || rows[0].SessionPurpose.String != c.Purpose {
				t.Fatal("generated query lost nullable count or purpose")
			}
			cycles, err := queries.ListGroupedCyclicHelpers(ctx, []pgtype.UUID{transcriptID})
			if err != nil {
				t.Fatal(err)
			}
			if c.Cyclic && (len(cycles) != 1 || cycles[0] != transcriptID) || !c.Cyclic && len(cycles) != 0 {
				t.Fatalf("cycle projection=%v want cyclic=%v", cycles, c.Cyclic)
			}
			probe, err := queries.GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: ownerID, LocalID: localID})
			if err != nil || probe != transcriptID {
				t.Fatalf("owner-local probe=%v error=%v", probe, err)
			}
			collective, err := queries.ListCollectiveGroupedCandidates(ctx, sqlc.ListCollectiveGroupedCandidatesParams{ViewerID: ownerID, GroupID: transcriptID, RouteKind: "collective"})
			if err != nil {
				t.Fatal(err)
			}
			if len(collective) != 0 {
				t.Fatal("missing collective unexpectedly returned candidates")
			}
		})
	}
}
