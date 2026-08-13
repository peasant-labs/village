package handler

// Migrate-on-read unit tests for blobMigrator.
//
// Exercises the 3-way envelope sniff (B6, incl. a partType-bearing CURRENT
// envelope), key+value migration (provider/modelHarness->harness,
// claude/gemini->claude-code/gemini-cli), and the rewrite/idempotence contract.
// The migrator is the real SUT (NewContentMigrator) — not mocked.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/peasant-labs/schema"
)

// currentEnvelopeJSON builds a current-contract TranscriptContent envelope whose
// embedded sessionDetail carries a turn with an extra post-merge "partType"
// field (B6: exercise the discriminator against CURRENT, partType-bearing
// content — not only legacy).
func currentEnvelopeJSON(t *testing.T, harness string) []byte {
	t.Helper()
	blob := `{
      "contractVersion": "0.1.0",
      "kind": "session_detail",
      "sessionDetail": {
        "schemaVersion": "0.1.0",
        "id": "sess-current",
        "harness": "` + harness + `",
        "startTime": "2026-01-01T00:00:00Z",
        "endTime": "2026-01-01T00:01:00Z",
        "turns": [
          {"index": 0, "role": "user", "content": "hello", "partType": "text"}
        ]
      }
    }`
	return []byte(blob)
}

// legacyBarePayloadJSON builds a bare SessionDetailPayload using the OLD
// provider-keyed shape (json:"provider" + legacy value), with NO envelope.
func legacyBarePayloadJSON(provider string) []byte {
	return []byte(`{
      "id": "sess-legacy",
      "provider": "` + provider + `",
      "startTime": "2026-01-01T00:00:00Z",
      "endTime": "2026-01-01T00:01:00Z",
      "turns": [
        {"index": 0, "role": "user", "content": "hi"},
        {"index": 1, "role": "assistant", "content": "yo"}
      ]
    }`)
}

func rawJSONLBlob() []byte {
	return []byte(`[{"role":"user","content":"hi"},{"role":"assistant","content":"yo"}]`)
}

// envelopeAtVersion builds a well-formed TranscriptContent envelope stamped at an
// ARBITRARY contractVersion (outer) / schemaVersion (embedded), so the
// patch-tolerance vs different-minor migrate-on-read dispatch can be exercised
// against currentContractVersion (0.1.1) without depending on the constant's
// literal value.
func envelopeAtVersion(contractVersion, schemaVersion, harness string) []byte {
	return []byte(`{
      "contractVersion": "` + contractVersion + `",
      "kind": "session_detail",
      "sessionDetail": {
        "schemaVersion": "` + schemaVersion + `",
        "id": "sess-vers",
        "harness": "` + harness + `",
        "startTime": "2026-01-01T00:00:00Z",
        "endTime": "2026-01-01T00:01:00Z",
        "turns": [
          {"index": 0, "role": "user", "content": "hello"}
        ]
      }
    }`)
}

// TestMigrate_PatchTolerant_PriorPatchNoRewrite locks the user-ratified Option A
// patch-tolerance semantics (1e8tk): a stored envelope at the PRIOR patch
// (0.1.0) is shape-identical to currentContractVersion (0.1.1) — same MAJOR.MINOR
// — so it is served NO-REWRITE. Exact-equality dispatch (the pre-1e8tk behavior)
// would churn every stored 0.1.0 blob the instant the constant bumped to 0.1.1;
// this test would catch that regression.
func TestMigrate_PatchTolerant_PriorPatchNoRewrite(t *testing.T) {
	if contractMinor(currentContractVersion) != "0.1" {
		t.Fatalf("test assumes current MAJOR.MINOR 0.1, got %q (current=%q)", contractMinor(currentContractVersion), currentContractVersion)
	}
	m := NewContentMigrator()
	got, rewrite, err := m.Migrate(context.Background(), envelopeAtVersion("0.1.0", "0.1.0", "claude-code"))
	if err != nil {
		t.Fatalf("Migrate prior-patch envelope: %v", err)
	}
	if rewrite {
		t.Errorf("prior-patch (0.1.0) envelope must be served no-rewrite under current=%q, got rewrite=true", currentContractVersion)
	}
	if got == nil || got.Harness != schema.HarnessClaudeCode {
		t.Errorf("harness: got %+v, want %q", got, schema.HarnessClaudeCode)
	}
	// No-rewrite ⇒ the embedded schemaVersion is served AS STORED (0.1.0), not
	// re-stamped — the migrator does not touch a shape-compatible payload.
	if got.SchemaVersion != schema.PushContractVersion("0.1.0") {
		t.Errorf("no-rewrite must preserve stored schemaVersion 0.1.0, got %q", got.SchemaVersion)
	}
}

// TestMigrate_DifferentMinor_RewritesAndRestamps proves the other side of the
// shape gate: an envelope at a DIFFERENT MAJOR.MINOR (older 0.0.9 and newer-minor
// 0.2.0) is shape-incompatible, so it IS rewritten and re-stamped to
// currentContractVersion.
func TestMigrate_DifferentMinor_RewritesAndRestamps(t *testing.T) {
	cases := []struct{ name, version string }{
		{"older-minor-0.0.9", "0.0.9"},
		{"newer-minor-0.2.0", "0.2.0"},
	}
	m := NewContentMigrator()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, rewrite, err := m.Migrate(context.Background(), envelopeAtVersion(tc.version, tc.version, "claude-code"))
			if err != nil {
				t.Fatalf("Migrate %s envelope: %v", tc.version, err)
			}
			if !rewrite {
				t.Errorf("different-minor (%s) envelope must rewrite under current=%q, got rewrite=false", tc.version, currentContractVersion)
			}
			if got == nil || got.SchemaVersion != currentContractVersion {
				t.Errorf("rewrite must re-stamp schemaVersion to %q, got %+v", currentContractVersion, got)
			}
		})
	}
}

// TestSameContractShape_MajorMinor unit-tests the shape predicate directly: equal
// in MAJOR.MINOR (patch differs) is shape-compatible; a differing MAJOR or MINOR
// is not.
func TestSameContractShape_MajorMinor(t *testing.T) {
	cases := []struct {
		a, b schema.PushContractVersion
		want bool
	}{
		{"0.1.0", "0.1.1", true},
		{"0.1.1", "0.1.0", true},
		{"0.1.0", "0.1.0", true},
		{"0.1.0", "0.2.0", false},
		{"0.1.0", "0.0.9", false},
		{"0.1.0", "1.1.0", false},
	}
	for _, tc := range cases {
		if got := sameContractShape(tc.a, tc.b); got != tc.want {
			t.Errorf("sameContractShape(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMigrate_CurrentEnvelope_NoRewrite(t *testing.T) {
	m := NewContentMigrator()
	got, rewrite, err := m.Migrate(context.Background(), currentEnvelopeJSON(t, "claude-code"))
	if err != nil {
		t.Fatalf("Migrate current envelope: %v", err)
	}
	if got == nil {
		t.Fatal("Migrate returned nil payload for current envelope")
	}
	if rewrite {
		t.Errorf("current-contract envelope must NOT rewrite, got rewrite=true")
	}
	if got.Harness != schema.HarnessClaudeCode {
		t.Errorf("harness: got %q, want %q", got.Harness, schema.HarnessClaudeCode)
	}
	if got.ID != "sess-current" {
		t.Errorf("id: got %q, want sess-current", got.ID)
	}
	if len(got.Turns) != 1 {
		t.Errorf("turns: got %d, want 1", len(got.Turns))
	}
}

func TestMigrate_BarePayload_LegacyKeyAndValue_Rewrite(t *testing.T) {
	cases := []struct {
		legacy string
		want   schema.Harness
	}{
		{"claude", schema.HarnessClaudeCode},
		{"gemini", schema.HarnessGeminiCLI},
	}
	m := NewContentMigrator()
	for _, tc := range cases {
		t.Run(tc.legacy, func(t *testing.T) {
			got, rewrite, err := m.Migrate(context.Background(), legacyBarePayloadJSON(tc.legacy))
			if err != nil {
				t.Fatalf("Migrate legacy bare payload: %v", err)
			}
			if !rewrite {
				t.Errorf("legacy bare payload must rewrite (rewrite=true), got false")
			}
			if got.Harness != tc.want {
				t.Errorf("harness key+value migrate: got %q, want %q", got.Harness, tc.want)
			}
			if len(got.Turns) != 2 {
				t.Errorf("turns preserved: got %d, want 2", len(got.Turns))
			}
		})
	}
}

func TestMigrate_RawJSONL_Rewrite(t *testing.T) {
	m := NewContentMigrator()
	got, rewrite, err := m.Migrate(context.Background(), rawJSONLBlob())
	if err != nil {
		t.Fatalf("Migrate raw JSONL: %v", err)
	}
	if !rewrite {
		t.Errorf("raw JSONL must rewrite (rewrite=true), got false")
	}
	if got == nil || len(got.Turns) != 2 {
		t.Fatalf("raw JSONL turns: got %+v, want 2 turns", got)
	}
}

// TestMigrate_Idempotent_SecondReadNoop: migrating a legacy blob yields a
// payload; re-storing it in the canonical current envelope and migrating THAT
// must be a no-op (rewrite=false) — the second read does not re-migrate.
func TestMigrate_Idempotent_SecondReadNoop(t *testing.T) {
	m := NewContentMigrator()
	first, rewrite1, err := m.Migrate(context.Background(), legacyBarePayloadJSON("claude"))
	if err != nil || !rewrite1 {
		t.Fatalf("first migrate: rewrite=%v err=%v (want rewrite=true)", rewrite1, err)
	}
	// Re-encode as the canonical current envelope (what the read handler stores).
	canonical, err := json.Marshal(schema.TranscriptContent{
		ContractVersion: currentContractVersion,
		Kind:            schema.ContentKindSessionDetail,
		SessionDetail:   first,
	})
	if err != nil {
		t.Fatalf("marshal canonical envelope: %v", err)
	}
	_, rewrite2, err := m.Migrate(context.Background(), canonical)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if rewrite2 {
		t.Errorf("second read of canonical envelope must be a no-op (rewrite=false), got true")
	}
}

func TestMigrate_EmptyBlob_Error(t *testing.T) {
	m := NewContentMigrator()
	if _, _, err := m.Migrate(context.Background(), []byte("   ")); err == nil {
		t.Error("expected error for empty/whitespace blob, got nil")
	}
}
