package handler

import (
	"net/http"

	"github.com/peasant-labs/schema"
)

// GetAnnotationManifest returns the SET of annotation content-hashes the village
// currently holds for the authenticated owner, plus an order-independent digest
// using the server-authoritative skip gate.
//
// GET /api/v1/annotations/manifest (AuthRequired)
//
// The push client fetches this once at run start and SKIPS any local annotation
// whose content-hash already appears here, pushing only the remainder. The
// manifest carries hashes only (no annotation content), so it is privacy-safe,
// and it is owner-scoped: a caller only ever sees their own hashes. This is an
// ADDITIVE endpoint — it does not alter the existing publish/annotation-push
// wire contract.
func (h *Handler) GetAnnotationManifest(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())

	hashes, err := h.queries.ListAnnotationContentHashesByOwner(r.Context(), user.PgID())
	if err != nil {
		// The client treats a manifest-fetch failure as fail-safe (push all), so
		// a 500 here is correct: it must never look like an empty manifest, which
		// would cause the client to skip everything.
		writeError(w, http.StatusInternalServerError, "Failed to load annotation manifest")
		return
	}

	// NewAnnotationManifestResponse normalizes (sort + dedup) and computes the
	// matching digest, so the wire payload is canonical regardless of row order.
	writeJSON(w, http.StatusOK, schema.NewAnnotationManifestResponse(hashes))
}
