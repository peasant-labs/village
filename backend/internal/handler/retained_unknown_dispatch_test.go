package handler

import (
	"bytes"
	_ "embed"
	"gopkg.in/yaml.v3"
	"io"
	"strings"
	"testing"
)

//go:embed testdata/retained_unknown_dispatch.yaml
var retainedDispatchYAML []byte

func TestRetainedUnknownDispatchKeepsNativeDataOpaque(t *testing.T) {
	var corpus struct {
		Cases []struct {
			Name    string `yaml:"name"`
			Content string `yaml:"content"`
		} `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(retainedDispatchYAML))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		t.Fatal("expected one fixture document")
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
