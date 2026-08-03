package handler

// Secret-scan 422-body pin. The publish handler rejects any transcript whose
// content trips the redaction scanner, surfacing the message that
// scanner.FormatScanErrors builds over scanner.ScanForSecrets. That rejection
// body is a cross-repo contract: peasant's client couples on its exact wording,
// so it must not drift silently. This pin holds the verbatim prefix against the
// REAL producers (no mock of the subject), co-located with the license-422 pin
// so every cross-repo-body pin lives together.

import (
	"strings"
	"testing"

	"github.com/peasant-labs/village/backend/internal/scanner"
)

// wantSecretScanPrefix is the verbatim leading clause the secret-scan rejection
// must surface. peasant's client couples on this exact wording.
const wantSecretScanPrefix = "Redaction check failed. Potential secrets detected:"

// TestScanForSecrets_ErrorBodyPinsPrefix pins the cross-repo secret-scan 422 body:
// peasant's client couples on this exact wording, so the rejection message the
// publish handler surfaces (scanner.FormatScanErrors over scanner.ScanForSecrets)
// must carry the verbatim prefix. Fast unit — no DB, deterministic planted secret.
func TestScanForSecrets_ErrorBodyPinsPrefix(t *testing.T) {
	// A planted AWS access key trips scanner.ScanForSecrets deterministically.
	issues := scanner.ScanForSecrets([]byte("token AKIAIOSFODNN7EXAMPLE in the log"))
	if len(issues) == 0 {
		t.Fatal("planted secret was not detected; the scanner pattern set changed")
	}
	body := scanner.FormatScanErrors(issues)
	if !strings.HasPrefix(body, wantSecretScanPrefix) {
		t.Errorf("secret-scan 422 body lost its verbatim prefix: got %q, want prefix %q", body, wantSecretScanPrefix)
	}
}
