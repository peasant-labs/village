package handler

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/observed_model_preservation/legacy_rendered.yaml
var legacyRenderedYAML []byte

// These exact mounted responses are also consumed by the frontend fetch-hook
// tests. Source JSONL alone is not the representation browsers receive.
func TestLegacyRenderedResponseFixtures(t *testing.T) {
	var corpus struct {
		Cases []struct {
			Name     string `yaml:"name"`
			Content  string `yaml:"content"`
			Rendered string `yaml:"rendered"`
		} `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(legacyRenderedYAML))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		t.Fatal("expected one rendered fixture document")
	}
	seen := map[string]bool{}
	for _, c := range corpus.Cases {
		if c.Name == "" || seen[c.Name] || c.Content == "" || c.Rendered == "" {
			t.Fatal("invalid rendered fixture inventory")
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
			store := newFakeBlobStore()
			store.put(key, []byte(c.Content))
			h := newTestHandler(publicTranscriptQuerier(key), store)
			first := getContent(t, h, mustFixtureUUID(t))
			if first.Code != http.StatusOK {
				t.Fatalf("first read=%d %s", first.Code, first.Body.String())
			}
			var got, want any
			if err := json.Unmarshal(first.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(c.Rendered), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("mounted response differs from frontend fixture: %s", first.Body.String())
			}
			writes := store.uploadCount()
			second := getContent(t, h, mustFixtureUUID(t))
			if second.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) || store.uploadCount() != writes {
				t.Fatal("second rendered response changed bytes or rewrote storage")
			}
		})
	}
	for _, name := range []string{"array_to_empty_harness_detail", "historical_nonassistant_observation_detail"} {
		if !seen[name] {
			t.Fatalf("missing rendered fixture %q", name)
		}
	}
}
