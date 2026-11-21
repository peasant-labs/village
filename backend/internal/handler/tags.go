package handler

import (
	"net/http"
	"strconv"

	"github.com/peasant-labs/schema"
)

func (h *Handler) ListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.queries.ListTags(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list tags")
		return
	}
	out := make([]schema.VillageTagUsage, 0, len(tags))
	for _, tag := range tags {
		out = append(out, schema.VillageTagUsage{ID: villageUUID(tag.ID), Name: tag.Name, UsageCount: tag.UsageCount})
	}
	writeJSON(w, http.StatusOK, out)
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
	out := make([]schema.VillageTagUsage, 0, len(tags))
	for _, tag := range tags {
		out = append(out, schema.VillageTagUsage{ID: villageUUID(tag.ID), Name: tag.Name, UsageCount: tag.UsageCount})
	}
	writeJSON(w, http.StatusOK, out)
}
