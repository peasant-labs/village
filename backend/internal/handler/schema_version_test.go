package handler

// Village MUST advertise the push-contract
// negotiation window [Min, Current] on GET /api/v1/schema/version. If it leaves
// these zero-value, peasant B2's classifyContract sees an unadvertised window
// and fails open, killing negotiation end to end (IP1). These tests assert the
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
