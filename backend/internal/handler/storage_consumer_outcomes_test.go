package handler

import (
	"context"
	_ "embed"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema/route"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/storage_consumer_outcomes.yaml
var storageConsumerOutcomesYAML []byte

type consumerOutcome string

const (
	consumerAbsent     consumerOutcome = "absent"
	consumerLookup     consumerOutcome = "lookup"
	consumerIntegrity  consumerOutcome = "integrity"
	consumerKey        consumerOutcome = "key"
	consumerMissingRow consumerOutcome = "missing-row"
)

var requiredStorageConsumerCases = []string{
	"consumer-reload-triggers-on-absent-and-uncertain",
	"consumer-reload-triggers-on-uncertain",
	"consumer-reload-not-on-integrity",
	"consumer-reload-not-on-key-loss",
	"consumer-pull-404-unchanged",
}

func TestStorageConsumerOutcomes(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name       string          `yaml:"name"`
			Outcome    consumerOutcome `yaml:"outcome"`
			WantReload bool            `yaml:"want_reload"`
			WantStatus int             `yaml:"want_status"`
		} `yaml:"cases"`
	}
	if err := yaml.Unmarshal(storageConsumerOutcomesYAML, &fixture); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("empty or repeated consumer case %q", c.Name)
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			if c.Outcome == consumerMissingRow {
				id := uuid.New()
				q := &mockQuerier{getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) {
					return sqlc.Transcript{}, pgx.ErrNoRows
				}}
				h := newTestHandler(q, &fixedTranscriptBlobStore{})
				path, err := route.VillageGetPullTranscriptContentPath(id.String())
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(http.MethodGet, path, nil)
				r = withChiURLParam(r, "id", id.String())
				r = r.WithContext(withUserID(r.Context(), uuid.New()))
				w := httptest.NewRecorder()
				h.GetPullTranscriptContent(w, r)
				if w.Code != c.WantStatus {
					t.Fatalf("missing row returned %d, want %d", w.Code, c.WantStatus)
				}
				return
			}
			var failure error
			switch c.Outcome {
			case consumerAbsent:
				failure = storage.ErrObjectAbsentProvider
			case consumerLookup:
				failure = storage.ErrObjectLookupUncertain
			case consumerIntegrity:
				failure = storage.ErrObjectIntegrityUncertain
			case consumerKey:
				failure = storage.ErrKeyPermanentlyUnavailable
			default:
				t.Fatalf("unknown outcome %q", c.Outcome)
			}
			id := uuid.New()
			row := pullTestTranscript(id, uuid.New(), dbVisibilityPrivate)
			row.ContentHash = pgtype.Text{}
			reloads := 0
			q := &mockQuerier{getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) {
				reloads++
				return row, nil
			}}
			h := newTestHandler(q, &fixedTranscriptBlobStore{downloadErr: failure})
			_, err := h.readEncryptedTranscript(context.Background(), row, "", func(sqlc.Transcript) bool { return true })
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v, want wrapped %v", err, failure)
			}
			if (reloads == 1) != c.WantReload {
				t.Fatalf("reloads = %d, want reload %v", reloads, c.WantReload)
			}
		})
	}
	for _, name := range requiredStorageConsumerCases {
		if !seen[name] {
			t.Fatalf("storage consumer fixture omits %q", name)
		}
	}
}
