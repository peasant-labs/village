package main

import (
	"bytes"
	_ "embed"
	"strings"
	"testing"

	"github.com/peasant-labs/village/backend/internal/config"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/runtime-repair.yaml
var runtimeRepairFixture []byte

func TestRuntimeRepairSelection(t *testing.T) {
	var fixture struct {
		Base  []string `yaml:"base"`
		Cases []struct {
			Name    string   `yaml:"name"`
			Args    []string `yaml:"args"`
			Bare    bool     `yaml:"bare"`
			Success bool     `yaml:"success"`
			Error   string   `yaml:"error"`
		} `yaml:"cases"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(runtimeRepairFixture))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"clear-selects-blob-authority": false, "declare-selects-blob-authority": false,
		"restore-selects-blob-authority": false, "restore-needs-source": false,
		"repair-disallows-other-mode": false, "invalid-actor-refused": false,
		"standalone-repair-argument-refused": false,
	}
	for _, c := range fixture.Cases {
		if _, known := required[c.Name]; !known || required[c.Name] {
			t.Fatalf("unknown or duplicate repair runtime case %q", c.Name)
		}
		required[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			var args []string
			if !c.Bare {
				args = append(args, fixture.Base...)
			}
			args = append(args, c.Args...)
			selected, err := parseRuntimeSelection(args)
			if c.Success {
				if err != nil || selected.Mode() != runtimeModeContentRepair || selected.AuthorityRequirements() != config.BlobProcessingAuthority {
					t.Fatalf("repair mode not selected with blob authority: mode=%v authority=%v err=%v", selected.Mode(), selected.AuthorityRequirements(), err)
				}
			} else if err == nil || !strings.Contains(err.Error(), c.Error) {
				t.Fatalf("want validation error containing %q, got %v", c.Error, err)
			}
		})
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("missing repair runtime fixture %q", name)
		}
	}
}
