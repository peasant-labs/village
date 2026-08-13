package handler

import (
	"net/http"

	"github.com/peasant-labs/schema"
)

// minPushContractVersion is the village's PUSH-ACCEPTANCE floor: the oldest push
// contract version it will still accept on the publish path. A3 DISTINCT FLOORS:
// this is SEPARATE from displayMigrateFloor (how far back a stored blob can be
// normalized for rendering, which may reach further back) and from the Phase-A
// SQL backfill. As of 1e8tk the current contract is 0.1.1 (the required
// harness+model strictening — same wire SHAPE, a PATCH bump), and the floor
// stays 0.1.0, so the legacy push window remains [0.1.0, 0.1.1]. An explicit,
// flat opaque capability token tells enriched clients whether
// observed-model evidence is proven safe through Village's typed storage paths.
const minPushContractVersion schema.PushContractVersion = "0.1.0"

// pullContractVersion / minPullContractVersion advertise the PULL ENVELOPE
// negotiation window [Min, Current]. They mirror peasant's
// defaults.PullContractVersion / MinPullContractVersion ("0.1.0"). The pull
// window is DISTINCT from the push window above: a client may pull a contract it
// could not push, and vice versa, so they negotiate independently. Today both
// sides sit at the single 0.1.0 envelope, so the window is [0.1.0, 0.1.0].
const (
	pullContractVersion    schema.PushContractVersion = "0.1.0"
	minPullContractVersion schema.PushContractVersion = "0.1.0"
)

// GetSchemaVersion returns the annotation schema version and supported types,
// and advertises the push-contract negotiation WINDOW [Min, Current] so the CLI
// can preflight. Without these the CLI's classifyContract sees an
// unadvertised window and fails open — defeating negotiation end to end (IP1).
// GET /api/v1/schema/version (public)
func (h *Handler) GetSchemaVersion(w http.ResponseWriter, r *http.Request) {
	legacy := schema.SchemaVersionResponse{
		AnnotationSchemaVersion: "1.0",
		SupportedTargetKinds: []string{
			string(schema.TargetSession),
			string(schema.TargetEntry),
			string(schema.TargetAnnotation),
			string(schema.TargetProject),
		},
		SupportedTypeIDs: []string{
			"quality.session_outcome",
			"quality.session_approval",
			"quality.user_frustration",
			"metadata.session_scope",
		},
		// Push-contract accept window: Current = the contract the village prefers
		// (== currentContractVersion, the migrate-on-read target); Min = the
		// push-acceptance floor.
		PushContractVersion:    currentContractVersion,
		MinPushContractVersion: minPushContractVersion,
		// Pull-envelope negotiation window [Min, Current] — DISTINCT
		// from the push window; the CLI preflights pulls against this.
		PullContractVersion:    pullContractVersion,
		MinPullContractVersion: minPullContractVersion,
	}
	legacy.ContentCapabilities = advertisedContentCapabilitiesWithEvaluator(h.preservationProof())
	if err := schema.ValidateContentCapabilityAdvertisements(legacy.ContentCapabilities); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, legacy)
}
