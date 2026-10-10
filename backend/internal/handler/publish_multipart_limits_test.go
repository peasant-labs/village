package handler

import (
	"bytes"
	_ "embed"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// publishLimitMetadataBase is the shortest metadata document that passes the
// size scan and then fails the handler's sessionId precondition, so a response
// other than a size refusal proves the part reached the scan.
const publishLimitMetadataBase = `{"identity":{}}`

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

	publishLimitExpectReaches         = "reaches_metadata_scan"
	publishLimitExpectMetadataRefused = "refused_by_metadata_scan"
	publishLimitExpectRequestRefused  = "refused_by_request_cap"
)

// TestPublishTranscriptMultipartLimits pins the publish multipart boundaries on
// the real handler: the whole-request cap, the multipart parser memory budget,
// and the metadata document scan. Each fixture case states the exact byte count
// it builds and what the stack must do with it.
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

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %q", w.Code, w.Body.String())
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

	base, boundary := publishLimitMultipartTemplate(t)
	content := []byte(publishLimitMetadataBase)

	switch testCase.Kind {
	case publishLimitKindMetadata:
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
	default:
		t.Fatalf("case %q has an unhandled kind %q", testCase.Name, testCase.Kind)
		return nil, ""
	}
}

// publishLimitMultipartTemplate builds a minimal multipart body with a
// metadata field and a zero-length transcript file part. Callers splice filler
// bytes into the metadata value or the file content to reach an exact size.
func publishLimitMultipartTemplate(t *testing.T) ([]byte, string) {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	field, err := writer.CreateFormField("metadata")
	if err != nil {
		t.Fatalf("create metadata field: %v", err)
	}
	if _, err := field.Write([]byte(publishLimitMetadataBase)); err != nil {
		t.Fatalf("write metadata field: %v", err)
	}
	if _, err := writer.CreateFormFile("transcript_file", "transcript.jsonl"); err != nil {
		t.Fatalf("create transcript file part: %v", err)
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
		case publishLimitKindMetadata, publishLimitKindMultipartTotal:
		default:
			t.Fatalf("case %q kind %q is not %s or %s", testCase.Name, testCase.Kind, publishLimitKindMetadata, publishLimitKindMultipartTotal)
		}
		switch testCase.Expect {
		case publishLimitExpectReaches:
			if len(testCase.WantErrorContains) != 0 {
				t.Fatalf("case %q reaches the handler and must not pin error text", testCase.Name)
			}
		case publishLimitExpectMetadataRefused, publishLimitExpectRequestRefused:
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

	// The corpus must be able to fail: without a case that reaches the handler
	// it cannot tell a working cap from one that refuses everything, and
	// without a refused case it cannot tell a lifted cap from no cap at all.
	var sawReaches, sawRefusal bool
	for _, testCase := range fixture.Cases {
		sawReaches = sawReaches || testCase.Expect == publishLimitExpectReaches
		sawRefusal = sawRefusal || testCase.Expect != publishLimitExpectReaches
	}
	if !sawReaches || !sawRefusal {
		t.Fatal("publish multipart limit corpus must hold a case that reaches the scan and one that is refused")
	}
	return fixture
}
