package handler

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/predecessor_classification.yaml
var predecessorClassificationFixture []byte

type predecessorFixtureState string
type predecessorFixtureFault string
type predecessorFixtureOutcome string

const (
	predecessorFixturePending   predecessorFixtureState = "pending"
	predecessorFixtureKnown     predecessorFixtureState = "known"
	predecessorFixtureMalformed predecessorFixtureState = "malformed"

	predecessorFixtureAbsent       predecessorFixtureFault = "absent"
	predecessorFixturePermanentKey predecessorFixtureFault = "permanent-key"
	predecessorFixtureIntegrity    predecessorFixtureFault = "integrity"
	predecessorFixtureUntyped      predecessorFixtureFault = "untyped"
	predecessorFixtureCanceled     predecessorFixtureFault = "canceled"

	predecessorFixtureBaseline        predecessorFixtureOutcome = "baseline"
	predecessorFixtureMoved           predecessorFixtureOutcome = "moved"
	predecessorFixtureUncertain       predecessorFixtureOutcome = "uncertain"
	predecessorFixtureCanceledOutcome predecessorFixtureOutcome = "canceled"
)

type predecessorClassificationCase struct {
	Name    string                    `yaml:"name"`
	State   predecessorFixtureState   `yaml:"state"`
	Fault   predecessorFixtureFault   `yaml:"fault"`
	Outcome predecessorFixtureOutcome `yaml:"outcome"`
}

var requiredPredecessorClassificationCases = []string{
	"pending-provider-absence-admits-baseline",
	"pending-terminal-key-loss-admits-baseline",
	"established-provider-absence-refuses",
	"established-terminal-key-loss-refuses",
	"malformed-provider-absence-is-uncertain",
	"pending-integrity-failure-is-uncertain",
	"pending-provider-failure-is-uncertain",
	"canceled-parent-is-not-a-baseline",
}

func TestPredecessorClassification(t *testing.T) {
	var cases []predecessorClassificationCase
	decoder := yaml.NewDecoder(bytes.NewReader(predecessorClassificationFixture))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("unexpected trailing fixture document: %v", err)
	}
	seen := make(map[string]bool)
	for _, c := range cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("missing or duplicate predecessor fixture name %q", c.Name)
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			row := sqlc.Transcript{
				ID:                  pgUUIDFrom(uuid.New()),
				ContentRevision:     1,
				BlobKey:             "transcripts/10000000-0000-4000-8000-000000000001.bin",
				WrappedDataKey:      []byte("test-wrapped-key"),
				EncryptionAlgorithm: string(storage.EncryptionAES256GCMRandomNonceV1),
				KeyVersion:          1,
			}
			switch c.State {
			case predecessorFixturePending:
			case predecessorFixtureKnown:
				row.ContentHash = pgtype.Text{String: schema.ComputeTranscriptHash([]byte("old")), Valid: true}
				row.BlobSizeBytes = pgtype.Int8{Int64: 3, Valid: true}
			case predecessorFixtureMalformed:
				row.ContentHash = pgtype.Text{String: "malformed", Valid: true}
			default:
				t.Fatalf("unhandled state %q", c.State)
			}
			var failure error
			ctx := context.Background()
			switch c.Fault {
			case predecessorFixtureAbsent:
				failure = storage.ErrObjectAbsentProvider
			case predecessorFixturePermanentKey:
				failure = storage.ErrKeyPermanentlyUnavailable
			case predecessorFixtureIntegrity:
				failure = storage.ErrObjectIntegrityUncertain
			case predecessorFixtureUntyped:
				failure = errors.New("synthetic unavailable")
			case predecessorFixtureCanceled:
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			default:
				t.Fatalf("unhandled fault %q", c.Fault)
			}
			q := &mockQuerier{getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) {
				return row, nil
			}}
			h := newTestHandler(q, &fixedTranscriptBlobStore{downloadErr: failure})
			candidate, err := storage.NewContentIdentity(schema.ComputeTranscriptHash([]byte("new")), 3)
			if err != nil {
				t.Fatal(err)
			}
			predecessor, err := h.classifyPredecessor(ctx, q, row, candidate)
			switch c.Outcome {
			case predecessorFixtureBaseline:
				if err != nil || !predecessor.baseline {
					t.Fatalf("classification baseline=%t error=%v", predecessor.baseline, err)
				}
				if _, err := h.lockedPredecessor(ctx, predecessor, row, candidate); err != nil {
					t.Fatalf("locked definite-absence check failed: %v", err)
				}
			case predecessorFixtureMoved:
				if !errors.Is(err, errPreImageMoved) {
					t.Fatalf("expected generation movement, got %v", err)
				}
			case predecessorFixtureUncertain:
				if !errors.Is(err, errUncertainPredecessor) {
					t.Fatalf("expected uncertainty, got %v", err)
				}
			case predecessorFixtureCanceledOutcome:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation, got %v", err)
				}
			default:
				t.Fatalf("unhandled outcome %q", c.Outcome)
			}
		})
	}
	for _, name := range requiredPredecessorClassificationCases {
		if !seen[name] {
			t.Fatalf("required predecessor fixture missing %q", name)
		}
	}
}
