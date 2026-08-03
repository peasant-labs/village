//go:build integration

package handler

// Real-Postgres integration tests for the pull skip-gate. They drive the REAL
// PullSkipGate handler (and thus the real pull-scoped + owner-scoped batch SQL)
// against a live database, asserting observable response bytes. Every currency
// field has a pos and neg control; the pull-scoping and owner-scoping controls
// are non-vacuous (a broken predicate would leak / flip the answer).
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:5432/village_test?sslmode=disable" \
//	  go test -tags=integration -race ./internal/handler/ -run TestPullSkipGate

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/pull_skip_gate/currency_cases.yaml
var skipGateCurrencyYAML []byte

// callSkipGate drives the REAL PullSkipGate handler for a caller and decodes the
// observable response.
func callSkipGate(t *testing.T, h *Handler, caller uuid.UUID, req schema.PullSkipGateRequest) schema.PullSkipGateResponse {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal skip-gate request: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/pull/transcripts/skip-gate", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: caller, Username: "skip-gate-caller"}))
	w := httptest.NewRecorder()
	h.PullSkipGate(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("skip-gate status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp schema.PullSkipGateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode skip-gate response: %v", err)
	}
	return resp
}

// setServerContentHash records the served-plaintext hash through the fixed
// system writer transaction required for storage identity mutations.
func setServerContentHash(t *testing.T, ctx context.Context, h *Handler, id pgtype.UUID, hash string) {
	t.Helper()
	if err := h.inTxAsSystem(ctx, func(q Querier) error {
		return q.SetTranscriptContentHash(ctx, sqlc.SetTranscriptContentHashParams{ID: id, ContentHash: pgtype.Text{String: hash, Valid: true}})
	}); err != nil {
		t.Fatalf("set server content hash: %v", err)
	}
}

// pushSessionAnnotations pushes owner-scoped session-target annotations linking to
// a transcript's local id (session_id = localID), one per content hash, through
// the real annotation-push path.
func pushSessionAnnotations(t *testing.T, h *Handler, owner uuid.UUID, localID string, hashes ...string) {
	t.Helper()
	if len(hashes) == 0 {
		return
	}
	items := make([]schema.AnnotationPushItem, 0, len(hashes))
	for _, hash := range hashes {
		sess := localID
		items = append(items, schema.AnnotationPushItem{
			ContentHash: hash,
			TargetKind:  schema.TargetSession,
			TypeID:      "quality.session_outcome",
			Value:       "resolved",
			SessionID:   &sess,
		})
	}
	postBulkAnnotations(t, h, owner, schema.AnnotationPushRequest{Annotations: items})
}

type skipGateCurrencyCase struct {
	Name                   string   `yaml:"name"`
	ServerContentHash      string   `yaml:"serverContentHash"`
	ClientContentHash      string   `yaml:"clientContentHash"`
	ServerAnnotationHashes []string `yaml:"serverAnnotationHashes"`
	ClientAnnotationHashes []string `yaml:"clientAnnotationHashes"`
	WantContentCurrent     bool     `yaml:"wantContentCurrent"`
	WantAnnotationsCurrent bool     `yaml:"wantAnnotationsCurrent"`
}

type skipGateCurrencyFixture struct {
	Cases []skipGateCurrencyCase `yaml:"cases"`
}

func decodeSkipGateCurrencyFixture(data []byte) (skipGateCurrencyFixture, error) {
	var fixture skipGateCurrencyFixture
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		return skipGateCurrencyFixture{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return skipGateCurrencyFixture{}, fmt.Errorf("currency fixture must contain exactly one YAML document")
	}
	return fixture, nil
}

func TestPullSkipGateCurrencyFixtureRejectsUnknownFields(t *testing.T) {
	unknown := append([]byte("unexpected_fixture_field: true\n"), skipGateCurrencyYAML...)
	if _, err := decodeSkipGateCurrencyFixture(unknown); err == nil || !strings.Contains(err.Error(), "unexpected_fixture_field") {
		t.Fatalf("unknown field error = %v, want strict rejection", err)
	}
	trailing := append(append([]byte{}, skipGateCurrencyYAML...), []byte("\n---\nunexpected: document\n")...)
	if _, err := decodeSkipGateCurrencyFixture(trailing); err == nil || !strings.Contains(err.Error(), "exactly one YAML document") {
		t.Fatalf("trailing document error = %v, want single-document rejection", err)
	}
}

// TestPullSkipGate_CurrencyMatrix drives the per-field pos+neg currency controls
// from the fixture corpus: contentCurrent (match / stale / legacy-NULL) and
// annotationsCurrent (match / missing / extra), each against a real transcript.
func TestPullSkipGate_CurrencyMatrix(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	f, err := decodeSkipGateCurrencyFixture(skipGateCurrencyYAML)
	if err != nil {
		t.Fatalf("load currency cases: %v", err)
	}
	// Row-count guard: the corpus must keep covering the field matrix.
	if len(f.Cases) < 8 {
		t.Fatalf("currency corpus has %d cases, want >= 8", len(f.Cases))
	}

	for i, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			owner := pullInsertUser(t, ctx, pool, int64(933001+i), fmt.Sprintf("sg-matrix-%d", i))
			defer cleanupOwners(t, ctx, pool, owner)
			ownerID := uuid.UUID(owner.Bytes)

			localID := uuid.NewString()
			tr := govStore(t, ctx, h, owner, localID, schema.LicenseCC0)
			if c.ServerContentHash != "" {
				setServerContentHash(t, ctx, h, tr.ID, c.ServerContentHash)
			}
			pushSessionAnnotations(t, h, ownerID, localID, c.ServerAnnotationHashes...)

			resp := callSkipGate(t, h, ownerID, schema.PullSkipGateRequest{Items: []schema.PullSkipGateItem{{
				TranscriptID:     wireTranscriptID(tr.ID),
				ContentHash:      c.ClientContentHash,
				AnnotationHashes: c.ClientAnnotationHashes,
			}}})

			if len(resp.Results) != 1 {
				t.Fatalf("want exactly 1 result, got %d (%+v)", len(resp.Results), resp.Results)
			}
			got := resp.Results[0]
			if got.ContentCurrent != c.WantContentCurrent {
				t.Errorf("contentCurrent: got %v, want %v", got.ContentCurrent, c.WantContentCurrent)
			}
			if got.AnnotationsCurrent != c.WantAnnotationsCurrent {
				t.Errorf("annotationsCurrent: got %v, want %v", got.AnnotationsCurrent, c.WantAnnotationsCurrent)
			}
		})
	}
}

// TestPullSkipGate_PullScopingOmission is the leak-free control: a mixed batch of
// a pullable id and a REAL non-pullable id (a transcript owned by another user)
// answers only the pullable id and WITHHOLDS the non-pullable one by omission. A
// broken pull predicate would leak the non-pullable id, so the control is
// non-vacuous.
func TestPullSkipGate_PullScopingOmission(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	a := pullInsertUser(t, ctx, pool, 933050, "sg-caller-a")
	b := pullInsertUser(t, ctx, pool, 933051, "sg-owner-b")
	defer cleanupOwners(t, ctx, pool, a, b)
	aID := uuid.UUID(a.Bytes)

	aTr := govStore(t, ctx, h, a, uuid.NewString(), schema.LicenseCC0)
	setServerContentHash(t, ctx, h, aTr.ID, "a-hash")
	// A real transcript owned by B, never shared with A: it EXISTS but A cannot
	// pull it, so its currency must be withheld (not a denial marker, an omission).
	bTr := govStore(t, ctx, h, b, uuid.NewString(), schema.LicenseCC0)
	setServerContentHash(t, ctx, h, bTr.ID, "b-hash")

	resp := callSkipGate(t, h, aID, schema.PullSkipGateRequest{Items: []schema.PullSkipGateItem{
		{TranscriptID: wireTranscriptID(aTr.ID), ContentHash: "a-hash"},
		{TranscriptID: wireTranscriptID(bTr.ID), ContentHash: "b-hash"},
	}})

	if len(resp.Results) != 1 {
		t.Fatalf("want exactly 1 result (only A's pullable id), got %d (%+v)", len(resp.Results), resp.Results)
	}
	if resp.Results[0].TranscriptID != wireTranscriptID(aTr.ID) {
		t.Errorf("answered id = %q, want A's pullable id %q", resp.Results[0].TranscriptID, wireTranscriptID(aTr.ID))
	}
	for _, res := range resp.Results {
		if res.TranscriptID == wireTranscriptID(bTr.ID) {
			t.Error("non-pullable transcript owned by B leaked into the skip-gate response")
		}
	}
}

// TestPullSkipGate_OwnerScopedAnnotations proves annotation currency is
// owner-scoped: another owner's annotations for the SAME session id never enter
// the caller's answer. Under a global (non-owner-scoped) gate the server set would
// include B's hash and flip the answer.
func TestPullSkipGate_OwnerScopedAnnotations(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	a := pullInsertUser(t, ctx, pool, 933060, "sg-owner-scope-a")
	b := pullInsertUser(t, ctx, pool, 933061, "sg-owner-scope-b")
	defer cleanupOwners(t, ctx, pool, a, b)
	aID := uuid.UUID(a.Bytes)
	bID := uuid.UUID(b.Bytes)

	localID := uuid.NewString()
	tr := govStore(t, ctx, h, a, localID, schema.LicenseCC0)
	setServerContentHash(t, ctx, h, tr.ID, "hash")

	// A owns one annotation for the transcript; B pushes an annotation with the
	// SAME session id (linking to the same local id) but under B's ownership.
	pushSessionAnnotations(t, h, aID, localID, "a-ann-1")
	pushSessionAnnotations(t, h, bID, localID, "b-ann-1")

	// A holds exactly its own {a-ann-1}: owner-scoped server set is {a-ann-1}, so
	// annotationsCurrent is TRUE (B's b-ann-1 is excluded).
	resp := callSkipGate(t, h, aID, schema.PullSkipGateRequest{Items: []schema.PullSkipGateItem{{
		TranscriptID:     wireTranscriptID(tr.ID),
		ContentHash:      "hash",
		AnnotationHashes: []string{"a-ann-1"},
	}}})
	if len(resp.Results) != 1 || !resp.Results[0].AnnotationsCurrent {
		t.Fatalf("owner-scoped: annotationsCurrent must be true with B's annotation excluded; got %+v", resp.Results)
	}

	// Non-vacuity: if A instead held B's hash, it is EXTRA versus the owner-scoped
	// server set {a-ann-1}, so annotationsCurrent is FALSE. This proves the server
	// set is A-only, not the global {a-ann-1, b-ann-1}.
	resp2 := callSkipGate(t, h, aID, schema.PullSkipGateRequest{Items: []schema.PullSkipGateItem{{
		TranscriptID:     wireTranscriptID(tr.ID),
		ContentHash:      "hash",
		AnnotationHashes: []string{"a-ann-1", "b-ann-1"},
	}}})
	if len(resp2.Results) != 1 || resp2.Results[0].AnnotationsCurrent {
		t.Fatalf("owner-scoped non-vacuity: holding B's hash must read EXTRA => false; got %+v", resp2.Results)
	}
}

// TestPullSkipGate_CrossOwnerNoOracle is the differential no-oracle control: B's
// answer for its own transcript is byte-identical whether or not A has published a
// transcript with the SAME content hash. B learns nothing about A's content.
func TestPullSkipGate_CrossOwnerNoOracle(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	a := pullInsertUser(t, ctx, pool, 933070, "sg-nooracle-a")
	b := pullInsertUser(t, ctx, pool, 933071, "sg-nooracle-b")
	defer cleanupOwners(t, ctx, pool, a, b)
	bID := uuid.UUID(b.Bytes)

	bTr := govStore(t, ctx, h, b, uuid.NewString(), schema.LicenseCC0)
	setServerContentHash(t, ctx, h, bTr.ID, "shared-hash")

	req := schema.PullSkipGateRequest{Items: []schema.PullSkipGateItem{{
		TranscriptID: wireTranscriptID(bTr.ID),
		ContentHash:  "shared-hash",
	}}}

	// B's answer BEFORE A publishes anything.
	before := callSkipGate(t, h, bID, req)

	// A publishes a transcript carrying the identical content hash.
	aTr := govStore(t, ctx, h, a, uuid.NewString(), schema.LicenseCC0)
	setServerContentHash(t, ctx, h, aTr.ID, "shared-hash")

	// B's answer AFTER must be byte-identical: A's identical content is invisible.
	after := callSkipGate(t, h, bID, req)

	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if !bytes.Equal(beforeJSON, afterJSON) {
		t.Errorf("cross-owner oracle: B's answer changed after A published identical content\n before=%s\n after =%s", beforeJSON, afterJSON)
	}
	if len(after.Results) != 1 || !after.Results[0].ContentCurrent {
		t.Errorf("B's own transcript should read contentCurrent=true; got %+v", after.Results)
	}
}
