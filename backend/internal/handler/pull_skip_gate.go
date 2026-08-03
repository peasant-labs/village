package handler

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// PullSkipGate answers, per transcript the client already holds, whether the
// authenticated served plaintext and the client's OWN annotations are still current, so the client
// can skip re-downloading what has not diverged.
//
// It is keyed on the transcript id (the primary key); content_hash is only a
// compare-VALUE, never an identity or uniqueness key. Currency is answered ONLY
// for ids the caller may pull: a non-pullable (or unknown, or malformed) id is
// withheld by OMISSION from the response, never echoed with a denial or unknown
// marker, so the endpoint is not an existence or currency oracle over ids the
// caller cannot pull (the 404-not-403 anti-enumeration spirit for a batch probe).
// Annotation currency is owner-scoped: it reflects only the requester's own
// annotations, so another owner's annotations can never move the answer.
//
// POST /api/v1/pull/transcripts/skip-gate (AuthRequired)
func (h *Handler) PullSkipGate(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	var req schema.PullSkipGateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid skip-gate request body")
		return
	}

	// Index the client's held state by canonical transcript id, and collect the
	// ids to resolve. A malformed id can never be pullable, so it is dropped here
	// (withheld by omission); a duplicate id keeps its first item.
	clientByID := make(map[string]schema.PullSkipGateItem, len(req.Items))
	ids := make([]pgtype.UUID, 0, len(req.Items))
	for _, item := range req.Items {
		id, err := uuid.Parse(string(item.TranscriptID))
		if err != nil {
			continue
		}
		key := id.String()
		if _, seen := clientByID[key]; seen {
			continue
		}
		clientByID[key] = item
		ids = append(ids, toPgUUID(id))
	}

	// Pull-scoped: only the ids the caller may PULL come back; every other
	// requested id is absent, so its currency is withheld by omission.
	pullable, err := h.queries.ListPullableTranscriptsByIDs(r.Context(), sqlc.ListPullableTranscriptsByIDsParams{
		UserID:        user.PgID(),
		TranscriptIds: ids,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve pullable transcripts")
		return
	}

	// Owner-scoped annotation hashes for the pullable transcript UUIDs. The UUID,
	// not an owner-local local_id, is the discovery boundary for every target arm.
	serverAnnByTranscriptID, err := h.ownerAnnotationSetsByTranscriptID(r, user.PgID(), pullable)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve annotation currency")
		return
	}

	results := make([]schema.PullSkipGateResult, 0, len(pullable))
	for _, p := range pullable {
		item := clientByID[uuid.UUID(p.ID.Bytes).String()]
		// contentCurrent: the server holds a hash AND it equals the client's held
		// hash. A NULL (legacy / un-backfilled) server hash is never current.
		contentCurrent := p.ContentHash.Valid && p.ContentHash.String == item.ContentHash
		annotationsCurrent := hashSetsEqual(item.AnnotationHashes, serverAnnByTranscriptID[uuid.UUID(p.ID.Bytes).String()])
		results = append(results, schema.PullSkipGateResult{
			TranscriptID:       wireTranscriptID(p.ID),
			ContentCurrent:     contentCurrent,
			AnnotationsCurrent: annotationsCurrent,
		})
	}

	writeJSON(w, http.StatusOK, schema.NewPullSkipGateResponse(results))
}

// ownerAnnotationSetsByTranscriptID returns, per pullable transcript UUID, the
// set of the requester's OWN annotation content-hashes linking to it. Owner and
// transcript UUID remain in the query predicate so a same-local-ID row never
// crosses an ownership boundary.
func (h *Handler) ownerAnnotationSetsByTranscriptID(r *http.Request, owner pgtype.UUID, pullable []sqlc.ListPullableTranscriptsByIDsRow) (map[string]map[string]struct{}, error) {
	byTranscriptID := map[string]map[string]struct{}{}
	if len(pullable) == 0 {
		return byTranscriptID, nil
	}

	transcriptIDs := make([]pgtype.UUID, 0, len(pullable))
	transcriptIDSet := make(map[string]struct{}, len(pullable))
	for _, p := range pullable {
		id := uuid.UUID(p.ID.Bytes).String()
		if _, seen := transcriptIDSet[id]; seen {
			continue
		}
		transcriptIDSet[id] = struct{}{}
		transcriptIDs = append(transcriptIDs, p.ID)
	}

	rows, err := h.queries.ListOwnerAnnotationHashesForTranscriptIDs(r.Context(), sqlc.ListOwnerAnnotationHashesForTranscriptIDsParams{
		OwnerID:       owner,
		TranscriptIds: transcriptIDs,
	})
	if err != nil {
		return nil, err
	}

	add := func(transcriptID, hash string) {
		set, ok := byTranscriptID[transcriptID]
		if !ok {
			set = map[string]struct{}{}
			byTranscriptID[transcriptID] = set
		}
		set[hash] = struct{}{}
	}
	for _, a := range rows {
		transcriptID := uuid.UUID(a.TranscriptID.Bytes).String()
		if _, ok := transcriptIDSet[transcriptID]; ok {
			add(transcriptID, a.ContentHash)
		}
	}
	return byTranscriptID, nil
}

// hashSetsEqual reports set-equality of the client's held annotation hashes and
// the server's owner-scoped set for one transcript. The client list is de-duped
// (a raw POST may not be canonical); a missing OR extra hash makes it false.
func hashSetsEqual(clientHashes []string, serverSet map[string]struct{}) bool {
	clientSet := make(map[string]struct{}, len(clientHashes))
	for _, hash := range clientHashes {
		clientSet[hash] = struct{}{}
	}
	if len(clientSet) != len(serverSet) {
		return false
	}
	for hash := range clientSet {
		if _, ok := serverSet[hash]; !ok {
			return false
		}
	}
	return true
}
