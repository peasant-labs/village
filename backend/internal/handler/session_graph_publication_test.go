package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/session_graph_publication.yaml
var graphPublicationYAML []byte

type graphPublicationCase struct {
	Name         string `yaml:"name"`
	CountOnly    bool   `yaml:"count_only"`
	CountJSON    string `yaml:"count_json"`
	InjectedJSON string `yaml:"injected_json"`
	Accepted     bool   `yaml:"accepted"`
	Error        string `yaml:"error"`
}
type graphPublicationFixture struct {
	LongText        string                 `yaml:"long_text"`
	LongRepetitions int                    `yaml:"long_repetitions"`
	Detail          string                 `yaml:"detail"`
	Cases           []graphPublicationCase `yaml:"cases"`
}

func loadGraphPublicationFixtures(t *testing.T) graphPublicationFixture {
	t.Helper()
	var f graphPublicationFixture
	d := yaml.NewDecoder(bytes.NewReader(graphPublicationYAML))
	d.KnownFields(true)
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("trailing fixture document: %v", err)
	}
	if f.LongRepetitions <= 0 || len(f.LongText)*f.LongRepetitions < 8192 {
		t.Fatal("long-content fixture must exceed preview boundaries")
	}
	f.Detail = strings.ReplaceAll(f.Detail, "__LONG__", strings.Repeat(f.LongText, f.LongRepetitions))
	if _, err := schema.DecodeSessionDetailPayloadRaw([]byte(f.Detail)); err != nil {
		t.Fatalf("invalid base graph fixture: %v", err)
	}
	names := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || names[c.Name] || !c.Accepted && c.Error == "" {
			t.Fatalf("invalid graph case %q", c.Name)
		}
		names[c.Name] = true
	}
	for _, name := range strings.Fields("full-graph-long-history input-count-absent count-only-zero count-only-positive count-only-safe-maximum input-count-null input-count-string input-count-bool input-count-array input-count-object input-count-negative input-count-fraction input-count-overflow read-navigation-rejected read-wrapper-rejected") {
		if !names[name] {
			t.Fatalf("missing graph case %q", name)
		}
	}
	return f
}

func graphPublicationContent(t *testing.T, f graphPublicationFixture, c graphPublicationCase) []byte {
	t.Helper()
	var detail map[string]json.RawMessage
	if err := json.Unmarshal([]byte(f.Detail), &detail); err != nil {
		t.Fatal(err)
	}
	if c.CountOnly {
		delete(detail, "rootSessionId")
		delete(detail, "purpose")
		delete(detail, "relationships")
		delete(detail, "earlierHistory")
		detail["turns"] = json.RawMessage("[]")
		detail["turnCount"] = json.RawMessage("0")
	}
	delete(detail, "inputSubmissionCount")
	if c.CountJSON != "" {
		detail["inputSubmissionCount"] = json.RawMessage(c.CountJSON)
	}
	if c.InjectedJSON != "" {
		var injected map[string]json.RawMessage
		if err := json.Unmarshal([]byte(c.InjectedJSON), &injected); err != nil {
			t.Fatal(err)
		}
		for key, value := range injected {
			detail[key] = value
		}
	}
	encoded, err := json.Marshal(map[string]any{"contractVersion": "0.1.0", "kind": "session_detail", "sessionDetail": detail})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func graphPublicationMetadata(t *testing.T, content []byte, f graphPublicationFixture) []byte {
	t.Helper()
	detail, err := decodePublicationDetail(content)
	if err != nil {
		detail, err = decodePublicationDetail(graphPublicationContent(t, f, f.Cases[0]))
	}
	if err != nil {
		t.Fatal(err)
	}
	req := schema.AuthoritativePublishRequest{
		Identity:    schema.AuthoritativeSessionIdentity{SessionID: schema.SessionID(detail.ID), SchemaVersion: 11, RootSessionID: detail.RootSessionID, Purpose: detail.Purpose, Relationships: detail.Relationships},
		Model:       schema.AuthoritativeModelInfo{Harness: detail.Harness, Model: "fixture-model"},
		Timestamp:   schema.AuthoritativeTimestampInfo{Start: 1700000000000, End: 1700000001000},
		Source:      schema.AuthoritativeSourceInfo{Format: schema.SourceFormatJSON},
		Project:     schema.AuthoritativeProjectContext{Hash: testProjectHash, Name: "graph-fixture"},
		Stats:       schema.AuthoritativeSessionStats{TurnCount: detail.TurnCount, InputSubmissionCount: detail.InputSubmissionCount},
		ContentHash: schema.ComputeTranscriptContentHash(content),
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestSessionGraphTypedMigrationAndRewrite(t *testing.T) {
	f := loadGraphPublicationFixtures(t)
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			raw := graphPublicationContent(t, f, c)
			before, err := decodePublicationDetail(raw)
			if !c.Accepted {
				if err == nil || !strings.Contains(err.Error(), c.Error) {
					t.Fatalf("raw rejection=%v want %q", err, c.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			migrated, _, err := NewContentMigrator().Migrate(context.Background(), raw)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := encodeCanonicalTranscript(migrated)
			if err != nil {
				t.Fatal(err)
			}
			after, err := decodePublicationDetail(encoded)
			if err != nil {
				t.Fatal(err)
			}
			before.SchemaVersion = after.SchemaVersion
			if !reflect.DeepEqual(before, after) {
				t.Fatal("typed migration/rewrite changed durable evidence, refs, counts, or full bodies")
			}
		})
	}
}

func TestSessionGraphPublishRejectionBeforeDependencies(t *testing.T) {
	f := loadGraphPublicationFixtures(t)
	for _, c := range f.Cases {
		if c.Accepted {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			content := graphPublicationContent(t, f, c)
			metadata := graphPublicationMetadata(t, content, f)
			body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, string(content))
			r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
			r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
			r = r.WithContext(withTestUser(r.Context()))
			w := httptest.NewRecorder()
			// No database or object dependency exists: reaching either is a failure,
			// not a mocked implementation of the validation under test.
			h := newTestHandler(&mockQuerier{}, nil)
			h.PublishTranscript(w, r)
			// The harness-aware content boundary owns rejection of content it
			// cannot preserve (409) before the durable graph decode below it, so
			// every refused case states that boundary's canonical message.
			if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), c.Error) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
