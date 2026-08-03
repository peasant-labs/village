//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

func TestTitleSafetyMountedPersistenceAndGovernance(t *testing.T) {
	fixtures := loadTitleWriteFixtures(t)
	createCase := titleFixtureNamed(t, fixtures, "shared_project_path_parity")
	republishCase := titleFixtureNamed(t, fixtures, "republish_safe_generated")
	safePatchCase := titleFixtureNamed(t, fixtures, "safe_patch_byte_preservation")
	sensitivePatchCase := titleFixtureNamed(t, fixtures, "sensitive_category_no_write_no_echo")

	pool := publishLockPool(t, 8)
	ctx := context.Background()
	owner := pullInsertUser(t, ctx, pool, 991057, "title-safety-publisher")
	defer cleanupOwners(t, ctx, pool, owner)
	blobs := authoritativeTestBlobStore(t)
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct real title pipeline: %v", err)
	}
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: blobs, titles: titles, cfg: &config.Config{FrontendURL: "https://village.example"}}
	user := &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "title-safety-publisher"}
	localID := "550e8400-e29b-41d4-a716-446655440057"
	content := `{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"title-safety","harness":"claude-code","turns":[]}}`

	created := mountedTitlePublish(t, h, user, localID, content, createCase, schema.LicenseCCBY)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var receipt schema.AuthoritativePublishResponse
	if err := json.Unmarshal(created.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	tid := toPgUUID(uuid.MustParse(receipt.TranscriptID.String()))
	defer deleteCurrentTitleTestBlob(t, ctx, h, blobs, tid)
	assertTitleAndGovernance(t, ctx, h, tid, createCase.Expected, schema.LicenseCCBY, dbVisibilityPrivate, 1)

	safePatched := mountedTitlePatch(t, h, user, tid, safePatchCase.Candidate, "")
	if safePatched.Code != safePatchCase.Status {
		t.Fatalf("safe PATCH status=%d body=%s", safePatched.Code, safePatched.Body.String())
	}
	assertVisibleTitle(t, ctx, h, tid, safePatchCase.Expected)
	assertGovernance(t, ctx, h, tid, schema.LicenseCCBY, dbVisibilityPrivate, 1)

	publicPatched := mountedTitlePatch(t, h, user, tid, "", dbVisibilityPublic)
	if publicPatched.Code != http.StatusOK {
		t.Fatalf("public PATCH status=%d body=%s", publicPatched.Code, publicPatched.Body.String())
	}
	assertGovernance(t, ctx, h, tid, schema.LicenseCCBY, dbVisibilityPublic, 2)

	republished := mountedTitlePublish(t, h, user, localID, content+" ", republishCase, "")
	if republished.Code != http.StatusOK {
		t.Fatalf("republish status=%d body=%s", republished.Code, republished.Body.String())
	}
	assertTitleAndGovernance(t, ctx, h, tid, republishCase.Expected, schema.LicenseCCBY, dbVisibilityPrivate, 3)

	before, err := h.queries.GetTranscriptByID(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	sensitivePatched := mountedTitlePatch(t, h, user, tid, sensitivePatchCase.Candidate, "")
	if sensitivePatched.Code != sensitivePatchCase.Status {
		t.Fatalf("sensitive PATCH status=%d body=%s", sensitivePatched.Code, sensitivePatched.Body.String())
	}
	if strings.Contains(sensitivePatched.Body.String(), sensitivePatchCase.Candidate) || !strings.Contains(sensitivePatched.Body.String(), sensitivePatchCase.Category) {
		t.Fatalf("sensitive PATCH response leaked input or omitted category: %s", sensitivePatched.Body.String())
	}
	after, err := h.queries.GetTranscriptByID(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != before.Title || after.TitleGenerated != before.TitleGenerated || after.LicenseID != before.LicenseID || after.Visibility != before.Visibility || !after.UpdatedAt.Time.Equal(before.UpdatedAt.Time) {
		t.Fatalf("sensitive PATCH changed row: before=%+v after=%+v", before, after)
	}
	assertGovernance(t, ctx, h, tid, schema.LicenseCCBY, dbVisibilityPrivate, 3)
}

func titleFixtureNamed(t *testing.T, fixtures []titleWriteFixture, name string) titleWriteFixture {
	t.Helper()
	for _, fixture := range fixtures {
		if fixture.Name == name {
			return fixture
		}
	}
	t.Fatalf("title fixture %q is absent", name)
	return titleWriteFixture{}
}

func mountedTitlePublish(t *testing.T, h *Handler, user *AuthUser, localID, content string, fixture titleWriteFixture, license schema.License) *httptest.ResponseRecorder {
	t.Helper()
	candidate := fixture.Candidate
	quality := schema.AuthoritativeQualityMetrics(schema.QualityMetrics{TitleGenerated: &candidate})
	request := schema.AuthoritativePublishRequest{
		Identity:  schema.AuthoritativeSessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2},
		Model:     schema.AuthoritativeModelInfo{Harness: schema.Harness(fixture.Harness), Model: "fixture-model"},
		Timestamp: schema.AuthoritativeTimestampInfo{Start: 1700000000000, End: 1700000001000},
		Source:    schema.AuthoritativeSourceInfo{FilePath: "/fixture/session.jsonl", Format: schema.SourceFormatJSONL},
		Project:   schema.AuthoritativeProjectContext{Hash: schema.ProjectHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), FilePath: fixture.ProjectPath, Name: "fixture"},
		Quality:   &quality, License: license, ContentHash: schema.ComputeTranscriptContentHash([]byte(content)), VisibilityIntent: schema.VisibilityIntentPrivate,
	}
	metadata, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, content)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, user))
	response := httptest.NewRecorder()
	h.PublishTranscript(response, req)
	return response
}

func mountedTitlePatch(t *testing.T, h *Handler, user *AuthUser, tid pgtype.UUID, title, visibility string) *httptest.ResponseRecorder {
	t.Helper()
	fields := make([]string, 0, 2)
	if title != "" {
		fields = append(fields, `"title":`+strconv.Quote(title))
	}
	if visibility != "" {
		fields = append(fields, `"visibility":`+strconv.Quote(visibility))
	}
	route := chi.NewRouteContext()
	route.URLParams.Add("id", uuid.UUID(tid.Bytes).String())
	ctx := context.WithValue(context.Background(), UserContextKey, user)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/transcripts/"+uuid.UUID(tid.Bytes).String(), strings.NewReader("{"+strings.Join(fields, ",")+"}")).WithContext(ctx)
	response := httptest.NewRecorder()
	h.UpdateTranscript(response, request)
	return response
}

func assertTitleAndGovernance(t *testing.T, ctx context.Context, h *Handler, tid pgtype.UUID, title string, license schema.License, visibility string, auditCount int) {
	t.Helper()
	row, err := h.queries.GetTranscriptByID(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	if !row.Title.Valid || row.Title.String != title || !row.TitleGenerated.Valid || row.TitleGenerated.String != title {
		t.Fatalf("persisted title pair=%+v/%+v want identical %q", row.Title, row.TitleGenerated, title)
	}
	assertGovernance(t, ctx, h, tid, license, visibility, auditCount)
}

func assertVisibleTitle(t *testing.T, ctx context.Context, h *Handler, tid pgtype.UUID, title string) {
	t.Helper()
	row, err := h.queries.GetTranscriptByID(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	if !row.Title.Valid || row.Title.String != title {
		t.Fatalf("visible title=%+v want exact bytes %q", row.Title, title)
	}
}

func assertGovernance(t *testing.T, ctx context.Context, h *Handler, tid pgtype.UUID, license schema.License, visibility string, auditCount int) {
	t.Helper()
	row, err := h.queries.GetTranscriptByID(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	if !row.LicenseID.Valid || row.LicenseID.String != string(license) || row.Visibility != visibility {
		t.Fatalf("governance license=%+v visibility=%q want %q/%q", row.LicenseID, row.Visibility, license, visibility)
	}
	var count int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id=$1`, tid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != auditCount {
		t.Fatalf("governance audit count=%d want %d", count, auditCount)
	}
}

func deleteCurrentTitleTestBlob(t *testing.T, ctx context.Context, h *Handler, blobs storage.TranscriptBlobStore, tid pgtype.UUID) {
	t.Helper()
	row, err := h.queries.GetTranscriptByID(ctx, tid)
	if err != nil {
		return
	}
	descriptor, err := descriptorFromTranscript(row)
	if err == nil {
		if err := blobs.Delete(ctx, descriptor); err != nil {
			t.Errorf("delete title integration object: %v", err)
		}
	}
}
