//go:build integration

package contentrepair

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/clear.yaml
var clearFixtureYAML []byte

type clearCase struct {
	Name             string `yaml:"name"`
	Hash             string `yaml:"hash"`
	Size             *int64 `yaml:"size"`
	Descriptor       string `yaml:"descriptor"`
	ExpectedRevision int64  `yaml:"expected_revision"`
	Read             string `yaml:"read"`
	ExpectReads      int    `yaml:"expect_reads"`
	Phase            string `yaml:"phase"`
	FailureClass     string `yaml:"failure_class"`
	Cleared          bool   `yaml:"cleared"`
}

const malformedHash = "legacy-malformed"

func TestClearMalformedIdentityRealPostgres(t *testing.T) {
	var fixture struct {
		Cases []clearCase `yaml:"cases"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(clearFixtureYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"malformed-hash-null-size-is-cleared":            false,
		"malformed-hash-negative-size-is-cleared":        false,
		"identity-free-row-is-not-matched":               false,
		"valid-identity-is-refused":                      false,
		"wrong-expected-revision-is-refused":             false,
		"absent-object-is-refused-as-uncertain":          false,
		"key-failure-is-refused-as-uncertain":            false,
		"undecodable-descriptor-is-refused-as-uncertain": false,
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if seen[c.Name] {
			t.Fatalf("duplicate clear case %q", c.Name)
		}
		seen[c.Name] = true
		if _, isRequired := required[c.Name]; isRequired {
			required[c.Name] = true
		}
	}
	for name, present := range required {
		if !present {
			t.Fatalf("missing clear fixture case %q", name)
		}
	}

	ctx := context.Background()
	pool := scratchDatabase(t)
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	canonical := schema.ComputeTranscriptHash([]byte("fixture"))
	authenticated, err := storage.NewContentIdentity(canonical, 7)
	if err != nil {
		t.Fatal(err)
	}
	rawHash := func(c clearCase) *string {
		switch fixtureValue(c.Hash) {
		case fixtureMalformed:
			h := malformedHash
			return &h
		case "valid":
			return &canonical
		case "null":
			return nil
		}
		t.Fatalf("fixture hash %q is unknown", c.Hash)
		return nil
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			blobKey := "transcripts/" + uuid.NewString() + ".bin"
			switch c.Descriptor {
			case "":
			case "undecodable":
				blobKey = "legacy/" + uuid.NewString()
			default:
				t.Fatalf("fixture descriptor %q is unknown", c.Descriptor)
			}
			prior := seedRow{blobKey: blobKey, hash: rawHash(c), size: c.Size}
			id := seedTranscriptRow(t, ctx, pool, prior)
			// An identity-free row beside the target must never be matched by
			// the target's malformed expected hash.
			sibling := seedTranscriptRow(t, ctx, pool, seedRow{blobKey: "transcripts/" + uuid.NewString() + ".bin"})

			reads := 0
			store := scriptedBlobStore{reads: &reads}
			switch c.Read {
			case "authenticates":
				store.identity = authenticated
			case "absent-provider":
				store.err = storage.ErrObjectAbsentProvider
			case "lookup-uncertain":
				store.err = storage.ErrObjectLookupUncertain
			case "key-failure":
				store.err = errors.New("key custody unavailable")
			default:
				t.Fatalf("fixture read %q is unknown", c.Read)
			}
			revision := c.ExpectedRevision
			if revision == 0 {
				revision = 1
			}
			opts := Options{
				RepairID: uuid.New(), TranscriptID: id, Action: ClearMalformedIdentity,
				ActorID: uuid.New(), AuthorizationRef: "ticket-2", Reason: "malformed identity", ExpectedRevision: revision,
			}
			outcome, err := Clear(ctx, Deps{Pool: pool, Queries: sqlc.New(pool), Blobs: store, Now: time.Now}, opts)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Phase.String() != c.Phase || outcome.FailureClass.String() != c.FailureClass || outcome.ContentRevision != 1 {
				t.Fatalf("outcome phase=%s class=%q revision=%d, want %s %q 1", outcome.Phase, outcome.FailureClass, outcome.ContentRevision, c.Phase, c.FailureClass)
			}
			if reads != c.ExpectReads {
				t.Fatalf("object reads = %d, want %d", reads, c.ExpectReads)
			}

			wantHash := prior.hash
			wantSize := prior.size
			if c.Cleared {
				wantHash, wantSize = nil, nil
			}
			assertIdentityColumns(t, ctx, pool, id, wantHash, wantSize)
			assertIdentityColumns(t, ctx, pool, sibling, nil, nil)

			rows, err := pool.Query(ctx, `SELECT phase, COALESCE(failure_class,''), prior_revision, resulting_revision,
				prior_identity_hash, prior_identity_size, resulting_identity_hash, resulting_identity_size, operator_actor
				FROM transcript_content_repairs WHERE repair_id=$1`, opts.RepairID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			count := 0
			for rows.Next() {
				var phase, class string
				var priorRev, resultingRev int64
				var priorHash, resultingHash *string
				var priorSize, resultingSize *int64
				var actor uuid.UUID
				if err := rows.Scan(&phase, &class, &priorRev, &resultingRev, &priorHash, &priorSize, &resultingHash, &resultingSize, &actor); err != nil {
					t.Fatal(err)
				}
				// A malformed or absent pair is never an identity, so the ledger
				// records NULL/NULL for every case in this fixture's rows.
				if c.Hash == "valid" && c.Size != nil {
					if priorHash == nil || *priorHash != canonical {
						t.Fatalf("ledger prior hash = %v, want the valid hash", priorHash)
					}
				} else if priorHash != nil || priorSize != nil {
					t.Fatalf("ledger prior pair = %v/%v, want NULL/NULL", priorHash, priorSize)
				}
				if (resultingHash == nil) != (priorHash == nil) || (resultingSize == nil) != (priorSize == nil) {
					t.Fatalf("ledger resulting pair %v/%v differs from prior %v/%v", resultingHash, resultingSize, priorHash, priorSize)
				}
				if phase != c.Phase || class != c.FailureClass || priorRev != 1 || resultingRev != 1 || actor != opts.ActorID {
					t.Fatalf("ledger row phase=%s class=%q revisions=%d/%d actor=%s", phase, class, priorRev, resultingRev, actor)
				}
				count++
			}
			if count != 1 {
				t.Fatalf("want exactly one ledger row, got %d", count)
			}
		})
	}
}

// assertIdentityColumns checks the raw identity columns and that the content
// revision never moved.
func assertIdentityColumns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, wantHash *string, wantSize *int64) {
	t.Helper()
	var hash *string
	var size, segmentedSize *int64
	var segmentedHash *string
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT content_hash, blob_size_bytes, segmented_identity_hash, segmented_identity_size, content_revision FROM transcripts WHERE id=$1`, id).
		Scan(&hash, &size, &segmentedHash, &segmentedSize, &revision); err != nil {
		t.Fatal(err)
	}
	if !equalPtr(hash, wantHash) || !equalPtr(size, wantSize) || segmentedHash != nil || segmentedSize != nil || revision != 1 {
		t.Fatalf("row %s identity = %v/%v segmented %v/%v revision %d, want %v/%v with no segmented identity at revision 1",
			id, deref(hash), deref(size), segmentedHash, segmentedSize, revision, deref(wantHash), deref(wantSize))
	}
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
