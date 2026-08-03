//go:build integration

package handler

// Regression guard for the list-surface license omission (the finding that
// started the PR26 remediation): GET /api/v1/transcripts serialized
// "license_id": null for every row because the hand-built SELECT/scan lists were
// never extended for migration-added columns. ListTranscripts now selects t.*
// with name-addressed scanning, so this test pins the observable outcome: a
// published license comes back on the LIST surface exactly as stored — the same
// transcripts.license_id column the pull surface emits via wireLicense (pull
// emission is pinned by TestPull_LicenseRoundTrip_RealPostgres; together they
// pin list/pull parity).
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:5432/village_test?sslmode=disable" \
//	  go test -tags=integration ./internal/handler/...

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func TestListTranscripts_LicenseParity_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 980701, "list-parity-owner")
	defer cleanupOwners(t, ctx, pool, owner)
	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	// Two public transcripts through the real publish path: one licensed, one not.
	licensed := govStore(t, ctx, h, owner, "list-parity-lic", schema.LicenseCCBY)
	unlicensed := govStore(t, ctx, h, owner, "list-parity-none", "")
	pub := "public"
	for _, tr := range []sqlc.Transcript{licensed, unlicensed} {
		if err := h.inTxAs(ctx, owner, func(q Querier) error {
			_, txErr := applyMetadataPatch(ctx, q, tr.ID, metadataPatch{Visibility: &pub})
			return txErr
		}); err != nil {
			t.Fatalf("make %s public: %v", tr.LocalID, err)
		}
	}

	// Anonymous list (public-only path) — the discovery surface under test.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts?limit=100&owner=list-parity-owner", nil)
	w := httptest.NewRecorder()
	h.ListTranscripts(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("ListTranscripts status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var resp struct {
		Transcripts []struct {
			Transcript struct {
				ID        string  `json:"id"`
				LicenseID *string `json:"license_id"`
			} `json:"transcript"`
		} `json:"transcripts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list response: %v\nbody: %s", err, w.Body.String())
	}

	got := map[string]*string{}
	for _, row := range resp.Transcripts {
		got[row.Transcript.ID] = row.Transcript.LicenseID
	}
	licID := uuid.UUID(licensed.ID.Bytes).String()
	noneID := uuid.UUID(unlicensed.ID.Bytes).String()

	lic, ok := got[licID]
	if !ok {
		t.Fatalf("licensed transcript %s missing from list response (rows: %d)", licID, len(got))
	}
	if lic == nil || *lic != string(schema.LicenseCCBY) {
		t.Errorf("list license_id = %v, want %q — the list surface must serve the stored license, not null", lic, schema.LicenseCCBY)
	}
	none, ok := got[noneID]
	if !ok {
		t.Fatalf("unlicensed transcript %s missing from list response", noneID)
	}
	if none != nil {
		t.Errorf("unlicensed list license_id = %q, want null", *none)
	}
}
