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
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/replace.yaml
var replaceFixtureYAML []byte

type replaceCase struct {
	Name             string `yaml:"name"`
	Stored           string `yaml:"stored"`
	Descriptor       string `yaml:"descriptor"`
	ExpectedRevision int64  `yaml:"expected_revision"`
	Read             string `yaml:"read"`
	ExpectReads      int    `yaml:"expect_reads"`
	Phase            string `yaml:"phase"`
	FailureClass     string `yaml:"failure_class"`
	Replaced         bool   `yaml:"replaced"`
}

func TestReplaceThirdValueRealPostgres(t *testing.T) {
	var fixture struct {
		Cases []replaceCase `yaml:"cases"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(replaceFixtureYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"third-value-advances-revision-and-installs-it":       false,
		"third-value-replaces-a-segmented-identity":           false,
		"object-authenticating-to-stored-identity-is-refused": false,
		"wrong-expected-revision-is-refused":                  false,
		"absent-object-is-refused-as-uncertain":               false,
		"key-failure-is-refused-as-uncertain":                 false,
		"undecodable-descriptor-is-refused-as-uncertain":      false,
		"identity-free-row-is-refused":                        false,
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if seen[c.Name] {
			t.Fatalf("duplicate replace case %q", c.Name)
		}
		seen[c.Name] = true
		if _, isRequired := required[c.Name]; isRequired {
			required[c.Name] = true
		}
	}
	for name, present := range required {
		if !present {
			t.Fatalf("missing replace fixture case %q", name)
		}
	}

	ctx := context.Background()
	pool := scratchDatabase(t)
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	canonical := func(s string) string { return schema.ComputeTranscriptHash([]byte(s)) }
	storedHash, storedSize := canonical("fixture"), int64(7)
	stored, err := storage.NewContentIdentity(storedHash, storedSize)
	if err != nil {
		t.Fatal(err)
	}
	third, err := storage.NewContentIdentity(canonical("third value"), 11)
	if err != nil {
		t.Fatal(err)
	}
	thirdHash, thirdSize := string(third.Hash()), third.PlaintextSize()
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
			prior := seedRow{blobKey: blobKey}
			malformed := malformedHash
			switch c.Stored {
			case "primary":
				prior.hash, prior.size = &storedHash, &storedSize
			case "segmented":
				prior.segHash, prior.segSize = &storedHash, &storedSize
			case "malformed":
				prior.hash, prior.size = &malformed, &storedSize
			case "none":
			default:
				t.Fatalf("fixture stored %q is unknown", c.Stored)
			}
			id := seedTranscriptRow(t, ctx, pool, prior)
			sibling := seedTranscriptRow(t, ctx, pool, seedRow{blobKey: "transcripts/" + uuid.NewString() + ".bin", hash: &storedHash, size: &storedSize})

			reads := 0
			store := scriptedBlobStore{reads: &reads}
			switch c.Read {
			case "third":
				store.identity = third
			case "stored":
				store.identity = stored
			case "absent-provider":
				store.err = storage.ErrObjectAbsentProvider
			case "lookup-uncertain":
				store.err = storage.ErrObjectLookupUncertain
			case "key-failure":
				store.err = errors.New("key custody unavailable")
			default:
				t.Fatalf("fixture read %q is unknown", c.Read)
			}
			expected := c.ExpectedRevision
			if expected == 0 {
				expected = 1
			}
			opts := Options{
				RepairID: uuid.New(), TranscriptID: id, Action: ReplaceThirdValue,
				ActorID: uuid.New(), AuthorizationRef: "ticket-3", Reason: "third value", ExpectedRevision: expected,
			}
			outcome, err := ReplaceThird(ctx, Deps{Pool: pool, Queries: sqlc.New(pool), Blobs: store, Now: time.Now}, opts)
			if err != nil {
				t.Fatal(err)
			}
			wantRevision := int64(1)
			if c.Replaced {
				wantRevision = 2
			}
			if outcome.Phase.String() != c.Phase || outcome.FailureClass.String() != c.FailureClass || outcome.ContentRevision != wantRevision {
				t.Fatalf("outcome phase=%s class=%q revision=%d, want %s %q %d", outcome.Phase, outcome.FailureClass, outcome.ContentRevision, c.Phase, c.FailureClass, wantRevision)
			}
			if reads != c.ExpectReads {
				t.Fatalf("object reads = %d, want %d", reads, c.ExpectReads)
			}

			// The identity slots and the revision after the run.
			var hash, segmentedHash *string
			var size, segmentedSize *int64
			var revision int64
			if err := pool.QueryRow(ctx, `SELECT content_hash, blob_size_bytes, segmented_identity_hash, segmented_identity_size, content_revision FROM transcripts WHERE id=$1`, id).
				Scan(&hash, &size, &segmentedHash, &segmentedSize, &revision); err != nil {
				t.Fatal(err)
			}
			if c.Replaced {
				if !equalPtr(hash, &thirdHash) || !equalPtr(size, &thirdSize) || segmentedHash != nil || segmentedSize != nil || revision != 2 {
					t.Fatalf("row after replacement = %v/%v segmented %v/%v revision %d, want the third value only at revision 2",
						deref(hash), deref(size), deref(segmentedHash), deref(segmentedSize), revision)
				}
			} else if !equalPtr(hash, prior.hash) || !equalPtr(size, prior.size) || !equalPtr(segmentedHash, prior.segHash) || !equalPtr(segmentedSize, prior.segSize) || revision != 1 {
				t.Fatalf("refused row changed: %v/%v segmented %v/%v revision %d", deref(hash), deref(size), deref(segmentedHash), deref(segmentedSize), revision)
			}
			// The sibling shares the stored identity and must never be matched.
			var siblingRevision int64
			var siblingHash *string
			if err := pool.QueryRow(ctx, `SELECT content_revision, content_hash FROM transcripts WHERE id=$1`, sibling).Scan(&siblingRevision, &siblingHash); err != nil {
				t.Fatal(err)
			}
			if siblingRevision != 1 || !equalPtr(siblingHash, &storedHash) {
				t.Fatalf("sibling row changed: revision %d hash %v", siblingRevision, deref(siblingHash))
			}

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
				if phase != c.Phase || class != c.FailureClass || priorRev != 1 || resultingRev != wantRevision || actor != opts.ActorID {
					t.Fatalf("ledger row phase=%s class=%q revisions=%d/%d actor=%s", phase, class, priorRev, resultingRev, actor)
				}
				// A usable stored identity is recorded as the prior pair;
				// anything else is not an identity and records NULL/NULL.
				if c.Stored == "primary" || c.Stored == "segmented" {
					if priorHash == nil || *priorHash != storedHash || priorSize == nil || *priorSize != storedSize {
						t.Fatalf("ledger prior pair = %v/%v, want the stored identity", deref(priorHash), deref(priorSize))
					}
				} else if priorHash != nil || priorSize != nil {
					t.Fatalf("ledger prior pair = %v/%v, want NULL/NULL", deref(priorHash), deref(priorSize))
				}
				wantResultingHash, wantResultingSize := priorHash, priorSize
				if c.Replaced {
					wantResultingHash, wantResultingSize = &thirdHash, &thirdSize
				}
				if !equalPtr(resultingHash, wantResultingHash) || !equalPtr(resultingSize, wantResultingSize) {
					t.Fatalf("ledger resulting pair = %v/%v, want %v/%v", deref(resultingHash), deref(resultingSize), deref(wantResultingHash), deref(wantResultingSize))
				}
				count++
			}
			if count != 1 {
				t.Fatalf("want exactly one ledger row, got %d", count)
			}
		})
	}
}
