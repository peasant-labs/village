//go:build integration

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/peasant-labs/village/backend/internal/config"
)

// TestPublishHarnessAcceptanceRealPostgres verifies what the mounted handler
// commits, not just what it passes to its database dependency. Blob storage is
// the existing in-memory fake; database transactions, constraints and triggers
// are real. Each case has its own owner and the same request identity.
func TestPublishHarnessAcceptanceRealPostgres(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	t.Cleanup(pool.Close)
	fixture := loadPublishHarnessCases(t)
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			id := uuid.New()
			owner := pullInsertUser(t, ctx, pool, 7_100_000_000+int64(id.ID()), "harness-publish-"+id.String()[:8])
			t.Cleanup(func() { cleanupOwners(t, ctx, pool, owner) })
			blobs := &mockTranscriptBlobStore{}
			h := New(&config.Config{FrontendURL: "https://example.test"}, pool, blobs)
			user := &AuthUser{ID: uuidFromPg(owner), Username: "fixture-owner"}
			w := httptest.NewRecorder()
			mountPublishHarnessHandler(h, user).ServeHTTP(w, publishHarnessRequest(t, fixture, c.Harness))
			assertPublishHarnessResponse(t, c, w)

			var storedHarness string
			err := pool.QueryRow(ctx, "SELECT model_provider FROM transcripts WHERE owner_id = $1", owner).Scan(&storedHarness)
			if c.Status != http.StatusCreated {
				if !errors.Is(err, pgx.ErrNoRows) || len(blobs.uploadedKeys) != 0 {
					t.Fatalf("refusal wrote a transcript or object: query=%v objects=%d", err, len(blobs.uploadedKeys))
				}
				return
			}
			if err != nil || storedHarness != c.Harness || len(blobs.uploadedKeys) != 1 {
				t.Fatalf("stored harness=%q want=%q query=%v objects=%d", storedHarness, c.Harness, err, len(blobs.uploadedKeys))
			}
		})
	}
}
