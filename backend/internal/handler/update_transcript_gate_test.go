package handler

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/update_transcript_gate/cases.yaml
var updateTranscriptGateYAML []byte

type updateTranscriptGateCase struct {
	Arm               string `yaml:"arm"`
	Name              string `yaml:"name"`
	Body              string `yaml:"body"`
	PreLicense        string `yaml:"preLicense"`
	PreVisibility     string `yaml:"preVisibility"`
	WantStatus        int    `yaml:"wantStatus"`
	WantBody          string `yaml:"wantBody"`
	WantLicense       string `yaml:"wantLicense"`
	WantLicenseIsNull bool   `yaml:"wantLicenseIsNull"`
}

func loadUpdateTranscriptGateCases(t *testing.T) []updateTranscriptGateCase {
	t.Helper()
	var cases []updateTranscriptGateCase
	decoder := yaml.NewDecoder(bytes.NewReader(updateTranscriptGateYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("decode update transcript gate fixtures: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("update transcript gate fixture must contain exactly one YAML document: %v", err)
	}
	if len(cases) != 8 {
		t.Fatalf("update transcript gate fixture count = %d, want 8", len(cases))
	}
	wantArms := map[string]bool{"accepted_license": false, "off_menu_license": false, "blocked_unlicense": false, "null_noop": false, "omitted_preserve": false, "off_menu_visibility": false, "group_schema_refusal": false, "shared_requires_narrowing": false}
	seenNames := map[string]bool{}
	for i, fixture := range cases {
		if fixture.Name == "" || seenNames[fixture.Name] || fixture.Body == "" || fixture.WantStatus == 0 {
			t.Fatalf("fixture %d is incomplete: %+v", i, fixture)
		}
		seenNames[fixture.Name] = true
		if _, ok := wantArms[fixture.Arm]; !ok || wantArms[fixture.Arm] {
			t.Fatalf("fixture %q has unknown or duplicate behavior arm %q", fixture.Name, fixture.Arm)
		}
		wantArms[fixture.Arm] = true
	}
	for arm, covered := range wantArms {
		if !covered {
			t.Fatalf("update transcript gate fixture is missing behavior arm %q", arm)
		}
	}
	return cases
}

func TestUpdateTranscript_LicenseAndVisibilityGate(t *testing.T) {
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatal(err)
	}
	ownerUUID := uuid.MustParse("00000000-0000-0000-0000-00000000c001")
	owner := pgtype.UUID{Bytes: ownerUUID, Valid: true}
	tid := uuid.MustParse("00000000-0000-0000-0000-00000000c002")

	for _, tc := range loadUpdateTranscriptGateCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			preLicense := pgtype.Text{}
			if tc.PreLicense != "" {
				preLicense = pgtype.Text{String: tc.PreLicense, Valid: true}
			}
			preVisibility := tc.PreVisibility
			if preVisibility == "" {
				preVisibility = dbVisibilityPrivate
			}
			var captured *sqlc.UpdateTranscriptMetadataParams
			mq := &mockQuerier{
				getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) {
					return sqlc.Transcript{ID: toPgUUID(tid), OwnerID: owner, Visibility: preVisibility, LicenseID: preLicense}, nil
				},
				getTranscriptGovernanceForUpdate: func(context.Context, pgtype.UUID) (sqlc.GetTranscriptGovernanceForUpdateRow, error) {
					return sqlc.GetTranscriptGovernanceForUpdateRow{Visibility: preVisibility, LicenseID: preLicense}, nil
				},
				updateTranscriptMetadata: func(_ context.Context, arg sqlc.UpdateTranscriptMetadataParams) (sqlc.Transcript, error) {
					captured = &arg
					return sqlc.Transcript{ID: arg.ID, OwnerID: owner, Visibility: arg.Visibility, LicenseID: arg.LicenseID, UpdatedAt: pgtype.Timestamptz{Time: time.Unix(1, 0), Valid: true}}, nil
				},
			}
			h := &Handler{queries: mq, titles: titles} // pool == nil: unit seam, no triggers
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tid.String())
			reqCtx := context.WithValue(context.Background(), UserContextKey, &AuthUser{ID: ownerUUID, Username: "gate-owner"})
			reqCtx = context.WithValue(reqCtx, chi.RouteCtxKey, rctx)
			r := httptest.NewRequest(http.MethodPatch, "/api/v1/transcripts/"+tid.String(), bytes.NewReader([]byte(tc.Body))).WithContext(reqCtx)
			w := httptest.NewRecorder()

			h.UpdateTranscript(w, r)

			if w.Code != tc.WantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.WantStatus, w.Body.String())
			}
			if tc.WantBody != "" && !strings.Contains(w.Body.String(), tc.WantBody) {
				t.Errorf("body = %q, want it to contain %q", w.Body.String(), tc.WantBody)
			}
			if tc.WantStatus != http.StatusOK && captured != nil {
				t.Errorf("rejected request reached the DB: %+v", *captured)
			}
			if tc.WantLicense != "" && (captured == nil || !captured.LicenseID.Valid || captured.LicenseID.String != tc.WantLicense) {
				t.Errorf("captured license = %+v, want %q", captured, tc.WantLicense)
			}
			if tc.WantLicenseIsNull && (captured == nil || captured.LicenseID.Valid) {
				t.Errorf("captured license = %+v, want NULL", captured)
			}
		})
	}
}
