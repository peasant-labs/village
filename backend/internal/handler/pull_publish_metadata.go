package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// publishMetadataNotFound is the one answer for every caller the route does
// not serve: no such transcript, no right to its metadata, an owner hidden
// from the caller, or no metadata stored. The bytes are the same in each case,
// so the answer reveals nothing about the transcript.
const publishMetadataNotFound = "Transcript publish metadata not found"

// publishMetadataUnverified is the answer when the stored metadata cannot be
// authenticated against its recorded hash and size.
const publishMetadataUnverified = "Stored publish metadata failed verification; nothing was served"

// canReadPublishMetadata reports whether the caller may read the stored
// publish metadata of t: its owner, or an owner of a collective it is
// submitted to with a pending or approved share. Being able to read the
// transcript itself (a member of a collective holding an approved share, a
// reader of a public transcript, the owner of a collective that rejected it)
// does not admit the metadata.
func (h *Handler) canReadPublishMetadata(ctx context.Context, user *AuthUser, t sqlc.Transcript) (bool, error) {
	if user == nil {
		return false, nil
	}
	caller := user.PgID()
	if t.OwnerID == caller {
		return true, nil
	}
	owners, err := h.queries.ListSubmittedCollectiveOwnersForTranscript(ctx, t.ID)
	if err != nil {
		return false, err
	}
	for _, owner := range owners {
		if owner == caller {
			return true, nil
		}
	}
	return false, nil
}

// GetPullTranscriptPublishMetadata serves the publish metadata Village stored
// for a transcript: the validated authoritative publish request, decrypted and
// authenticated against the row's recorded hash and size, byte for byte. It
// negotiates the pull contract like the other pull reads, sends no ETag, and
// answers every refusal after the session check with the same 404.
//
// A republish between the row read and the object read deletes the object the
// row named. As on the content route, a missing object reloads the row, runs
// the whole access decision again on it, and reads once more.
// GET /api/v1/pull/transcripts/{id}/publish-metadata (AuthRequired)
func (h *Handler) GetPullTranscriptPublishMetadata(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := GetUser(ctx)
	if _, ok := negotiatePullContract(w, r); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, publishMetadataNotFound)
		return
	}
	first, ok := h.authorizePublishMetadata(w, ctx, user, id)
	if !ok {
		return
	}
	body, err := h.blobs.ReadMetadata(ctx, id, first.descriptor, first.object)
	if errors.Is(err, storage.ErrObjectNotFound) {
		fresh, ok := h.authorizePublishMetadata(w, ctx, user, id)
		if !ok {
			return
		}
		if fresh.object.Key() == first.object.Key() {
			// The row still names the object that is gone: nothing replaced
			// it, so another read cannot succeed.
			publishMetadataMissingResponse(w, id)
			return
		}
		body, err = h.blobs.ReadMetadata(ctx, id, fresh.descriptor, fresh.object)
	}
	if errors.Is(err, storage.ErrObjectNotFound) {
		publishMetadataMissingResponse(w, id)
		return
	}
	if err != nil {
		// ReadMetadata authenticates the ciphertext and checks the plaintext
		// against the stored hash and size before it returns any byte.
		publishMetadataUnverifiedResponse(w, id, "read")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// storedPublishMetadata is what one authorized row says to read.
type storedPublishMetadata struct {
	descriptor storage.BlobDescriptor
	object     storage.MetadataObject
}

// authorizePublishMetadata reads the row and decides the request on it: the
// shared 404 for every refusal, a 500 for a failed lookup or malformed
// columns, otherwise the descriptor and metadata object to read. It writes
// the response itself whenever it returns false.
func (h *Handler) authorizePublishMetadata(w http.ResponseWriter, ctx context.Context, user *AuthUser, id uuid.UUID) (storedPublishMetadata, bool) {
	t, err := h.queries.GetTranscriptByID(ctx, toPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, publishMetadataNotFound)
		return storedPublishMetadata{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read the transcript")
		return storedPublishMetadata{}, false
	}
	allowed, err := h.canReadPublishMetadata(ctx, user, t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read the transcript")
		return storedPublishMetadata{}, false
	}
	if !allowed {
		writeError(w, http.StatusNotFound, publishMetadataNotFound)
		return storedPublishMetadata{}, false
	}
	// The metadata names the owner's repository, paths and commit authors, so
	// an owner the public-owner rule hides from this caller hides it too.
	owner, err := h.resolvePublicOwner(ctx, user, t.OwnerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve the transcript owner")
		return storedPublishMetadata{}, false
	}
	if !owner.IsProfile() {
		writeError(w, http.StatusNotFound, publishMetadataNotFound)
		return storedPublishMetadata{}, false
	}
	object, err := metadataObjectFromTranscript(t)
	if err != nil {
		publishMetadataUnverifiedResponse(w, id, "metadata_columns")
		return storedPublishMetadata{}, false
	}
	if object == nil {
		writeError(w, http.StatusNotFound, publishMetadataNotFound)
		return storedPublishMetadata{}, false
	}
	descriptor, err := descriptorFromTranscript(t)
	if err != nil {
		publishMetadataUnverifiedResponse(w, id, "descriptor")
		return storedPublishMetadata{}, false
	}
	return storedPublishMetadata{descriptor: descriptor, object: *object}, true
}

// publishMetadataUnverifiedResponse answers a metadata object that could not
// be authenticated. The log names only the transcript and the stage: never the
// bytes, the object key, or the store's error text.
func publishMetadataUnverifiedResponse(w http.ResponseWriter, id uuid.UUID, stage string) {
	slog.Error("publish metadata verification failed", "transcript_id", id.String(), "stage", stage)
	writeError(w, http.StatusInternalServerError, publishMetadataUnverified)
}

// publishMetadataMissingResponse answers a metadata object the row names but
// storage does not hold, after the one reload. That is a storage
// reconciliation matter, not a failed authentication, so it is a warning with
// its own stage; the caller gets the same 500 as any unserved object.
func publishMetadataMissingResponse(w http.ResponseWriter, id uuid.UUID) {
	slog.Warn("publish metadata object missing", "transcript_id", id.String(), "stage", "object_missing")
	writeError(w, http.StatusInternalServerError, publishMetadataUnverified)
}
