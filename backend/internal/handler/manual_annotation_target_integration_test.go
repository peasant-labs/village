//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
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

//go:embed testdata/manual_annotation_targets/cases.yaml
var manualAnnotationTargetCasesYAML []byte

type manualAnnotationTargetFixture struct {
	ExpectedCaseCount int                          `yaml:"expectedCaseCount"`
	RequiredCaseNames []string                     `yaml:"requiredCaseNames"`
	Cases             []manualAnnotationTargetCase `yaml:"cases"`
}

type manualAnnotationTargetCase struct {
	Name       string `yaml:"name"`
	Action     string `yaml:"action"`
	Target     string `yaml:"target"`
	TypeID     string `yaml:"typeId"`
	Value      string `yaml:"value"`
	EntryIndex int    `yaml:"entryIndex"`
	WantStatus int    `yaml:"wantStatus"`
}

func loadManualAnnotationTargetFixture(t *testing.T) map[string]manualAnnotationTargetCase {
	t.Helper()
	fixture, err := decodeManualAnnotationTargetFixture(manualAnnotationTargetCasesYAML)
	if err != nil {
		t.Fatalf("decode manual annotation target fixture: %v", err)
	}
	if len(fixture.Cases) != fixture.ExpectedCaseCount {
		t.Fatalf("manual annotation target fixture has %d cases, want declared %d", len(fixture.Cases), fixture.ExpectedCaseCount)
	}
	cases := make(map[string]manualAnnotationTargetCase, len(fixture.Cases))
	for _, c := range fixture.Cases {
		if c.Name == "" || c.Action == "" || c.Target == "" || c.WantStatus == 0 {
			t.Fatalf("manual annotation target fixture has incomplete case %+v", c)
		}
		if c.Action != "delete" && (c.TypeID == "" || c.Value == "" || c.EntryIndex < 0) {
			t.Fatalf("manual annotation target fixture has incomplete annotation case %+v", c)
		}
		if _, duplicate := cases[c.Name]; duplicate {
			t.Fatalf("manual annotation target fixture repeats case %q", c.Name)
		}
		cases[c.Name] = c
	}
	for _, name := range fixture.RequiredCaseNames {
		if _, ok := cases[name]; !ok {
			t.Fatalf("manual annotation target fixture omits required case %q", name)
		}
	}
	return cases
}

func decodeManualAnnotationTargetFixture(data []byte) (manualAnnotationTargetFixture, error) {
	var fixture manualAnnotationTargetFixture
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		return manualAnnotationTargetFixture{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return manualAnnotationTargetFixture{}, errors.New("fixture contains a trailing YAML document")
		}
		return manualAnnotationTargetFixture{}, err
	}
	return fixture, nil
}

func TestManualAnnotationTargetFixtureRejectsTrailingDocument(t *testing.T) {
	_, err := decodeManualAnnotationTargetFixture(append(append([]byte{}, manualAnnotationTargetCasesYAML...), []byte("\n---\nunexpected: document\n")...))
	if err == nil || !strings.Contains(err.Error(), "trailing YAML document") {
		t.Fatalf("trailing fixture document error = %v, want explicit rejection", err)
	}
}

func manualAnnotationTargetCaseByName(t *testing.T, cases map[string]manualAnnotationTargetCase, name string) manualAnnotationTargetCase {
	t.Helper()
	c, ok := cases[name]
	if !ok {
		t.Fatalf("manual annotation target fixture has no %q case", name)
	}
	return c
}

func postManualAnnotationAs(t *testing.T, h *Handler, viewer uuid.UUID, transcriptID uuid.UUID, c manualAnnotationTargetCase) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(createManualAnnotationRequest{
		TypeID:     c.TypeID,
		Value:      c.Value,
		EntryIndex: &c.EntryIndex,
	})
	if err != nil {
		t.Fatalf("marshal manual annotation: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/"+transcriptID.String()+"/annotations", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: viewer, Username: "manual-viewer"}))
	r = withChiURLParam(r, "id", transcriptID.String())
	w := httptest.NewRecorder()
	h.CreateTranscriptAnnotation(w, r)
	return w
}

// insertManualAnnotationFixture seeds the post-migration state produced by the
// 030 backfill. The database migration integration test exercises the SQL
// transition; this handler test verifies that the mounted list, pull, count,
// and skip-gate surfaces consume the exact transcript locator.
func insertManualAnnotationFixture(t *testing.T, ctx context.Context, h *Handler, viewer uuid.UUID, entrySessionID string, c manualAnnotationTargetCase, targetTranscriptID *uuid.UUID) string {
	t.Helper()
	contentHash := "fixture-" + uuid.NewString()
	var target any
	if targetTranscriptID != nil {
		target = *targetTranscriptID
	}
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO annotations (
			content_hash, owner_id, target_kind, entry_session_id, entry_index,
			entry_end_index, target_transcript_id, type_id, value, annotator_kind
		) VALUES ($1, $2, 'entry', $3, $4, $5, $6::uuid, $7, $8, 'human')
	`, contentHash, viewer, entrySessionID, c.EntryIndex, c.EntryIndex+1, target, c.TypeID, c.Value); err != nil {
		t.Fatalf("insert manual annotation fixture %q: %v", c.Name, err)
	}
	return contentHash
}

func pullInfoForManualTarget(t *testing.T, h *Handler, viewer uuid.UUID, transcriptID uuid.UUID) schema.PullTranscriptInfo {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+transcriptID.String(), nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: viewer, Username: "manual-viewer"}))
	r = withChiURLParam(r, "id", transcriptID.String())
	w := httptest.NewRecorder()
	h.GetPullTranscript(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("pull transcript status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var info schema.PullTranscriptInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode pull transcript info: %v", err)
	}
	return info
}

func TestManualAnnotationsUseExactTranscriptTargets_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping manual annotation target integration test in -short mode")
	}
	cases := loadManualAnnotationTargetFixture(t)
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	ownerA := associationFixtureUser(t, ctx, pool, 61, "manual-target-owner-a")
	ownerB := associationFixtureUser(t, ctx, pool, 62, "manual-target-owner-b")
	viewer := associationFixtureUser(t, ctx, pool, 63, "manual-target-viewer")
	privateOwner := associationFixtureUser(t, ctx, pool, 64, "manual-target-private")
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM groups WHERE created_by = ANY($1)", []pgtype.UUID{ownerA, ownerB})
		cleanupOwners(t, ctx, pool, ownerA, ownerB, viewer, privateOwner)
	}()

	sharedLocalID := uuid.NewString()
	first := govStore(t, ctx, h, ownerA, sharedLocalID, schema.LicenseCC0)
	second := govStore(t, ctx, h, ownerB, sharedLocalID, schema.LicenseCC0)
	legacyLocalID := uuid.NewString()
	legacy := govStore(t, ctx, h, ownerA, legacyLocalID, schema.LicenseCC0)
	ambiguousLocalID := uuid.NewString()
	ambiguousFirst := govStore(t, ctx, h, ownerA, ambiguousLocalID, schema.LicenseCC0)
	ambiguousSecond := govStore(t, ctx, h, ownerB, ambiguousLocalID, schema.LicenseCC0)
	private := govStore(t, ctx, h, privateOwner, uuid.NewString(), schema.LicenseCC0)
	execAsSystem(t, ctx, pool, "UPDATE transcripts SET visibility = 'shared' WHERE id = ANY($1)", []pgtype.UUID{first.ID, second.ID, legacy.ID, ambiguousFirst.ID, ambiguousSecond.ID})
	firstGroup := pullInsertGroup(t, ctx, pool, ownerA, "manual-target-first-"+uuid.NewString())
	pullAddMember(t, ctx, pool, firstGroup, viewer, "member")
	pullShare(t, ctx, pool, first.ID, firstGroup, "approved")
	pullShare(t, ctx, pool, legacy.ID, firstGroup, "approved")
	pullShare(t, ctx, pool, ambiguousFirst.ID, firstGroup, "approved")
	secondGroup := pullInsertGroup(t, ctx, pool, ownerB, "manual-target-second-"+uuid.NewString())
	pullAddMember(t, ctx, pool, secondGroup, viewer, "member")
	pullShare(t, ctx, pool, second.ID, secondGroup, "approved")
	pullShare(t, ctx, pool, ambiguousSecond.ID, secondGroup, "approved")

	viewerID := uuid.UUID(viewer.Bytes)
	firstID := uuid.UUID(first.ID.Bytes)
	secondID := uuid.UUID(second.ID.Bytes)
	legacyID := uuid.UUID(legacy.ID.Bytes)
	ambiguousFirstID := uuid.UUID(ambiguousFirst.ID.Bytes)
	ambiguousSecondID := uuid.UUID(ambiguousSecond.ID.Bytes)
	privateID := uuid.UUID(private.ID.Bytes)
	firstCase := manualAnnotationTargetCaseByName(t, cases, "non-owner labels first shared transcript")
	requireManualAnnotationFixtureTarget(t, firstCase, "first")
	firstCreated := requireManualAnnotationCreated(t, h, viewerID, firstID, firstCase)
	secondCase := manualAnnotationTargetCaseByName(t, cases, "same viewer labels second shared transcript with same local ID")
	requireManualAnnotationFixtureTarget(t, secondCase, "second")
	secondCreated := requireManualAnnotationCreated(t, h, viewerID, secondID, secondCase)
	legacyCase := manualAnnotationTargetCaseByName(t, cases, "migrated viewer label follows globally unique transcript")
	requireManualAnnotationFixtureTarget(t, legacyCase, "legacy")
	legacyHash := insertManualAnnotationFixture(t, ctx, h, viewerID, legacyLocalID, legacyCase, &legacyID)
	requireExactManualTarget(t, ctx, h, viewer, viewerID, legacyID, "legacy", legacyHash)
	ambiguousCase := manualAnnotationTargetCaseByName(t, cases, "duplicate legacy local ID stays unbound")
	requireManualAnnotationFixtureTarget(t, ambiguousCase, "ambiguous")
	ambiguousHash := insertManualAnnotationFixture(t, ctx, h, viewerID, ambiguousLocalID, ambiguousCase, nil)
	requireNoManualTarget(t, ctx, h, viewerID, ambiguousFirstID, "ambiguous-first")
	requireNoManualTarget(t, ctx, h, viewerID, ambiguousSecondID, "ambiguous-second")
	repeatCase := manualAnnotationTargetCaseByName(t, cases, "repeat label on first exact transcript deduplicates")
	requireManualAnnotationFixtureTarget(t, repeatCase, "first")
	repeated := requireManualAnnotationCreated(t, h, viewerID, firstID, repeatCase)
	privateCase := manualAnnotationTargetCaseByName(t, cases, "inaccessible transcript remains hidden")
	requireManualAnnotationFixtureTarget(t, privateCase, "private")
	privateResponse := postManualAnnotationAs(t, h, viewerID, privateID, privateCase)
	if privateResponse.Code != privateCase.WantStatus {
		t.Fatalf("%s: status=%d, want %d (body: %s)", privateCase.Name, privateResponse.Code, privateCase.WantStatus, privateResponse.Body.String())
	}

	firstHash := requireManualAnnotationHash(t, firstCase.Name, firstCreated)
	secondHash := requireManualAnnotationHash(t, secondCase.Name, secondCreated)
	if firstHash == secondHash {
		t.Fatalf("same viewer labels on distinct same-local-id transcripts share hash %q", firstHash)
	}
	if repeatedHash := requireManualAnnotationHash(t, repeatCase.Name, repeated); repeatedHash != firstHash {
		t.Fatalf("exact target replay hash=%q, want %q", repeatedHash, firstHash)
	}
	requireExactManualTarget(t, ctx, h, viewer, viewerID, firstID, "first", firstHash)
	requireExactManualTarget(t, ctx, h, viewer, viewerID, secondID, "second", secondHash)

	manifest := associationAnnotationManifest(t, h, viewerID)
	if len(manifest.Hashes) != 4 || !containsHash(manifest.Hashes, firstHash) || !containsHash(manifest.Hashes, secondHash) || !containsHash(manifest.Hashes, legacyHash) || !containsHash(manifest.Hashes, ambiguousHash) {
		t.Fatalf("viewer manifest=%v, want both exact-target hashes and both legacy fixture hashes", manifest.Hashes)
	}
	skip := callSkipGate(t, h, viewerID, schema.PullSkipGateRequest{Items: []schema.PullSkipGateItem{
		{TranscriptID: wireTranscriptID(first.ID), AnnotationHashes: []string{firstHash}},
		{TranscriptID: wireTranscriptID(second.ID), AnnotationHashes: []string{secondHash}},
		{TranscriptID: wireTranscriptID(legacy.ID), AnnotationHashes: []string{legacyHash}},
		{TranscriptID: wireTranscriptID(ambiguousFirst.ID), AnnotationHashes: []string{ambiguousHash}},
		{TranscriptID: wireTranscriptID(ambiguousSecond.ID), AnnotationHashes: []string{ambiguousHash}},
	}})
	if len(skip.Results) != 5 {
		t.Fatalf("manual target skip gate returned %d results, want 5", len(skip.Results))
	}
	for _, result := range skip.Results {
		wantCurrent := result.TranscriptID == wireTranscriptID(first.ID) || result.TranscriptID == wireTranscriptID(second.ID) || result.TranscriptID == wireTranscriptID(legacy.ID)
		if result.AnnotationsCurrent != wantCurrent {
			t.Fatalf("manual target skip gate result=%+v, want annotationsCurrent=%v", result, wantCurrent)
		}
	}

	deleteCase := manualAnnotationTargetCaseByName(t, cases, "transcript deletion cascades its manual labels")
	requireManualAnnotationFixtureTarget(t, deleteCase, "first")
	if deleteCase.WantStatus != http.StatusNoContent {
		t.Fatalf("delete fixture status=%d, want 204", deleteCase.WantStatus)
	}
	execAsSystem(t, ctx, pool, "DELETE FROM transcripts WHERE id = $1", first.ID)
	var firstRows, secondRows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM annotations WHERE target_transcript_id = $1", first.ID).Scan(&firstRows); err != nil {
		t.Fatalf("count cascaded first manual labels: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM annotations WHERE target_transcript_id = $1", second.ID).Scan(&secondRows); err != nil {
		t.Fatalf("count surviving second manual labels: %v", err)
	}
	if firstRows != 0 || secondRows != 1 {
		t.Fatalf("manual target cascade rows first/second=%d/%d, want 0/1", firstRows, secondRows)
	}
	// The retract trigger intentionally preserves governance evidence after a
	// transcript deletion. This in-body deletion therefore needs the sanctioned
	// test-only maintenance cleanup that cleanupOwners cannot see once the row is
	// gone.
	purgeAuditRows(t, ctx, pool, []pgtype.UUID{first.ID})
}

func containsHash(hashes []string, want string) bool {
	for _, hash := range hashes {
		if hash == want {
			return true
		}
	}
	return false
}

func requireManualAnnotationCreated(t *testing.T, h *Handler, viewer, transcriptID uuid.UUID, c manualAnnotationTargetCase) schema.AnnotationSummary {
	t.Helper()
	w := postManualAnnotationAs(t, h, viewer, transcriptID, c)
	if w.Code != c.WantStatus {
		t.Fatalf("%s: status=%d, want %d (body: %s)", c.Name, w.Code, c.WantStatus, w.Body.String())
	}
	if c.WantStatus != http.StatusCreated {
		t.Fatalf("%s: fixture status=%d, helper requires 201", c.Name, c.WantStatus)
	}
	var created schema.AnnotationSummary
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("%s: decode created annotation: %v", c.Name, err)
	}
	return created
}

func requireManualAnnotationFixtureTarget(t *testing.T, c manualAnnotationTargetCase, want string) {
	t.Helper()
	if c.Target != want {
		t.Fatalf("%s: fixture target=%q, want %q", c.Name, c.Target, want)
	}
}

func requireManualAnnotationHash(t *testing.T, caseName string, created schema.AnnotationSummary) string {
	t.Helper()
	if created.ContentHash == nil || *created.ContentHash == "" {
		t.Fatalf("%s: created manual annotation lacks content hash", caseName)
	}
	return *created.ContentHash
}

func requireExactManualTarget(t *testing.T, ctx context.Context, h *Handler, viewer pgtype.UUID, viewerID, transcriptID uuid.UUID, label, wantHash string) {
	t.Helper()
	rows, err := h.queries.ListAnnotationsByTranscriptID(ctx, toPgUUID(transcriptID))
	if err != nil {
		t.Fatalf("list exact manual target %s: %v", label, err)
	}
	if len(rows) != 1 || !rows[0].TargetTranscriptID.Valid || uuid.UUID(rows[0].TargetTranscriptID.Bytes) != transcriptID || rows[0].OwnerID != viewer {
		t.Fatalf("list exact manual target %s = %+v, want viewer row bound to transcript %s", label, rows, transcriptID)
	}
	listed := listTranscriptAssociationAnnotations(t, h, viewerID, transcriptID)
	if len(listed) != 1 || listed[0].ContentHash == nil || *listed[0].ContentHash != wantHash {
		t.Fatalf("mounted refetch for %s = %+v, want only its viewer label", label, listed)
	}
	if count, err := h.queries.CountTranscriptAnnotations(ctx, toPgUUID(transcriptID)); err != nil || count != 1 {
		t.Fatalf("pull count for %s = %d, %v; want 1, nil", label, count, err)
	}
	info := pullInfoForManualTarget(t, h, viewerID, transcriptID)
	if info.AnnotationCount != 1 {
		t.Fatalf("mounted pull count for %s = %d, want 1", label, info.AnnotationCount)
	}
	pulled := pullTranscriptAssociationAnnotations(t, h, viewerID, transcriptID)
	if len(pulled) != 1 || pulled[0].ContentHash == nil || *pulled[0].ContentHash != wantHash || pulled[0].AuthorUserID != viewerID.String() {
		t.Fatalf("mounted pull annotation refetch for %s = %+v, want only viewer label", label, pulled)
	}
}

func requireNoManualTarget(t *testing.T, ctx context.Context, h *Handler, viewerID, transcriptID uuid.UUID, label string) {
	t.Helper()
	rows, err := h.queries.ListAnnotationsByTranscriptID(ctx, toPgUUID(transcriptID))
	if err != nil {
		t.Fatalf("list absent manual target %s: %v", label, err)
	}
	if len(rows) != 0 {
		t.Fatalf("list absent manual target %s = %+v, want no rows", label, rows)
	}
	listed := listTranscriptAssociationAnnotations(t, h, viewerID, transcriptID)
	if len(listed) != 0 {
		t.Fatalf("mounted refetch for absent %s = %+v, want no rows", label, listed)
	}
	count, err := h.queries.CountTranscriptAnnotations(ctx, toPgUUID(transcriptID))
	if err != nil || count != 0 {
		t.Fatalf("pull count for absent %s = %d, %v; want 0, nil", label, count, err)
	}
	info := pullInfoForManualTarget(t, h, viewerID, transcriptID)
	if info.AnnotationCount != 0 {
		t.Fatalf("mounted pull count for absent %s = %d, want 0", label, info.AnnotationCount)
	}
	pulled := pullTranscriptAssociationAnnotations(t, h, viewerID, transcriptID)
	if len(pulled) != 0 {
		t.Fatalf("mounted pull annotation refetch for absent %s = %+v, want no rows", label, pulled)
	}
}
