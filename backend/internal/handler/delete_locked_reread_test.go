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
)

//go:embed testdata/delete-locked-reread.yaml
var deleteLockedRereadYAML []byte

type deleteLockedRereadCase struct {
	Name        string `yaml:"name"`
	RereadError string `yaml:"reread_error"`
	Status      int    `yaml:"status"`
}

var requiredDeleteLockedRereadCases = []string{
	"locked-reread-no-rows-is-not-found",
	"locked-reread-database-failure-is-a-server-error",
}

func TestDeleteLockedRereadErrorMapping(t *testing.T) {
	cases, err := decodeFixtureRows[deleteLockedRereadCase](deleteLockedRereadYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{}
	for _, name := range requiredDeleteLockedRereadCases {
		required[name] = false
	}
	for _, c := range cases {
		if seen, known := required[c.Name]; !known || seen {
			t.Fatalf("unknown or duplicate delete locked re-read case %q", c.Name)
		}
		required[c.Name] = true
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("missing delete locked re-read case %q", name)
		}
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
			owner, id := uuid.New(), uuid.New()
			q := publicTranscriptQuerier(key)
			base := q.getTranscriptByID
			reads := 0
			q.getTranscriptByID = func(ctx context.Context, tid pgtype.UUID) (sqlc.Transcript, error) {
				reads++
				if reads == 2 {
					// The read under the publish lock.
					switch c.RereadError {
					case "no-rows":
						return sqlc.Transcript{}, pgx.ErrNoRows
					case "database":
						return sqlc.Transcript{}, errors.New("synthetic connection failure")
					default:
						t.Fatalf("fixture reread_error %q is unknown", c.RereadError)
					}
				}
				row, err := base(ctx, tid)
				row.OwnerID = pgUUIDFrom(owner)
				return row, err
			}
			store := &mockTranscriptBlobStore{}
			h := newTestHandler(q, store)
			path, err := route.VillageUpdateTranscriptPath(id.String()) // deletion shares the transcript's own path
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodDelete, path, nil)
			r = withChiURLParam(r, "id", id.String())
			r = r.WithContext(withUserID(r.Context(), owner))
			w := httptest.NewRecorder()
			h.DeleteTranscript(w, r)
			if reads != 2 {
				t.Fatalf("the handler read the transcript %d times, want the pre-read and the locked re-read", reads)
			}
			if w.Code != c.Status {
				t.Fatalf("status %d body %s, want %d", w.Code, w.Body.String(), c.Status)
			}
			if len(store.deletedKeys) != 0 {
				t.Fatalf("deleted %v after a failed locked re-read, want nothing deleted", store.deletedKeys)
			}
		})
	}
}
