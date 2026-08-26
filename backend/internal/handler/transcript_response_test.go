package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
)

//go:embed testdata/transcript_response/contract.yaml
var transcriptResponseContractYAML []byte

type transcriptResponseContract struct {
	AllowedFields         []string                      `yaml:"allowed_fields"`
	DeniedFields          []string                      `yaml:"denied_fields"`
	FutureInternalFields  []string                      `yaml:"future_internal_fields"`
	MountedPaths          []mountedPathFixture          `yaml:"mounted_paths"`
	OwnerFields           []string                      `yaml:"owner_fields"`
	OwnerValues           map[string]any                `yaml:"owner_values"`
	SourceRow             map[string]any                `yaml:"source_row"`
	ResolvedProjectFields map[string]string             `yaml:"resolved_project_fields"`
	ConditionalValidators []conditionalValidatorFixture `yaml:"conditional_validators"`
}

type mountedPathFixture struct {
	Name            string `yaml:"name"`
	Shape           string `yaml:"shape"`
	ResolvesProject bool   `yaml:"resolves_project"`
}

// requiredResolvedProjectFields names the resolved identity fields the wire must
// carry. They are named rather than counted because each one exists for its own
// reason: the display name is what a surface renders, the source is what lets an
// inferred label be styled differently from an owner-chosen one, and the remote
// label is the repository subtitle. Losing any one of them silently changes what
// a client can show.
var requiredResolvedProjectFields = []string{
	"project_display_name",
	"project_name_source",
	"project_remote_label",
}

// resolvedProjectFixture turns the fixture's declared resolved values into the
// production resolver result the mounted composition points take.
func resolvedProjectFixture(t *testing.T, fixture transcriptResponseContract) projectname.Resolved {
	t.Helper()
	for _, field := range requiredResolvedProjectFields {
		if fixture.ResolvedProjectFields[field] == "" {
			t.Fatalf("the response contract declares no value for resolved field %q. That field exists because a client "+
				"renders it; restore it rather than removing it from this manifest.", field)
		}
	}
	return projectname.Resolved{
		DisplayName: fixture.ResolvedProjectFields["project_display_name"],
		Source:      projectname.NameSource(fixture.ResolvedProjectFields["project_name_source"]),
		RemoteLabel: fixture.ResolvedProjectFields["project_remote_label"],
	}
}

func TestPullAuthorizationPrecedesConditionalResponse(t *testing.T) {
	fixture := loadTranscriptResponseContract(t)
	caller, owner, transcriptID := uuid.New(), uuid.New(), uuid.New()
	for _, validator := range fixture.ConditionalValidators {
		if !validator.Matches {
			continue
		}
		t.Run(validator.Value, func(t *testing.T) {
			q := &mockQuerier{getTranscriptByID: func(_ context.Context, _ pgtype.UUID) (sqlc.Transcript, error) {
				row := pullTestTranscript(transcriptID, owner, dbVisibilityPrivate)
				row.ContentHash = pgText("fixture-hash")
				return row, nil
			}}
			h := newTestHandler(q, &fixedTranscriptBlobStore{body: "must not be served"})
			r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+transcriptID.String()+"/content", nil)
			r.Header.Set("If-None-Match", validator.Value)
			r = r.WithContext(withUserID(r.Context(), caller))
			r = withChiURLParam(r, "id", transcriptID.String())
			w := httptest.NewRecorder()
			h.GetPullTranscriptContent(w, r)
			if w.Code != http.StatusNotFound || w.Header().Get("ETag") != "" {
				t.Fatalf("unauthorized conditional request status/ETag = %d/%q; want 404 with no ETag", w.Code, w.Header().Get("ETag"))
			}
		})
	}
}

type conditionalValidatorFixture struct {
	Value   string `yaml:"value"`
	Matches bool   `yaml:"matches"`
}

func loadTranscriptResponseContract(t *testing.T) transcriptResponseContract {
	t.Helper()
	var fixture transcriptResponseContract
	decoder := yaml.NewDecoder(bytes.NewReader(transcriptResponseContractYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode transcript response contract fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("transcript response contract must contain exactly one YAML document; got %v", err)
	}
	if len(fixture.AllowedFields) != 62 || len(fixture.DeniedFields) != 4 || len(fixture.FutureInternalFields) != 3 || len(fixture.MountedPaths) != 5 || len(fixture.OwnerFields) != 3 || len(fixture.ConditionalValidators) != 4 {
		t.Fatalf("transcript response contract counts = allowed %d, denied %d, paths %d, owner %d, validators %d; want 62/4/5/3/4", len(fixture.AllowedFields), len(fixture.DeniedFields), len(fixture.MountedPaths), len(fixture.OwnerFields), len(fixture.ConditionalValidators))
	}
	return fixture
}

func TestTranscriptResponseContractExact(t *testing.T) {
	fixture := loadTranscriptResponseContract(t)
	sourceJSON, err := json.Marshal(fixture.SourceRow)
	if err != nil {
		t.Fatalf("marshal transcript source fixture: %v", err)
	}
	var source sqlc.Transcript
	if err := json.Unmarshal(sourceJSON, &source); err != nil {
		t.Fatalf("decode transcript source fixture into production row: %v", err)
	}
	want := make(map[string]bool, len(fixture.AllowedFields))
	for _, field := range fixture.AllowedFields {
		want[field] = true
	}
	resolved := resolvedProjectFixture(t, fixture)
	for _, mountedPath := range fixture.MountedPaths {
		t.Run(mountedPath.Name, func(t *testing.T) {
			var responseValue any
			switch mountedPath.Name {
			case "publish":
				responseValue = publishTranscriptResponse(source)
			case "detail":
				responseValue = detailTranscriptResponse(source, resolved)
			case "update":
				responseValue = updateTranscriptResponse(source)
			case "list":
				responseValue = listTranscriptResponse(source, resolved)
			case "public_group":
				groupSource := make(map[string]any, len(fixture.SourceRow)+len(fixture.OwnerValues))
				for field, value := range fixture.SourceRow {
					groupSource[field] = value
				}
				for field, value := range fixture.OwnerValues {
					groupSource[field] = value
				}
				groupJSON, err := json.Marshal(groupSource)
				if err != nil {
					t.Fatalf("marshal public-group source fixture: %v", err)
				}
				var groupRow sqlc.ListGroupTranscriptsRow
				if err := json.Unmarshal(groupJSON, &groupRow); err != nil {
					t.Fatalf("decode public-group fixture into production query row: %v", err)
				}
				responseValue = groupTranscriptFromRow(groupRow)
			default:
				t.Fatalf("unknown mounted response fixture path %q; add its production composition explicitly", mountedPath.Name)
			}
			assertMountedResponseValues(t, fixture, mountedPath, responseValue, want)
		})
	}
}

func assertMountedResponseValues(t *testing.T, fixture transcriptResponseContract, mountedPath mountedPathFixture, responseValue any, want map[string]bool) {
	t.Helper()
	payload, err := json.Marshal(responseValue)
	if err != nil {
		t.Fatalf("marshal %s production response composition: %v", mountedPath.Name, err)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("decode %s production response composition: %v", mountedPath.Name, err)
	}
	wantCount := len(want) + len(requiredResolvedProjectFields)
	if mountedPath.Shape == "flat_group" {
		wantCount += len(fixture.OwnerFields)
	}
	if len(response) != wantCount {
		t.Fatalf("%s response has %d fields; want %d", mountedPath.Name, len(response), wantCount)
	}
	for _, field := range fixture.AllowedFields {
		got, ok := response[field]
		if !ok {
			t.Errorf("%s response omitted allowed field %q", mountedPath.Name, field)
			continue
		}
		wantJSON, err := json.Marshal(fixture.SourceRow[field])
		if err != nil {
			t.Fatalf("marshal source value for %s: %v", field, err)
		}
		if !bytes.Equal(got, wantJSON) {
			t.Errorf("%s field %q = %s; want source-distinguishing value %s", mountedPath.Name, field, got, wantJSON)
		}
	}
	for _, field := range requiredResolvedProjectFields {
		got, ok := response[field]
		if !ok {
			t.Errorf("%s response omitted resolved project field %q", mountedPath.Name, field)
			continue
		}
		wantValue := ""
		if mountedPath.ResolvesProject {
			wantValue = fixture.ResolvedProjectFields[field]
		}
		wantJSON, err := json.Marshal(wantValue)
		if err != nil {
			t.Fatalf("marshal resolved project value for %s: %v", field, err)
		}
		if !bytes.Equal(got, wantJSON) {
			t.Errorf("%s field %q = %s; want %s", mountedPath.Name, field, got, wantJSON)
		}
	}
	for _, field := range fixture.DeniedFields {
		if _, ok := response[field]; ok {
			t.Errorf("central response exposed denied field %q", field)
		}
	}
	for _, field := range fixture.FutureInternalFields {
		if _, ok := response[field]; ok {
			t.Errorf("%s response exposed future internal field %q", mountedPath.Name, field)
		}
	}
	for _, field := range fixture.OwnerFields {
		got, present := response[field]
		if mountedPath.Shape != "flat_group" {
			if present {
				t.Errorf("%s nested response unexpectedly exposed group owner field %q", mountedPath.Name, field)
			}
			continue
		}
		if !present {
			t.Errorf("%s group response omitted owner field %q", mountedPath.Name, field)
			continue
		}
		wantJSON, err := json.Marshal(fixture.OwnerValues[field])
		if err != nil {
			t.Fatalf("marshal owner source value for %s: %v", field, err)
		}
		if !bytes.Equal(got, wantJSON) {
			t.Errorf("%s owner field %q = %s; want %s", mountedPath.Name, field, got, wantJSON)
		}
	}
	for field := range response {
		if !want[field] && !containsString(fixture.OwnerFields, field) && !containsString(requiredResolvedProjectFields, field) {
			t.Errorf("%s response exposed unexpected field %q", mountedPath.Name, field)
		}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestConditionalValidatorFixture(t *testing.T) {
	fixture := loadTranscriptResponseContract(t)
	for _, validator := range fixture.ConditionalValidators {
		if got := ifNoneMatchMatches(validator.Value, "fixture-hash"); got != validator.Matches {
			t.Errorf("validator %q matched = %v; want %v", validator.Value, got, validator.Matches)
		}
	}
}
