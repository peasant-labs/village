package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

var validAttestationTypes = map[string]bool{
	"used_in_training": true,
	"referenced":       true,
	"evaluated":        true,
	"deployed":         true,
}

// CreateAttestation creates a usage attestation for a transcript.
// POST /transcripts/{id}/attestations (AuthRequired)
// The DB FK constraint on (attester_id, org_login) -> user_github_orgs
// enforces that only verified org members can attest.
func (h *Handler) CreateAttestation(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}

	transcript, err := h.queries.GetTranscriptByID(r.Context(), toPgUUID(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	if !h.canViewTranscript(r.Context(), user, transcript) {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	var req struct {
		OrgLogin        string `json:"org_login"`
		AttestationType string `json:"attestation_type"`
		Note            string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	req.AttestationType = strings.TrimSpace(req.AttestationType)
	if !validAttestationTypes[req.AttestationType] {
		writeError(w, http.StatusBadRequest, "Invalid attestation type")
		return
	}
	if strings.TrimSpace(req.OrgLogin) == "" {
		writeError(w, http.StatusBadRequest, "org_login is required")
		return
	}

	attestation, err := h.queries.CreateAttestation(r.Context(), sqlc.CreateAttestationParams{
		TranscriptID:    toPgUUID(id),
		AttesterID:      user.PgID(),
		OrgLogin:        req.OrgLogin,
		AttestationType: req.AttestationType,
		Note:            toPgText(req.Note),
	})
	if err != nil {
		// FK constraint violation means user isn't a verified org member
		if strings.Contains(err.Error(), "foreign key") || strings.Contains(err.Error(), "violates") {
			writeError(w, http.StatusForbidden, "You must be a verified member of this org to attest")
			return
		}
		// Unique constraint violation means duplicate attestation
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			writeError(w, http.StatusConflict, "This attestation already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to create attestation")
		return
	}

	writeJSON(w, http.StatusCreated, attestation)
}

// DeleteAttestation removes an attestation (only the attester can delete their own).
// DELETE /transcripts/{id}/attestations/{attestationID} (AuthRequired)
func (h *Handler) DeleteAttestation(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	attestationID, err := uuid.Parse(chi.URLParam(r, "attestationID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid attestation ID")
		return
	}

	err = h.queries.DeleteAttestation(r.Context(), sqlc.DeleteAttestationParams{
		ID:         toPgUUID(attestationID),
		AttesterID: user.PgID(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete attestation")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ListTranscriptAttestations returns all attestations for a transcript.
// GET /transcripts/{id}/attestations (AuthOptional)
func (h *Handler) ListTranscriptAttestations(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}

	transcript, err := h.queries.GetTranscriptByID(r.Context(), toPgUUID(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	user := GetUser(r.Context())
	if !h.canViewTranscript(r.Context(), user, transcript) {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	attestations, err := h.queries.ListTranscriptAttestations(r.Context(), toPgUUID(id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch attestations")
		return
	}

	writeJSON(w, http.StatusOK, attestations)
}
