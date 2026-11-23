package contentrepair

import (
	"bytes"
	_ "embed"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/options.yaml
var optionsFixtureYAML []byte

type optionsFixtureFields struct {
	RepairID          *string `yaml:"repair_id"`
	TranscriptID      *string `yaml:"transcript_id"`
	ActorID           *string `yaml:"actor_id"`
	Action            *string `yaml:"action"`
	AuthorizationRef  *string `yaml:"authorization_ref"`
	Reason            *string `yaml:"reason"`
	ExpectedRevision  *int64  `yaml:"expected_revision"`
	SourceKey         *string `yaml:"source_key"`
	SourceSupplied    bool    `yaml:"source_supplied"`
	Reconciles        *string `yaml:"reconciles"`
	ReconcileSupplied bool    `yaml:"reconcile_supplied"`
}

type optionsFixture struct {
	Defaults optionsFixtureFields `yaml:"defaults"`
	Cases    []struct {
		Name                 string `yaml:"name"`
		optionsFixtureFields `yaml:",inline"`
		ErrorFlag            string `yaml:"error_flag"`
	} `yaml:"cases"`
}

var requiredOptionCases = []string{
	"clear-valid", "replacement-valid", "declaration-valid", "restore-valid",
	"restore-requires-source", "declaration-refuses-source", "source-must-be-opaque",
	"reserved-actor-refused", "nil-transcript-refused", "raw-ledger-action-refused",
	"unknown-action-refused", "empty-reason-refused", "revision-zero-refused", "invalid-citation-refused",
}

func TestParseOptions(t *testing.T) {
	var fixture optionsFixture
	decoder := yaml.NewDecoder(bytes.NewReader(optionsFixtureYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	str := func(value, fallback *string) string {
		if value != nil {
			return *value
		}
		if fallback == nil {
			return ""
		}
		return *fallback
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("empty or repeated repair option case %q", c.Name)
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			rev := fixture.Defaults.ExpectedRevision
			if c.ExpectedRevision != nil {
				rev = c.ExpectedRevision
			}
			if rev == nil {
				t.Fatal("fixture lacks revision")
			}
			raw := RawOptions{
				RepairID: str(c.RepairID, fixture.Defaults.RepairID), TranscriptID: str(c.TranscriptID, fixture.Defaults.TranscriptID),
				ActorID: str(c.ActorID, fixture.Defaults.ActorID), Action: str(c.Action, fixture.Defaults.Action),
				AuthorizationRef: str(c.AuthorizationRef, fixture.Defaults.AuthorizationRef), Reason: str(c.Reason, fixture.Defaults.Reason),
				ExpectedRevision: *rev, SourceSupplied: c.SourceSupplied, SourceKey: str(c.SourceKey, nil),
				ReconcileSupplied: c.ReconcileSupplied, Reconciles: str(c.Reconciles, nil),
			}
			got, err := ParseOptions(raw)
			if c.ErrorFlag != "" {
				if err == nil || !strings.Contains(err.Error(), c.ErrorFlag) {
					t.Fatalf("want flag %s error, got %v", c.ErrorFlag, err)
				}
				if raw.SourceKey != "" && strings.Contains(err.Error(), raw.SourceKey) {
					t.Fatal("error exposed source key")
				}
				return
			}
			if err != nil || !got.Action.IsValid() || got.AuthorizationRef != "authorized" || got.Reason != "repair" {
				t.Fatalf("valid repair options rejected or not normalized: %v", err)
			}
		})
	}
	for _, name := range requiredOptionCases {
		if !seen[name] {
			t.Fatalf("missing repair option fixture %q", name)
		}
	}
}
