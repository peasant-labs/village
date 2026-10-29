//go:build integration

package database

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/migration-048-checks.yaml
var migration048ChecksYAML []byte

//go:embed testdata/legacy-lineage.yaml
var legacyLineageYAML []byte

type migration048Check struct {
	Name         string `yaml:"name"`
	WriterMarker bool   `yaml:"writer_marker"`
	Statement    string `yaml:"statement"`
	Expect       string `yaml:"expect"`
	CheckSQL     string `yaml:"check_sql"`
	Want         string `yaml:"want"`
}

var requiredMigration048Checks = []string{
	"metadata_all_null_ok",
	"metadata_all_set_ok",
	"metadata_key_without_hash_refused",
	"metadata_key_bad_shape_refused",
	"metadata_negative_size_refused",
	"metadata_update_without_writer_marker_refused",
	"rewrite_row_cascades_with_transcript",
	"rewrite_bad_hash_refused",
}

type legacyLineageCase struct {
	Name     string `yaml:"name"`
	Rewrites []struct {
		Replaced  string `yaml:"replaced"`
		Installed string `yaml:"installed"`
		Legacy    bool   `yaml:"legacy"`
	} `yaml:"rewrites"`
	Query string `yaml:"query"`
	Want  bool   `yaml:"want"`
}

var requiredLegacyLineageCases = []string{
	"raw_jsonl_rewrite_is_legacy",
	"payload_rewrite_is_not_legacy",
	"two_step_chain_from_legacy_is_legacy",
	"rewrite_of_a_legacy_rewrite_stays_legacy",
	"publisher_hash_is_not_legacy",
}

func decodeStrictYAML[T any](t *testing.T, raw []byte) []T {
	t.Helper()
	var cases []T
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("fixture has an extra YAML document: %v", err)
	}
	return cases
}

func requireNames(t *testing.T, got []string, required []string) {
	t.Helper()
	present := map[string]bool{}
	for _, name := range got {
		if name == "" || present[name] {
			t.Fatalf("empty or duplicate fixture case %q", name)
		}
		present[name] = true
	}
	for _, name := range required {
		if !present[name] {
			t.Fatalf("the fixture no longer contains %q; restore it", name)
		}
	}
}

func hashNamed(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// seedMigration048Transcript commits one encrypted-shape transcript and
// returns its id and owner id.
func seedMigration048Transcript(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
		t.Fatal(err)
	}
	owner := insertFenceOwner(t, ctx, tx)
	id, err := insertFenceTranscript(ctx, tx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id, owner
}

func TestMigration048_Checks(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	cases := decodeStrictYAML[migration048Check](t, migration048ChecksYAML)
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.Name)
	}
	requireNames(t, names, requiredMigration048Checks)

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			id, owner := seedMigration048Transcript(t, pool)
			sub := strings.NewReplacer("{transcript_id}", id, "{owner_id}", owner, "{hash}", hashNamed("fixture"))
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
				t.Fatal(err)
			}
			if c.WriterMarker {
				if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true)"); err != nil {
					t.Fatal(err)
				}
			}
			_, execErr := tx.Exec(ctx, sub.Replace(c.Statement))
			if c.Expect == "ok" {
				if execErr != nil {
					t.Fatalf("statement refused: %v", execErr)
				}
			} else if execErr == nil || !strings.Contains(execErr.Error(), c.Expect) {
				t.Fatalf("statement error = %v, want it to contain %q", execErr, c.Expect)
			}
			if c.CheckSQL != "" {
				var got string
				if err := tx.QueryRow(ctx, sub.Replace(c.CheckSQL)).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != c.Want {
					t.Fatalf("check returned %q, want %q", got, c.Want)
				}
			}
		})
	}
}

// catalog048 renders the parts of the transcripts catalog migration 048 touches.
func catalog048(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var out []string
	collect := func(query string) {
		rows, err := pool.Query(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			out = append(out, line)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	collect(`SELECT 'column ' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '')
		FROM information_schema.columns WHERE table_name = 'transcripts' AND table_schema = current_schema() ORDER BY column_name`)
	collect(`SELECT 'constraint ' || conname || ' ' || pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'transcripts'::regclass ORDER BY conname`)
	collect(`SELECT 'trigger ' || pg_get_triggerdef(oid) FROM pg_trigger WHERE tgrelid = 'transcripts'::regclass AND NOT tgisinternal ORDER BY tgname`)
	collect(`SELECT 'table ' || tablename FROM pg_tables WHERE tablename = 'transcript_content_rewrites' AND schemaname = current_schema()`)
	return strings.Join(out, "\n")
}

func TestMigration048_RoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	for _, m := range migrations {
		if m.version >= 48 {
			continue
		}
		if err := runMigration(pool, m); err != nil {
			t.Fatalf("run migration %d: %v", m.version, err)
		}
	}
	before := catalog048(t, pool)

	upSQL, err := migrationsFS.ReadFile("migrations/048_transcript_publish_evidence.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	downSQL, err := migrationsFS.ReadFile("migrations/048_transcript_publish_evidence.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("up: %v", err)
	}
	afterUp := catalog048(t, pool)
	if afterUp == before {
		t.Fatal("the up migration changed nothing in the catalog")
	}
	if _, err := pool.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got := catalog048(t, pool); got != before {
		t.Fatalf("down did not restore the catalog\nbefore:\n%s\n\nafter down:\n%s", before, got)
	}
	if _, err := pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if got := catalog048(t, pool); got != afterUp {
		t.Fatalf("second up reached a different catalog\nfirst:\n%s\n\nsecond:\n%s", afterUp, got)
	}
}

func TestLegacyLineage(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	queries := sqlc.New(pool)
	cases := decodeStrictYAML[legacyLineageCase](t, legacyLineageYAML)
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.Name)
	}
	requireNames(t, names, requiredLegacyLineageCases)

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			id, _ := seedMigration048Transcript(t, pool)
			var transcriptID pgtype.UUID
			if err := transcriptID.Scan(id); err != nil {
				t.Fatal(err)
			}
			for _, r := range c.Rewrites {
				if err := queries.RecordTranscriptContentRewrite(ctx, sqlc.RecordTranscriptContentRewriteParams{
					TranscriptID:         transcriptID,
					ReplacedContentHash:  hashNamed(r.Replaced),
					InstalledContentHash: hashNamed(r.Installed),
					ReplacedWasLegacy:    r.Legacy,
				}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := queries.IsLegacyLineageContent(ctx, sqlc.IsLegacyLineageContentParams{
				TranscriptID: transcriptID,
				ContentHash:  hashNamed(c.Query),
			})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.Want {
				t.Fatalf("IsLegacyLineageContent = %v, want %v", got, c.Want)
			}
		})
	}

	// Recording the same installed hash twice keeps the first row.
	id, _ := seedMigration048Transcript(t, pool)
	var transcriptID pgtype.UUID
	if err := transcriptID.Scan(id); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []bool{true, false} {
		if err := queries.RecordTranscriptContentRewrite(ctx, sqlc.RecordTranscriptContentRewriteParams{
			TranscriptID: transcriptID, ReplacedContentHash: hashNamed("a"), InstalledContentHash: hashNamed("b"), ReplacedWasLegacy: legacy,
		}); err != nil {
			t.Fatalf("record (legacy=%v): %v", legacy, err)
		}
	}
	got, err := queries.IsLegacyLineageContent(ctx, sqlc.IsLegacyLineageContentParams{TranscriptID: transcriptID, ContentHash: hashNamed("b")})
	if err != nil || !got {
		t.Fatalf("a repeated record replaced the first row: got %v, err %v", got, err)
	}
}
