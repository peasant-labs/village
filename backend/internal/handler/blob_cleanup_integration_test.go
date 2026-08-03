//go:build integration

package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

type cleanupFailureTranscriptBlobStore struct {
	*recordingTranscriptBlobStore
	afterWrite func()
	deleteErr  error
}

func (s *cleanupFailureTranscriptBlobStore) Write(ctx context.Context, id uuid.UUID, content []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	descriptor, identity, err := s.recordingTranscriptBlobStore.Write(ctx, id, content)
	if err == nil && s.afterWrite != nil {
		afterWrite := s.afterWrite
		s.afterWrite = nil
		afterWrite()
	}
	return descriptor, identity, err
}

func (s *cleanupFailureTranscriptBlobStore) Delete(ctx context.Context, descriptor storage.BlobDescriptor) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.recordingTranscriptBlobStore.Delete(ctx, descriptor)
}

func mountedCleanupPublish(t *testing.T, ctx context.Context, h *Handler, owner pgtype.UUID, localID, marker string) publishOutcome {
	t.Helper()
	publish, err := preparePublish(owner, "cleanup-evidence-owner", localID, nil, fmt.Sprintf(`[{"role":"user","content":%q}]`, marker))
	if err != nil {
		t.Fatalf("prepare mounted cleanup publish: %v", err)
	}
	return publish(ctx, h)
}

func cleanupFixtureTranscript(t *testing.T, ctx context.Context, queries *sqlc.Queries, owner pgtype.UUID, localID string) sqlc.Transcript {
	t.Helper()
	id, err := queries.GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: owner, LocalID: localID})
	if err != nil {
		t.Fatalf("load cleanup fixture transcript ID: %v", err)
	}
	row, err := queries.GetTranscriptByID(ctx, id)
	if err != nil {
		t.Fatalf("load cleanup fixture transcript: %v", err)
	}
	return row
}

func TestMountedBlobCleanupFailuresEmitReconciliationEvidence(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()

	for ordinal, tc := range loadCleanupFailureCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			operation, ok := cleanupOperationFromFixture(tc.Operation)
			if !ok {
				t.Fatalf("fixture operation=%q is unknown", tc.Operation)
			}

			ctx := context.Background()
			owner := associationFixtureUser(t, ctx, pool, 7000+ordinal, "cleanup-evidence")
			defer cleanupOwners(t, context.Background(), pool, owner)

			blobs := &cleanupFailureTranscriptBlobStore{recordingTranscriptBlobStore: newRecordingTranscriptBlobStore()}
			queries := sqlc.New(pool)
			h := &Handler{cfg: minimalConfig(), pool: pool, queries: queries, blobs: blobs}
			localID := uuid.NewString()
			var row sqlc.Transcript
			if operation != cleanupCreateCandidate {
				created := mountedCleanupPublish(t, ctx, h, owner, localID, "initial mounted cleanup fixture")
				if created.status != http.StatusCreated {
					t.Fatalf("initial mounted publish status=%d body=%q, want 201", created.status, created.body)
				}
				row = cleanupFixtureTranscript(t, ctx, queries, owner, localID)
				defer purgeAuditRows(t, context.Background(), pool, []pgtype.UUID{row.ID})
			}

			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			blobs.deleteErr = errors.New("fixture object store unavailable")

			switch operation {
			case cleanupCreateCandidate, cleanupRepublishCandidate:
				requestCtx, cancel := context.WithCancel(ctx)
				blobs.afterWrite = cancel
				outcome := mountedCleanupPublish(t, requestCtx, h, owner, localID, "canceled mounted cleanup fixture")
				cancel()
				if outcome.status != http.StatusInternalServerError {
					t.Fatalf("mounted rollback publish status=%d body=%q, want 500", outcome.status, outcome.body)
				}
			case cleanupRepublishSuperseded:
				outcome := mountedCleanupPublish(t, ctx, h, owner, localID, "committed mounted republish fixture")
				if outcome.status != http.StatusOK {
					t.Fatalf("mounted committed republish status=%d body=%q, want 200", outcome.status, outcome.body)
				}
			case cleanupRewriteCandidate:
				blobs.afterWrite = func() {
					execAsSystem(t, context.Background(), pool, "DELETE FROM transcripts WHERE id = $1", row.ID)
				}
				h.rewriteCanonicalTranscript(ctx, row, []byte(`{"kind":"candidate-race"}`))
			case cleanupRewriteSuperseded:
				h.rewriteCanonicalTranscript(ctx, row, []byte(`{"kind":"committed-rewrite"}`))
			case cleanupDeleteTarget:
				request := httptest.NewRequest(http.MethodDelete, "/api/v1/transcripts/"+uuidFromPg(row.ID).String(), nil)
				request = withChiURLParam(request, "id", uuidFromPg(row.ID).String())
				request = request.WithContext(withUserID(request.Context(), uuidFromPg(owner)))
				response := httptest.NewRecorder()
				h.DeleteTranscript(response, request)
				if response.Code != http.StatusInternalServerError {
					t.Fatalf("mounted delete status=%d body=%q, want 500", response.Code, response.Body.String())
				}
			default:
				t.Fatalf("fixture operation=%q has no mounted production path", tc.Operation)
			}

			logLine := logs.String()
			if strings.Count(logLine, "msg=transcript_blob_reconciliation_required") != 1 ||
				!strings.Contains(logLine, "operation="+tc.Operation) ||
				!strings.Contains(logLine, "completion="+tc.Completion) ||
				strings.Contains(logLine, "fixture object store unavailable") {
				t.Fatalf("mounted cleanup evidence does not identify the exact secret-safe path: %q", logLine)
			}
		})
	}
}
