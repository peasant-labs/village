package handler

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/retained_unknown_receiver.yaml
var retainedReceiverYAML []byte

type retainedReceiverCase struct {
	Name           string `yaml:"name"`
	Text           string `yaml:"text"`
	Repeat         int    `yaml:"repeat"`
	ExpectedWrites int    `yaml:"expected_writes"`
	Content        string `yaml:"-"`
}

func loadRetainedReceiverCases(t *testing.T) []retainedReceiverCase {
	t.Helper()
	cases, err := loadRetainedUnknownFixtures(retainedUnknownYAML)
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []retainedReceiverCase `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(retainedReceiverYAML))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		t.Fatal("receiver fixture must contain one document")
	}
	seen := map[string]bool{}
	for i := range corpus.Cases {
		c := &corpus.Cases[i]
		if c.Name == "" || seen[c.Name] || c.Text == "" || c.Repeat < 1 || c.Repeat > 1000000 || c.ExpectedWrites < 1 || c.ExpectedWrites > 2 {
			t.Fatalf("invalid receiver fixture %q", c.Name)
		}
		seen[c.Name] = true
		// Construct the actual publisher wire, not Go's HTML-escaped serialization.
		// The payload is a JSON string containing only these fixture-owned runes.
		if strings.ContainsAny(c.Text, "\"\\\n\r") {
			t.Fatal("receiver text requires an explicitly escaped fixture recipe")
		}
		c.Content = strings.Replace(cases[0].Content, `\"text\":\"x\"`, `\"text\":\"`+strings.Repeat(c.Text, c.Repeat)+`\"`, 1)
		if c.Content == cases[0].Content || len(c.Content) >= 8<<20 {
			t.Fatalf("fixture %q must change the payload within the actual wire limit", c.Name)
		}
		var value any
		if err := json.Unmarshal([]byte(c.Content), &value); err != nil {
			t.Fatal(err)
		}
		inflated, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if len(inflated) <= 8<<20 {
			t.Fatalf("fixture %q no longer exercises serializer expansion", c.Name)
		}
	}
	for _, name := range strings.Fields("html_payload_below_actual_transport_limit unicode_payload_below_actual_transport_limit") {
		if !seen[name] {
			t.Fatalf("required receiver fixture %q missing", name)
		}
	}
	return corpus.Cases
}

// Independent original-wire oracle: keep payload as a string and numbers as
// lexical json.Number values, without invoking a Schema decoder on the expected
// side. This detects first-decode data loss as well as rewrite loss.
func assertRetainedWire(t *testing.T, original, received []byte) {
	t.Helper()
	evidence := func(raw []byte) (any, any) {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var envelope map[string]any
		if err := d.Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		detail, ok := envelope["sessionDetail"].(map[string]any)
		if !ok {
			t.Fatal("response lacks session detail")
		}
		return detail["retainedUnknown"], detail["diagnostics"]
	}
	wantRecords, wantDiagnostics := evidence(original)
	gotRecords, gotDiagnostics := evidence(received)
	if !reflect.DeepEqual(wantRecords, gotRecords) || !reflect.DeepEqual(wantDiagnostics, gotDiagnostics) {
		t.Fatal("receiver changed original retained payload bytes, coordinates or diagnostics")
	}
	decoded, err := schema.DecodeTranscriptContentRaw(received)
	if err != nil {
		t.Fatal(err)
	}
	if !containsContentCapability(schema.RequiredContentCapabilities(*decoded.SessionDetail), schema.ContentCapabilityRetainedUnknownV1) {
		t.Fatal("receiver dropped retained evidence capability")
	}
}

func TestRetainedUnknownActualWireBudget(t *testing.T) {
	for _, c := range loadRetainedReceiverCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			// A real publication must pass both the receiver boundary and metadata
			// mirror path before any encrypted store side effect.
			raw := []byte(c.Content)
			if err := requireSupportedContentForHarness(raw, "claude-code", productionObservedModelPreservationEvaluator{}, productionProvenancePreservationEvaluator{}); err != nil {
				t.Fatal(err)
			}
			detail, err := decodePublicationDetail(raw)
			if err != nil {
				t.Fatal(err)
			}
			var metadata schema.AuthoritativePublishRequest
			if err := json.Unmarshal(retainedUnknownMetadata(t, raw), &metadata); err != nil {
				t.Fatal(err)
			}
			var legacy schema.PublishRequest
			if err := json.Unmarshal(retainedUnknownMetadata(t, raw), &legacy); err != nil {
				t.Fatal(err)
			}
			if err := validatePublicationGraphMirrors(detail, &legacy, &metadata); err != nil {
				t.Fatal(err)
			}
			assertRetainedWire(t, raw, raw)
			encoded, err := encodeCanonicalTranscript(detail)
			if c.ExpectedWrites == 2 {
				if err != nil {
					t.Fatal(err)
				}
				if len(encoded) > 8<<20 {
					t.Fatal("canonical rewrite exceeded its actual transport budget")
				}
				assertRetainedWire(t, raw, encoded)
			} else {
				if !errors.Is(err, errCanonicalRewriteTooLarge) {
					t.Fatalf("expected bounded rewrite refusal, got %v", err)
				}
				original, err := originalContentEnvelope(raw)
				if err != nil || !bytes.Equal(original, raw) {
					t.Fatalf("expanded rewrite changed original envelope: %v", err)
				}
				var envelope map[string]json.RawMessage
				if err := json.Unmarshal(raw, &envelope); err != nil {
					t.Fatal(err)
				}
				wrapped, err := originalContentEnvelope(envelope["sessionDetail"])
				if err != nil {
					t.Fatal(err)
				}
				assertRetainedWire(t, raw, wrapped)
			}
		})
	}
}
