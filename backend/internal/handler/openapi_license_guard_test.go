package handler

// License-contract 422-body pin. The village enforces publish bodies through the
// contract module's single byte-source (schema.ValidatePublishRequest), so served vs
// enforced drift is structurally impossible — no separate vendored-bytes guard is
// needed. What DOES need pinning is the cross-repo ERROR BODY: peasant's client
// couples on the exact enum-violation wording for an off-menu license, so this test
// holds that body verbatim against the module's SchemaLicense menu.

import (
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
)

// wantLicenseMenu is the verbatim SchemaLicense menu clause the enforce path must
// surface for an off-menu publish license. It is a stable cross-repo contract (the
// closed 3-value CC menu): peasant's client couples on this exact wording.
const wantLicenseMenu = `value must be one of "CC0-1.0", "CC-BY-4.0", "CC-BY-SA-4.0"`

// TestValidatePublish_BadLicense_ErrorBodyPinsMenu pins the 422 BODY for an off-menu
// publish license at the validator level: the enum-violation error must carry the
// verbatim license menu clause (every value, in declaration order) so the rejection
// is actionable and the cross-repo body stays byte-stable.
func TestValidatePublish_BadLicense_ErrorBodyPinsMenu(t *testing.T) {
	v := payloadValidator()
	err := v.ValidatePublish([]byte(`{"model": {"harness": "claude-code", "model": "x"}, "license": "MIT"}`))
	if err == nil {
		t.Fatal("off-menu license accepted; want 422-class rejection")
	}
	msg := err.Error()
	if !strings.Contains(msg, wantLicenseMenu) {
		t.Errorf("bad-license 422 body lost the verbatim menu clause: got %q, want substring %q", msg, wantLicenseMenu)
	}
	// Defense in depth: every menu value must appear (self-updating if the menu grows).
	for _, l := range schema.AllLicenses {
		if !strings.Contains(msg, string(l)) {
			t.Errorf("bad-license 422 body does not name menu license %q: %q", l, msg)
		}
	}
}
