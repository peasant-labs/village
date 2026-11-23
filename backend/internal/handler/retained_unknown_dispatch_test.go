package handler

import (
	_ "embed"
	"strings"
	"testing"
)

//go:embed testdata/retained_unknown_dispatch.yaml
var retainedDispatchYAML []byte

func TestRetainedUnknownDispatchKeepsNativeDataOpaque(t *testing.T) {
	corpus, err := decodeFixtureDocument[struct {
		Cases []struct {
			Name    string `yaml:"name"`
			Content string `yaml:"content"`
		} `yaml:"cases"`
	}](retainedDispatchYAML)
	if err != nil {
		t.Fatalf("decode the retained dispatch fixture: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range corpus.Cases {
		if c.Name == "" || seen[c.Name] || c.Content == "" {
			t.Fatal("invalid dispatch fixture")
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			if _, err := validateContentBoundary([]byte(c.Content), "claude-code", contentPublication); err != nil {
				t.Fatal(err)
			}
			if _, err := validateContentBoundary([]byte(c.Content), "claude-code", contentStoredRead); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, name := range strings.Fields("native_message_alias_data_is_opaque native_tool_alias_data_is_opaque unrelated_native_additive_field") {
		if !seen[name] {
			t.Fatalf("missing required case %q", name)
		}
	}
}
