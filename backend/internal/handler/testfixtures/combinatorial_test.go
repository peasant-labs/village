package testfixtures

import (
	"encoding/json"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

func TestValidPayloads(t *testing.T) {
	t.Parallel()

	c := jsonschema.NewCompiler()
	sch, err := c.Compile("testdata/schema.json")
	if err != nil {
		t.Fatalf("failed to compile schema: %v", err)
	}

	payloads, err := LoadValidPayloads("testdata/valid/payloads.yaml")
	if err != nil {
		t.Fatalf("failed to load valid payloads: %v", err)
	}

	for _, p := range payloads {
		p := p
		t.Run(p.Name, func(t *testing.T) {
			t.Parallel()

			b, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("failed to marshal payload: %v", err)
			}

			var v interface{}
			if err := json.Unmarshal(b, &v); err != nil {
				t.Fatalf("failed to unmarshal JSON: %v", err)
			}

			if err := sch.Validate(v); err != nil {
				t.Errorf("expected valid payload %q to pass validation, but failed: %v", p.Name, err)
			}
		})
	}
}

func TestInvalidPayloads(t *testing.T) {
	t.Parallel()

	c := jsonschema.NewCompiler()
	sch, err := c.Compile("testdata/schema.json")
	if err != nil {
		t.Fatalf("failed to compile schema: %v", err)
	}

	// We load as generic map so that omitted fields are genuinely omitted when converting to JSON.
	// Using InvalidPayload struct would serialize zero values for fields lacking omitempty.
	yamlData, err := os.ReadFile("testdata/invalid/payloads.yaml")
	if err != nil {
		t.Fatalf("failed to read invalid payloads: %v", err)
	}

	var rawPayloads []map[string]interface{}
	if err := yaml.Unmarshal(yamlData, &rawPayloads); err != nil {
		t.Fatalf("failed to unmarshal yaml: %v", err)
	}

	for _, p := range rawPayloads {
		p := p
		name := ""
		if n, ok := p["name"].(string); ok {
			name = n
		}

		expectedError := ""
		if e, ok := p["expectedError"].(string); ok {
			expectedError = e
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var jsonData []byte
			if raw, ok := p["_raw"].(string); ok {
				jsonData = []byte(raw)
			} else {
				// Remove metadata fields not part of the payload
				delete(p, "name")
				delete(p, "description")
				delete(p, "expectedError")
				// Use a local error variable: writing to the outer-scope `err`
				// from these t.Parallel() subtests is a data race.
				data, mErr := json.Marshal(p)
				if mErr != nil {
					t.Fatalf("failed to marshal generic map to JSON: %v", mErr)
				}
				jsonData = data
			}

			var v interface{}
			err := json.Unmarshal(jsonData, &v)
			if err != nil {
				if !strings.Contains(err.Error(), expectedError) {
					t.Errorf("expected error %q, got unmarshal error %q", expectedError, err.Error())
				}
				return // correctly failed at JSON parsing
			}

			err = sch.Validate(v)
			if err == nil {
				t.Errorf("expected payload %q to fail validation with error %q, but it passed", name, expectedError)
			} else {
				if !strings.Contains(err.Error(), expectedError) {
					t.Errorf("expected error containing %q, got %q", expectedError, err.Error())
				}
			}
		})
	}
}

func TestCombinatorialTestCases(t *testing.T) {
	t.Parallel()

	fixturesByCategory := map[string][]FixtureItem{
		"providers": {
			{Name: "openai", Value: "openai"},
			{Name: "anthropic", Value: "anthropic"},
		},
		"models": {
			{Name: "gpt-4", Value: "gpt-4"},
			{Name: "claude-3", Value: "claude-3"},
		},
		"outcomes": {
			{Name: "success", Value: "success"},
			{Name: "failure", Value: "failure"},
		},
	}

	categories := []string{"providers", "models", "outcomes"}
	cases := GenerateCombinatorialTestCases(categories, fixturesByCategory)

	expectedCount := 2 * 2 * 2
	if len(cases) != expectedCount {
		t.Errorf("expected %d combinations, got %d", expectedCount, len(cases))
	}

	if len(cases) > 0 {
		first := cases[0]
		if first.Name != "openai_gpt-4_success" {
			t.Errorf("expected first name to be openai_gpt-4_success, got %s", first.Name)
		}
		if len(first.Fixtures) != 3 {
			t.Errorf("expected 3 fixtures in combination, got %d", len(first.Fixtures))
		}
	}
}

func TestGeneratedCombinatorialPayloads(t *testing.T) {
	t.Parallel()

	c := jsonschema.NewCompiler()
	sch, err := c.Compile("testdata/schema.json")
	if err != nil {
		t.Fatalf("failed to compile schema: %v", err)
	}

	payloads, err := GenerateCombinatorialPayloads("testdata")
	if err != nil {
		t.Fatalf("failed to generate combinatorial payloads: %v", err)
	}

	// Shuffle and take up to 1000 random subsets
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Shuffle(len(payloads), func(i, j int) {
		payloads[i], payloads[j] = payloads[j], payloads[i]
	})

	maxTests := 1000
	if len(payloads) > maxTests {
		payloads = payloads[:maxTests]
	}

	for _, p := range payloads {
		p := p
		t.Run(p.Name, func(t *testing.T) {
			t.Parallel()

			b, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("failed to marshal payload: %v", err)
			}

			var v interface{}
			if err := json.Unmarshal(b, &v); err != nil {
				t.Fatalf("failed to unmarshal JSON: %v", err)
			}

			if err := sch.Validate(v); err != nil {
				t.Errorf("expected generated combinatorial payload %q to pass validation, but failed: %v", p.Name, err)
			}
		})
	}
}

func TestGeneratedInvalidPayloads(t *testing.T) {
	t.Parallel()

	c := jsonschema.NewCompiler()
	sch, err := c.Compile("testdata/schema.json")
	if err != nil {
		t.Fatalf("failed to compile schema: %v", err)
	}

	payloads, err := GenerateInvalidPayloads("testdata")
	if err != nil {
		t.Fatalf("failed to generate invalid payloads: %v", err)
	}

	for _, p := range payloads {
		p := p
		t.Run(p.Name, func(t *testing.T) {
			t.Parallel()

			var jsonData []byte
			var err error

			if p.Raw != "" {
				jsonData = []byte(p.Raw)
			} else {
				// We need to marshal only the fields that belong to the request.
				rawJson, _ := json.Marshal(p)
				var m map[string]interface{}
				json.Unmarshal(rawJson, &m)
				delete(m, "name")
				delete(m, "description")
				delete(m, "expectedError")

				jsonData, err = json.Marshal(m)
				if err != nil {
					t.Fatalf("failed to marshal payload map to JSON: %v", err)
				}
			}

			var v interface{}
			err = json.Unmarshal(jsonData, &v)
			if err != nil {
				// Expected to fail unmarshaling
				if !strings.Contains(err.Error(), p.ExpectedError) && p.ExpectedError != "invalid character" {
					t.Errorf("expected unmarshal error containing %q, got %q", p.ExpectedError, err.Error())
				}
				return
			}

			err = sch.Validate(v)

			// For SQL injections and XSS in model field, it passes schema because schema just requires string.
			if p.ExpectedError == "injection payload" {
				if err != nil {
					t.Errorf("expected payload %q to pass JSON schema (since it only requires a string) and fail app validation later, but it failed schema with: %v", p.Name, err)
				}
				t.Logf("Note: Payload %q passed JSON schema validation (as expected for strings), but should fail application validation.", p.Name)
			} else if p.ExpectedError == "invalid character" || p.ExpectedError == "unexpected end of JSON input" {
				// We expected it to fail JSON unmarshaling, but if it successfully parsed (e.g., valid JSON but wrong shape),
				// it must fail schema validation instead.
				if err == nil {
					t.Errorf("expected payload %q to fail JSON parsing or schema validation, but it passed", p.Name)
				}
			} else {
				if err == nil {
					t.Errorf("expected payload %q to fail schema validation with error %q, but it passed", p.Name, p.ExpectedError)
				} else {
					if !strings.Contains(err.Error(), p.ExpectedError) {
						t.Errorf("expected error containing %q, got %q", p.ExpectedError, err.Error())
					}
				}
			}
		})
	}
}
