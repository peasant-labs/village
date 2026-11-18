package middleware

import (
	"bytes"
	_ "embed"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/request_log.yaml
var requestLogYAML []byte

type requestLogFixture struct {
	Routes []string `yaml:"routes"`
	Cases  []struct {
		Name          string `yaml:"name"`
		Method        string `yaml:"method"`
		Target        string `yaml:"target"`
		Body          string `yaml:"body"`
		Authorization string `yaml:"authorization"`
		Want          string `yaml:"want"`
	} `yaml:"cases"`
	Forbidden []string `yaml:"forbidden"`
}

var requiredRequestLogCases = []string{
	"log-template-transcript-id",
	"log-template-segment-id-and-ordinal",
	"log-template-window-query-values",
	"log-template-username-and-hash",
	"log-template-locator-body-and-credential",
	"log-template-unmatched-root",
	"log-template-unknown-method",
	"log-template-health",
}

// TestRequestLoggerRecordsRouteTemplates mounts the production request logger
// on a chi router shaped like the API's (a root route plus an /api/v1
// subrouter) and sends requests carrying synthetic identifiers. The line must
// be exactly method, template and status, and must hold no request value.
func TestRequestLoggerRecordsRouteTemplates(t *testing.T) {
	var fixture requestLogFixture
	decoder := yaml.NewDecoder(bytes.NewReader(requestLogYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode testdata/request_log.yaml: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if seen[c.Name] {
			t.Fatalf("case %q repeats", c.Name)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredRequestLogCases {
		if !seen[name] {
			t.Fatalf("required case %q is missing from testdata/request_log.yaml", name)
		}
		delete(seen, name)
	}
	if len(seen) != 0 {
		t.Fatalf("cases outside the required-name manifest: %v", seen)
	}

	var out bytes.Buffer
	logger := log.New(&out, "", 0)
	root := chi.NewRouter()
	root.Use(RequestLogger(logger))
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	api := chi.NewRouter()
	for _, pattern := range fixture.Routes {
		switch {
		case !strings.HasPrefix(pattern, "/api/v1"):
			root.Get(pattern, ok)
		case strings.HasSuffix(pattern, "/locate"):
			api.Post(strings.TrimPrefix(pattern, "/api/v1"), ok)
		default:
			api.Get(strings.TrimPrefix(pattern, "/api/v1"), ok)
		}
	}
	root.Mount("/api/v1", api)

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			out.Reset()
			r := httptest.NewRequest(http.MethodGet, c.Target, strings.NewReader(c.Body))
			r.Method = c.Method
			if c.Authorization != "" {
				r.Header.Set("Authorization", c.Authorization)
			}
			root.ServeHTTP(httptest.NewRecorder(), r)
			line := strings.TrimSpace(out.String())
			if strings.Count(line, "\n") != 0 {
				t.Fatalf("one request wrote several lines: %q", line)
			}
			fields := strings.Fields(line)
			if len(fields) != 4 || strings.Join(fields[:3], " ") != c.Want {
				t.Fatalf("logged %q, want %q followed by a duration", line, c.Want)
			}
			for _, value := range fixture.Forbidden {
				if strings.Contains(strings.Join(fields[:3], " "), value) {
					t.Fatalf("logged line %q holds the request value %q", line, value)
				}
			}
		})
	}
}
