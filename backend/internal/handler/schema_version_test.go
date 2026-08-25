package handler

// Village MUST advertise the push-contract
// negotiation window [Min, Current] on GET /api/v1/schema/version. If it leaves
// these zero-value, Peasant's contract classifier sees an unadvertised window
// and fails open, killing negotiation end to end. These tests assert the
// window is advertised and well-formed.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/peasant-labs/schema"
)

func TestGetSchemaVersion_AdvertisesPushWindow(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil)
	w := httptest.NewRecorder()

	h.GetSchemaVersion(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var resp schema.SchemaVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode SchemaVersionResponse: %v", err)
	}

	// The window must be advertised (non-empty) — the bug was both fields being
	// zero-value, which makes the CLI fail open.
	if resp.PushContractVersion == "" {
		t.Error("pushContractVersion is empty — the CLI negotiation window is unadvertised (fails open)")
	}
	if resp.MinPushContractVersion == "" {
		t.Error("minPushContractVersion is empty — the CLI negotiation window is unadvertised (fails open)")
	}
	// Current must equal the village's migrate-on-read target contract.
	if resp.PushContractVersion != currentContractVersion {
		t.Errorf("pushContractVersion: got %q, want %q", resp.PushContractVersion, currentContractVersion)
	}
	// Min must not exceed Current (a valid [Min, Current] window).
	if resp.MinPushContractVersion > resp.PushContractVersion {
		t.Errorf("invalid window: Min %q > Current %q", resp.MinPushContractVersion, resp.PushContractVersion)
	}
}

// Wire-shape assertion: the JSON the CLI parses carries the two camelCase keys
// the negotiation preflight reads.
func TestGetSchemaVersion_WireKeysPresent(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil)
	w := httptest.NewRecorder()

	h.GetSchemaVersion(w, r)

	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"pushContractVersion", "minPushContractVersion"} {
		v, ok := raw[key]
		if !ok {
			t.Errorf("response missing %q key", key)
			continue
		}
		if s, _ := v.(string); s == "" {
			t.Errorf("%q is empty on the wire", key)
		}
	}
}

// TestGetSchemaVersion_PushFloorPinnedAtAdvertisedValue pins the ADVERTISED push
// floor at the exact version Village serves today.
//
// The floor decides which CLIs may publish at all. Raising it refuses every older
// client outright, so it may only ever move as a deliberate, separately-argued
// decision — never as a side effect of some other change. The tests above prove
// the window is advertised and that a below-floor client is turned away; neither
// notices if the floor itself moves. This one does.
//
// It was written while adding the project-identity guard, where raising the floor
// LOOKED like a way to guarantee that every accepted payload carries a project
// hash. It is not: no published push-contract version guarantees a project object,
// so a higher floor would have refused older clients for an unrelated reason and
// closed nothing. The guard is enforced per payload instead, and the floor stays
// where it is.
func TestGetSchemaVersion_PushFloorPinnedAtAdvertisedValue(t *testing.T) {
	const pinnedPushFloor schema.PushContractVersion = "0.1.0"

	if minPushContractVersion != pinnedPushFloor {
		t.Fatalf("the push-acceptance floor is %q, pinned at %q. Moving the floor refuses every client below it, so it "+
			"is a deliberate compatibility decision: argue it, then update this pin in the same change.",
			minPushContractVersion, pinnedPushFloor)
	}

	h := newTestHandler(&mockQuerier{}, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil)
	w := httptest.NewRecorder()
	h.GetSchemaVersion(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var resp schema.SchemaVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode SchemaVersionResponse: %v", err)
	}
	if resp.MinPushContractVersion != pinnedPushFloor {
		t.Fatalf("advertised minPushContractVersion = %q, want the pinned floor %q", resp.MinPushContractVersion, pinnedPushFloor)
	}
}
