package handler

// The publish boundary refuses a transcript that does not say which project it
// belongs to. See testdata/publish-project-hash.yaml for why each case is there.

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/publish-project-hash.yaml
var publishProjectHashYAML []byte

type publishProjectHashCase struct {
	Name string `yaml:"name"`
	Why  string `yaml:"why"`
	// Metadata is the RAW publish metadata body. It is raw JSON rather than a
	// marshalled schema.PublishRequest because the case that matters most omits
	// the project object entirely, and a Go struct always emits its fields.
	Metadata               string            `yaml:"metadata"`
	WantStatus             int               `yaml:"want_status"`
	WantActionableElements map[string]string `yaml:"want_actionable_elements"`
}

// requiredPublishProjectHashCases names the cases that must exist. The first is
// the only non-vacuous one - the single hole the published contract still permits
// - and the other two bracket it: one proves the older contract rejection still
// holds, the other proves the guard refuses a missing identity rather than
// refusing publishes in general.
var requiredPublishProjectHashCases = []string{
	"project_object_absent_entirely",
	"project_object_present_without_hash",
	"project_object_present_with_hash_is_accepted",
}

// requiredActionableElements are the six things a refusal must tell its reader.
// A refusal that names fewer leaves the publisher guessing at what to do next.
var requiredActionableElements = []string{"what", "why", "where", "when", "meaning", "fix"}

func loadPublishProjectHashCases(t *testing.T) []publishProjectHashCase {
	t.Helper()
	cases, err := decodeFixtureRows[publishProjectHashCase](publishProjectHashYAML)
	if err != nil {
		t.Fatalf("load the publish project-hash fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if present[c.Name] {
			t.Fatalf("the publish project-hash fixture repeats case %q", c.Name)
		}
		present[c.Name] = true
		if c.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", c.Name)
		}
		if c.Metadata == "" {
			t.Fatalf("case %q carries no publish metadata, so it drives nothing", c.Name)
		}
		if len(c.WantActionableElements) == 0 {
			continue
		}
		for _, element := range requiredActionableElements {
			if strings.TrimSpace(c.WantActionableElements[element]) == "" {
				t.Fatalf("case %q asserts an actionable refusal but names no %q element. All six of %v are required; "+
					"restore it rather than removing it from this case.", c.Name, element, requiredActionableElements)
			}
		}
	}
	for _, required := range requiredPublishProjectHashCases {
		if !present[required] {
			t.Fatalf("the publish project-hash fixture no longer contains %q. That case exists because losing it hides a "+
				"real failure; restore it rather than removing it from this manifest.", required)
		}
	}
	return cases
}

func TestPublishTranscript_ProjectIdentityRequired(t *testing.T) {
	if payloadValidator() == nil {
		t.Skip("contract validator unavailable; the publish path cannot be exercised")
	}
	for _, testCase := range loadPublishProjectHashCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			mq := &mockQuerier{
				getTranscriptIDByOwnerAndLocalID: func(_ context.Context, _ sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
					return pgtype.UUID{}, errors.New("not found")
				},
				createTranscript: func(_ context.Context, _ sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
					return sqlc.Transcript{}, nil
				},
			}
			h := newTestHandler(mq, &mockTranscriptBlobStore{})

			body, boundary := multipartBody(t, map[string]string{"metadata": testCase.Metadata},
				`[{"role":"user","content":"hello"}]`)
			r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
			r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
			r = r.WithContext(withTestUser(r.Context()))
			w := httptest.NewRecorder()

			h.PublishTranscript(w, r)

			if w.Code != testCase.WantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, testCase.WantStatus, w.Body.String())
			}
			if len(testCase.WantActionableElements) == 0 {
				return
			}

			var refusal struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil {
				t.Fatalf("decode the refusal body %q: %v", w.Body.String(), err)
			}
			for _, element := range requiredActionableElements {
				want := testCase.WantActionableElements[element]
				if !strings.Contains(refusal.Error, want) {
					t.Errorf("the refusal states no %s: it does not contain %q.\nrefusal: %s", element, want, refusal.Error)
				}
			}
		})
	}
}
