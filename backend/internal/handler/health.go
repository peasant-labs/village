package handler

import (
	"context"
	"net/http"
	"regexp"
	"time"

	"github.com/peasant-labs/schema"
)

// buildRevision is set by the linker, not by the running process's environment.
// Binaries built without a revision omit it from the health response.
var buildRevision string

var shortBuildRevision = regexp.MustCompile(`^[0-9a-f]{7,12}$`)

type healthTrait string

const healthPublishRevision healthTrait = "publish-revision-n"
const healthSegmentedReads healthTrait = "segmented-reads-v2"

// SetSegmentedReadsMounted configures the probe during router composition,
// before the handler is made available to concurrent requests.
func (h *Handler) SetSegmentedReadsMounted(mounted bool) {
	h.segmentedReadsMounted = mounted
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	probe := schema.VillageHealthResponse{
		Status: "ok",
		Serves: []string{string(healthPublishRevision)},
	}
	if h.segmentedReadsMounted {
		probe.Serves = append(probe.Serves, string(healthSegmentedReads))
	}
	if shortBuildRevision.MatchString(buildRevision) {
		probe.BuildRevision = buildRevision
	}
	if h.pool != nil {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		var applied *int64
		if err := h.pool.QueryRow(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&applied); err == nil {
			probe.Migrations = applied
		}
	}
	// The load balancer needs liveness even when the registry is temporarily
	// unreadable. The deploy gate fails closed on the absent migrations member.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, probe)
}
