package handler

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

// publishLimitMetadataBase is the shortest metadata document that passes the
// size scan and then fails the handler's sessionId precondition, so a response
// other than a size refusal proves the part reached the scan.
const publishLimitMetadataBase = `{"identity":{}}`

// publishLimitTranscriptBase is a known-harness, structurally incomplete
// transcript. It forces the strict canonical content path so the transcript
// file passes the size gate and is then refused by the next validation, which
// distinguishes a size refusal from a content refusal.
const publishLimitTranscriptBase = `{"harness":"claude-code","turns":[]}`

//go:embed testdata/publish_multipart_limits.yaml
var publishMultipartLimitFixtureYAML []byte

type publishMultipartLimitFixture struct {
	RequiredNames []string                    `yaml:"required_names"`
	Cases         []publishMultipartLimitCase `yaml:"cases"`
}

type publishMultipartLimitCase struct {
	Name              string   `yaml:"name"`
	Kind              string   `yaml:"kind"`
	TargetBytes       int64    `yaml:"target_bytes"`
	Expect            string   `yaml:"expect"`
	WantErrorContains []string `yaml:"want_error_contains"`
	Why               string   `yaml:"why"`
}

const (
	publishLimitKindMetadata       = "metadata"
	publishLimitKindMultipartTotal = "multipart_total"
	publishLimitKindTranscriptFile = "transcript_file"

	publishLimitExpectReaches          = "reaches_metadata_scan"
	publishLimitExpectMetadataRefused  = "refused_by_metadata_scan"
	publishLimitExpectRequestRefused   = "refused_by_request_cap"
	publishLimitExpectTranscriptReach  = "reaches_transcript_boundary"
	publishLimitExpectTranscriptRefuse = "refused_by_transcript_boundary"
)

// TestPublishTranscriptMultipartLimits pins the publish multipart boundaries on
// the real handler: the whole-request cap, the multipart parser memory budget,
// the metadata document scan, and the transcript file content boundary. Each
// fixture case states the exact byte count it builds and what the stack must do
// with it.
func TestPublishTranscriptMultipartLimits(t *testing.T) {
	fixture := loadPublishMultipartLimitFixture(t)
	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			reader, boundary := publishLimitRequest(t, testCase)
			r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", reader)
			r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
			r = r.WithContext(withTestUser(r.Context()))
			w := httptest.NewRecorder()

			newTestHandler(&mockQuerier{}, nil).PublishTranscript(w, r)

			wantStatus := http.StatusBadRequest
			if testCase.Kind == publishLimitKindTranscriptFile {
				// The content boundary refusal is the handler's conflict answer.
				wantStatus = http.StatusConflict
			}
			if w.Code != wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", w.Code, wantStatus, w.Body.String())
			}
			body := w.Body.String()
			switch testCase.Expect {
			case publishLimitExpectReaches:
				if !strings.Contains(body, "sessionId is required") {
					t.Fatalf("case %q must reach the handler's sessionId precondition, got %q", testCase.Name, body)
				}
				if strings.Contains(body, "document exceeds") || strings.Contains(body, "Invalid multipart form") {
					t.Fatalf("case %q was refused at a size gate instead of reaching the handler: %q", testCase.Name, body)
				}
			case publishLimitExpectMetadataRefused:
				for _, want := range testCase.WantErrorContains {
					if !strings.Contains(body, want) {
						t.Fatalf("case %q metadata scan refusal missing %q: %q", testCase.Name, want, body)
					}
				}
				if strings.Contains(body, "Invalid multipart form") {
					t.Fatalf("case %q was refused before parsing, not by the metadata scan: %q", testCase.Name, body)
				}
			case publishLimitExpectRequestRefused:
				for _, want := range testCase.WantErrorContains {
					if !strings.Contains(body, want) {
						t.Fatalf("case %q request refusal missing %q: %q", testCase.Name, want, body)
					}
				}
			case publishLimitExpectTranscriptReach:
				if !strings.Contains(body, "could not be decoded") {
					t.Fatalf("case %q must read the transcript file and reach strict content validation, got %q", testCase.Name, body)
				}
				if strings.Contains(body, "document exceeds") {
					t.Fatalf("case %q was refused at the content size gate instead of reaching strict validation: %q", testCase.Name, body)
				}
			case publishLimitExpectTranscriptRefuse:
				for _, want := range testCase.WantErrorContains {
					if !strings.Contains(body, want) {
						t.Fatalf("case %q transcript boundary refusal missing %q: %q", testCase.Name, want, body)
					}
				}
			default:
				t.Fatalf("case %q has an unhandled expectation %q", testCase.Name, testCase.Expect)
			}
		})
	}
}

// publishLimitRequest builds a streaming multipart request body of exactly the
// case's target byte count, so the test itself never holds a whole
// 128 MiB/256 MiB buffer in memory.
func publishLimitRequest(t *testing.T, testCase publishMultipartLimitCase) (io.Reader, string) {
	t.Helper()

	switch testCase.Kind {
	case publishLimitKindMetadata:
		base, boundary := publishLimitMultipartTemplate(t, publishLimitMetadataBase, "")
		content := []byte(publishLimitMetadataBase)
		idx := bytes.Index(base, content)
		if idx < 0 {
			t.Fatalf("metadata template does not contain the base document")
		}
		pad := testCase.TargetBytes - int64(len(content))
		if pad < 0 {
			t.Fatalf("metadata target %d is shorter than the base document %d", testCase.TargetBytes, len(content))
		}
		return io.MultiReader(
			bytes.NewReader(base[:idx]),
			bytes.NewReader(content),
			&publishLimitFiller{remaining: pad, b: ' '},
			bytes.NewReader(base[idx+len(content):]),
		), boundary
	case publishLimitKindMultipartTotal:
		base, boundary := publishLimitMultipartTemplate(t, publishLimitMetadataBase, "")
		suffix := []byte("\r\n--" + boundary + "--\r\n")
		if !bytes.HasSuffix(base, suffix) {
			t.Fatalf("multipart template does not end with the closing boundary")
		}
		prefix := base[:len(base)-len(suffix)]
		pad := testCase.TargetBytes - int64(len(prefix)) - int64(len(suffix))
		if pad < 0 {
			t.Fatalf("request target %d is shorter than the multipart envelope %d", testCase.TargetBytes, len(prefix)+len(suffix))
		}
		return io.MultiReader(
			bytes.NewReader(prefix),
			&publishLimitFiller{remaining: pad, b: 'a'},
			bytes.NewReader(suffix),
		), boundary
	case publishLimitKindTranscriptFile:
		base, boundary := publishLimitMultipartTemplate(t, publishLimitValidMetadata(t), publishLimitTranscriptBase)
		content := []byte(publishLimitTranscriptBase)
		idx := bytes.Index(base, content)
		if idx < 0 {
			t.Fatalf("transcript template does not contain the base content")
		}
		pad := testCase.TargetBytes - int64(len(content))
		if pad < 0 {
			t.Fatalf("transcript target %d is shorter than the base content %d", testCase.TargetBytes, len(content))
		}
		return io.MultiReader(
			bytes.NewReader(base[:idx]),
			bytes.NewReader(content),
			&publishLimitFiller{remaining: pad, b: ' '},
			bytes.NewReader(base[idx+len(content):]),
		), boundary
	default:
		t.Fatalf("case %q has an unhandled kind %q", testCase.Name, testCase.Kind)
		return nil, ""
	}
}

// publishLimitValidMetadata builds metadata the handler accepts up to the
// transcript file read, so a transcript_file case reaches the content boundary
// rather than an earlier metadata refusal.
func publishLimitValidMetadata(t *testing.T) string {
	t.Helper()

	metadata := schema.PublishRequest{
		Identity:  schema.SessionIdentity{SessionID: "550e8400-e29b-41d4-a716-446655440000", SchemaVersion: 2},
		Model:     schema.ModelInfo{Harness: schema.HarnessCodex, Model: "gpt-4"},
		Timestamp: schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:    schema.SourceInfo{FilePath: "/p/t.jsonl", Format: "jsonl"},
		Project:   schema.ProjectContext{Hash: testProjectHash, Name: "test-project"},
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal publish metadata: %v", err)
	}
	return string(encoded)
}

// publishLimitMultipartTemplate builds a minimal multipart body with a metadata
// field and a transcript file part. Callers splice filler bytes into the
// metadata value or the file content to reach an exact size.
func publishLimitMultipartTemplate(t *testing.T, metadataValue, fileContent string) ([]byte, string) {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	field, err := writer.CreateFormField("metadata")
	if err != nil {
		t.Fatalf("create metadata field: %v", err)
	}
	if _, err := field.Write([]byte(metadataValue)); err != nil {
		t.Fatalf("write metadata field: %v", err)
	}
	part, err := writer.CreateFormFile("transcript_file", "transcript.jsonl")
	if err != nil {
		t.Fatalf("create transcript file part: %v", err)
	}
	if _, err := part.Write([]byte(fileContent)); err != nil {
		t.Fatalf("write transcript file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body.Bytes(), writer.Boundary()
}

// publishLimitFiller yields remaining copies of a single byte without
// allocating them, keeping the test's own memory bounded while the production
// parser streams the body.
type publishLimitFiller struct {
	remaining int64
	b         byte
}

func (r *publishLimitFiller) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = r.b
	}
	r.remaining -= int64(n)
	return n, nil
}

func loadPublishMultipartLimitFixture(t *testing.T) publishMultipartLimitFixture {
	t.Helper()

	var fixture publishMultipartLimitFixture
	decoder := yaml.NewDecoder(bytes.NewReader(publishMultipartLimitFixtureYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("publish multipart limit fixture requires exactly one YAML document; trailing decode err = %v", err)
	}
	if len(fixture.RequiredNames) == 0 {
		t.Fatal("publish multipart limit fixture has no required-name manifest")
	}

	names := map[string]bool{}
	for _, testCase := range fixture.Cases {
		if testCase.Name == "" || names[testCase.Name] {
			t.Fatalf("duplicate or empty publish multipart limit case %q", testCase.Name)
		}
		names[testCase.Name] = true
		if strings.TrimSpace(testCase.Why) == "" {
			t.Fatalf("case %q has no why", testCase.Name)
		}
		if testCase.TargetBytes <= 0 {
			t.Fatalf("case %q target_bytes must be positive", testCase.Name)
		}
		switch testCase.Kind {
		case publishLimitKindMetadata, publishLimitKindMultipartTotal, publishLimitKindTranscriptFile:
		default:
			t.Fatalf("case %q kind %q is not %s, %s or %s", testCase.Name, testCase.Kind, publishLimitKindMetadata, publishLimitKindMultipartTotal, publishLimitKindTranscriptFile)
		}
		switch testCase.Expect {
		case publishLimitExpectReaches, publishLimitExpectTranscriptReach:
			if len(testCase.WantErrorContains) != 0 {
				t.Fatalf("case %q reaches a later stage and must not pin error text", testCase.Name)
			}
		case publishLimitExpectMetadataRefused, publishLimitExpectRequestRefused, publishLimitExpectTranscriptRefuse:
			if len(testCase.WantErrorContains) == 0 {
				t.Fatalf("case %q refuses and must pin the refusal text", testCase.Name)
			}
		default:
			t.Fatalf("case %q expectation %q is unknown", testCase.Name, testCase.Expect)
		}
	}
	for _, required := range fixture.RequiredNames {
		if !names[required] {
			t.Fatalf("required-name manifest names a missing case %q", required)
		}
	}
	if len(names) != len(fixture.RequiredNames) {
		t.Fatalf("required-name manifest covers %d of %d cases; every case must be named", len(fixture.RequiredNames), len(names))
	}

	// The corpus must be able to fail: without a case that reaches a later stage
	// it cannot tell a working cap from one that refuses everything, and
	// without a refused case it cannot tell a lifted cap from no cap at all.
	var sawReaches, sawRefusal bool
	for _, testCase := range fixture.Cases {
		reaches := testCase.Expect == publishLimitExpectReaches || testCase.Expect == publishLimitExpectTranscriptReach
		sawReaches = sawReaches || reaches
		sawRefusal = sawRefusal || !reaches
	}
	if !sawReaches || !sawRefusal {
		t.Fatal("publish multipart limit corpus must hold a case that reaches a later stage and one that is refused")
	}
	return fixture
}
