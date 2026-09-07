package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/observed_model_preservation/dispatch_mutations.yaml
var dispatchMutationYAML []byte

func TestContentBoundaryMutationsWithholdCapabilities(t *testing.T) {
	var corpus struct {
		Cases []struct {
			Name      string `yaml:"name"`
			Target    string `yaml:"target"`
			Operation string `yaml:"operation"`
		} `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(dispatchMutationYAML))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		t.Fatal("expected one mutation fixture document")
	}
	seen := map[string]bool{}
	for _, mutation := range corpus.Cases {
		if mutation.Name == "" || seen[mutation.Name] {
			t.Fatal("missing or duplicated mutation name")
		}
		seen[mutation.Name] = true
		t.Run(mutation.Name, func(t *testing.T) {
			var target *legacyDispatchCase
			for _, c := range loadLegacyDispatch(t) {
				if c.Name == mutation.Target {
					target = &c
					break
				}
			}
			if target == nil {
				t.Fatal("mutation target missing")
			}
			executions := 0
			mutated := func(raw []byte, known string, mode contentBoundaryMode) (contentBoundary, error) {
				if string(raw) != target.Content || known != target.Context {
					return validateContentBoundary(raw, known, mode)
				}
				executions++
				switch mutation.Operation {
				case "remove_usage":
					return validateContentBoundary(bytes.Replace(raw, []byte(`"usage":null`), []byte(`"content":"kept"`), 1), known, mode)
				case "ignore_context":
					return validateContentBoundary(raw, "", mode)
				case "bypass_validation":
					return contentBoundary{}, nil
				case "fallback_on_error":
					result, err := validateContentBoundary(raw, known, mode)
					if err != nil {
						return contentBoundary{}, nil
					}
					return result, nil
				default:
					t.Fatalf("unknown mutation %q", mutation.Operation)
					return contentBoundary{}, nil
				}
			}
			proofErr := proveContentBoundary(mutated)
			if proofErr == nil || executions == 0 || !strings.Contains(proofErr.Error(), target.Name) {
				t.Fatalf("mutation not caught at its production proof case: executions=%d err=%v", executions, proofErr)
			}
			h := newTestHandler(&mockQuerier{}, newFakeBlobStore())
			h.preservationEvaluator = fixedPreservationEvaluator{err: proofErr}
			w := httptest.NewRecorder()
			h.GetSchemaVersion(w, httptest.NewRequest(http.MethodGet, "/api/v1/schema/version", nil))
			var version schema.SchemaVersionResponse
			if err := json.Unmarshal(w.Body.Bytes(), &version); err != nil {
				t.Fatal(err)
			}
			if len(version.ContentCapabilities) != 0 {
				t.Fatal("bypassed validation advertised preservation")
			}
			raw := piContent(t)
			refused := publishPiParts(t, h, GetUser(withTestUser(context.Background())), piMetadata(t, raw), raw)
			if refused.Code != http.StatusConflict {
				t.Fatalf("publish=%d %s", refused.Code, refused.Body.String())
			}
		})
	}
	for _, name := range strings.Fields("removed_usage_discriminator bypassed_strict_parser bypassed_known_pi_context strict_failure_falls_back") {
		if !seen[name] {
			t.Fatalf("required mutation %q missing", name)
		}
	}
}
