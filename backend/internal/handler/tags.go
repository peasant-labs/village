package handler

import (
	"net/http"
	"strconv"
)

func (h *Handler) ListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.queries.ListTags(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list tags")
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (h *Handler) PopularTags(w http.ResponseWriter, r *http.Request) {
	limit := int32(10)
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = int32(l)
	}
	tags, err := h.queries.ListPopularTags(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list popular tags")
		return
	}
	writeJSON(w, http.StatusOK, tags)
}
