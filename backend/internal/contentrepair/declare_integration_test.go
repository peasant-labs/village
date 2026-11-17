//go:build integration

package contentrepair

import (
	"bytes"
	"context"
	_ "embed"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/declare.yaml
var declareFixtureYAML []byte

type declareCase struct {
	Name         string `yaml:"name"`
	Read         string `yaml:"read"`
	Descriptor   string `yaml:"descriptor"`
	Phase        string `yaml:"phase"`
	FailureClass string `yaml:"failure_class"`
	StateAfter   string `yaml:"state_after"`
}

// scriptedBlobStore answers only ReadBounded, the one call Declare makes.
type scriptedBlobStore struct {
	storage.TranscriptBlobStore
	identity storage.ContentIdentity
	err      error
}

func (s scriptedBlobStore) ReadBounded(context.Context, uuid.UUID, storage.BlobDescriptor, storage.LoadedContentIdentity, int64) ([]byte, storage.ContentIdentity, error) {
	return nil, s.identity, s.err
}

func TestDeclareUnavailableRealPostgres(t *testing.T) {
	var fixture struct {
		Cases []declareCase `yaml:"cases"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(declareFixtureYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"lookup-uncertain-is-declared-unavailable":            false,
		"undecodable-descriptor-is-refused-as-uncertain":      false,
		"object-authenticating-to-stored-identity-is-refused": false,
	}
	for _, c := range fixture.Cases {
		if seen, known := required[c.Name]; !known || seen {
			t.Fatalf("unknown or duplicate declare case %q", c.Name)
		}
		required[c.Name] = true
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("missing declare fixture case %q", name)
		}
	}

	ctx := context.Background()
	pool := scratchDatabase(t)
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	stored, err := storage.NewContentIdentity(schema.ComputeTranscriptHash([]byte("fixture")), 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			blobKey := "transcripts/" + uuid.NewString() + ".bin"
			switch c.Descriptor {
			case "":
			case "undecodable":
				// A key outside the opaque generation form cannot build a descriptor.
				blobKey = "legacy/" + uuid.NewString()
			default:
				t.Fatalf("fixture descriptor %q is unknown", c.Descriptor)
			}
			id := seedPresentTranscript(t, ctx, pool, stored, blobKey)
			var store scriptedBlobStore
			switch c.Read {
			case "lookup-uncertain":
				store.err = storage.ErrObjectLookupUncertain
			case "stored-identity":
				store.identity = stored
			case "absent-provider":
				store.err = storage.ErrObjectAbsentProvider
			default:
				t.Fatalf("fixture read %q is unknown", c.Read)
			}
			opts := Options{
				RepairID: uuid.New(), TranscriptID: id, Action: DeclareUnavailable,
				ActorID: uuid.New(), AuthorizationRef: "ticket-1", Reason: "object lost", ExpectedRevision: 1,
			}
			outcome, err := Declare(ctx, Deps{Pool: pool, Queries: sqlc.New(pool), Blobs: store, Now: time.Now}, opts)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Phase.String() != c.Phase || outcome.FailureClass.String() != c.FailureClass {
				t.Fatalf("outcome phase=%s class=%q, want %s %q", outcome.Phase, outcome.FailureClass, c.Phase, c.FailureClass)
			}
			var state string
			var revision int64
			var hash *string
			var size *int64
			if err := pool.QueryRow(ctx, `SELECT content_state, content_revision, content_hash, blob_size_bytes FROM transcripts WHERE id=$1`, id).Scan(&state, &revision, &hash, &size); err != nil {
				t.Fatal(err)
			}
			if state != c.StateAfter || revision != 1 || hash == nil || *hash != string(stored.Hash()) || size == nil || *size != stored.PlaintextSize() {
				t.Fatalf("row after repair: state=%s revision=%d hash=%v size=%v", state, revision, hash, size)
			}
			rows, err := pool.Query(ctx, `SELECT phase, COALESCE(failure_class,''), prior_revision, resulting_revision, prior_identity_hash, resulting_identity_hash, operator_actor FROM transcript_content_repairs WHERE repair_id=$1`, opts.RepairID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			count := 0
			for rows.Next() {
				var phase, class string
				var prior, resulting int64
				var priorHash, resultingHash *string
				var actor uuid.UUID
				if err := rows.Scan(&phase, &class, &prior, &resulting, &priorHash, &resultingHash, &actor); err != nil {
					t.Fatal(err)
				}
				if phase != c.Phase || class != c.FailureClass || prior != 1 || resulting != 1 ||
					priorHash == nil || resultingHash == nil || *priorHash != *resultingHash || actor != opts.ActorID {
					t.Fatalf("ledger row phase=%s class=%q revisions=%d/%d actor=%s", phase, class, prior, resulting, actor)
				}
				count++
			}
			if count != 1 {
				t.Fatalf("want exactly one ledger row, got %d", count)
			}
		})
	}
}

func seedPresentTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, identity storage.ContentIdentity, key string) uuid.UUID {
	t.Helper()
	owner, id := uuid.New(), uuid.New()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)", database.SystemActorID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO users(id,github_id,github_username,provider,provider_user_id) VALUES($1,$2,$3,'github',$4)", owner, int64(owner[0])+900000, owner.String(), owner.String()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO transcripts(id,owner_id,local_id,visibility,model_provider,blob_key,blob_size_bytes,schema_version,content_hash,wrapped_data_key,encryption_algorithm,key_version,project_hash) VALUES($1,$2,$3,'private','claude',$4,$5,'2',$6,$7,$8,1,$9)",
		id, owner, id.String(), key, identity.PlaintextSize(), string(identity.Hash()), []byte("wrapped-key"), string(storage.EncryptionAES256GCMRandomNonceV1), "b7c2a90e4d18"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

// scratchDatabase creates an isolated database so ledger rows need no cleanup.
func scratchDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required; refusing to skip real PostgreSQL repair proof")
	}
	base, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	adminCfg := base.Copy()
	adminCfg.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(ctx, adminCfg)
	if err != nil {
		t.Fatal(err)
	}
	name := "village_repair_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create isolated repair database (TEST_DATABASE_URL user needs CREATEDB): %v", err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		admin.Close()
	})
	target := base.Copy()
	target.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
