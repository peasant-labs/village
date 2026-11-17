//go:build integration

package handler

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema/route"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/publish_revision_lifecycle.yaml
var publishRevisionLifecycleYAML []byte

type publishRevisionLifecycleCase struct {
	Name     string   `yaml:"name"`
	Scenario string   `yaml:"scenario"`
	Why      string   `yaml:"why"`
	Patches  []string `yaml:"patches"`
}

// requiredPublishRevisionLifecycleCases is the exact set of cases the fixture
// holds: an unlisted case and a deleted case both fail.
var requiredPublishRevisionLifecycleCases = []string{
	"metadata-visibility-license-keeps",
	"delete-recreate-new-uuid-one",
}

func TestPublishRevisionAcrossOtherWriters(t *testing.T) {
	cases, err := decodeFixtureRows[publishRevisionLifecycleCase](publishRevisionLifecycleYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{}
	for _, name := range requiredPublishRevisionLifecycleCases {
		required[name] = false
	}
	for _, c := range cases {
		if seen, known := required[c.Name]; !known || seen || c.Scenario == "" || c.Why == "" {
			t.Fatalf("unknown, duplicate or incomplete publish revision lifecycle case %+v", c)
		}
		required[c.Name] = true
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("required publish revision lifecycle case %q is missing", name)
		}
	}

	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)
	for ordinal, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			w := newOrderingWorld(t, pool, objects, 20000+ordinal, attachmentPublicationContent())
			replaced := w.request(republishAudienceRevisedContent(), "replacement")(w.ctx)
			if replaced.Code != http.StatusOK {
				t.Fatalf("replacement status=%d body=%s", replaced.Code, replaced.Body.String())
			}
			before := w.row()
			if before.ContentRevision != 2 {
				t.Fatalf("a changed republish left revision %d, want 2", before.ContentRevision)
			}
			switch c.Scenario {
			case "owner-patch":
				for _, body := range c.Patches {
					response := mountedOwnerPatchBody(w.handler, w.owner, w.id, body)
					if response.Code != http.StatusOK {
						t.Fatalf("owner PATCH %s status=%d body=%s", body, response.Code, response.Body.String())
					}
					after := w.row()
					if after.ContentRevision != before.ContentRevision || after.ContentHash != before.ContentHash || after.BlobSizeBytes != before.BlobSizeBytes ||
						after.SegmentedIdentityHash != before.SegmentedIdentityHash || after.SegmentedIdentitySize != before.SegmentedIdentitySize ||
						after.BlobKey != before.BlobKey || after.PublishMetadataKey != before.PublishMetadataKey {
						t.Fatalf("owner PATCH %s changed the content revision, identity or objects: before %+v after %+v", body, before, after)
					}
				}
			case "delete-recreate":
				deleted := w.deleteTranscript()
				if deleted.Code != http.StatusOK {
					t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
				}
				recreated := w.request(republishAudienceRevisedContent(), "recreated")(w.ctx)
				if recreated.Code != http.StatusCreated {
					t.Fatalf("recreate status=%d body=%s", recreated.Code, recreated.Body.String())
				}
				newID, err := sqlc.New(w.pool).GetTranscriptIDByOwnerAndLocalID(w.ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: toPgUUID(w.owner.ID), LocalID: w.localID})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { purgeAuditRows(t, context.Background(), w.pool, []pgtype.UUID{newID}) })
				if newID == w.id {
					t.Fatal("a recreated session reused the deleted transcript's ID")
				}
				if _, err := sqlc.New(w.pool).GetTranscriptByID(w.ctx, w.id); err == nil {
					t.Fatal("the deleted transcript still exists")
				}
				row, err := sqlc.New(w.pool).GetTranscriptByID(w.ctx, newID)
				if err != nil {
					t.Fatal(err)
				}
				if row.ContentRevision != 1 {
					t.Fatalf("a recreated session starts at revision %d, want 1", row.ContentRevision)
				}
				t.Cleanup(func() {
					if descriptor, err := descriptorFromTranscript(row); err == nil {
						_ = w.store.Delete(context.Background(), descriptor)
					}
					if metadata, err := metadataObjectFromTranscript(row); err == nil && metadata != nil {
						_ = w.store.DeleteMetadata(context.Background(), *metadata)
					}
				})
			default:
				t.Fatalf("fixture scenario %q is unknown", c.Scenario)
			}
		})
	}
}

// deleteTranscript is the owner's mounted delete.
func (w *orderingWorld) deleteTranscript() *httptest.ResponseRecorder {
	w.t.Helper()
	id := uuidFromPg(w.id).String()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(w.ctx, chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, UserContextKey, w.owner)
	path, err := route.VillageUpdateTranscriptPath(id) // deletion shares the transcript's own path
	if err != nil {
		w.t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodDelete, path, nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	w.handler.DeleteTranscript(recorder, r)
	return recorder
}
