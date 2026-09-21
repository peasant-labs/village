package scanner

import (
	"bytes"
	_ "embed"
	"gopkg.in/yaml.v3"
	"io"
	"slices"
	"strings"
	"testing"
)

//go:embed testdata/safe_diagnostics.yaml
var safeDiagnosticsYAML []byte

func TestSecretDiagnosticsContainRulesNotPayload(t *testing.T) {
	var corpus struct {
		Cases []struct {
			Name      string   `yaml:"name"`
			Content   string   `yaml:"content"`
			Rules     []string `yaml:"rules"`
			Findings  []string `yaml:"findings"`
			Forbidden []string `yaml:"forbidden"`
			Message   string   `yaml:"message"`
		} `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(safeDiagnosticsYAML))
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
		if c.Name == "" || seen[c.Name] || len(c.Forbidden) == 0 || c.Message == "" {
			t.Fatal("invalid diagnostic fixture")
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			findings := c.Findings
			if c.Content != "" {
				findings = ScanForSecrets([]byte(c.Content))
				if !slices.Equal(findings, c.Rules) {
					t.Fatalf("rule membership=%v want=%v", findings, c.Rules)
				}
			}
			message := FormatScanErrors(findings)
			if !strings.Contains(message, c.Message) || !strings.HasPrefix(message, "Redaction check failed. Potential secrets detected:") {
				t.Fatal("scanner-specific remediation missing")
			}
			for _, private := range c.Forbidden {
				if strings.Contains(message, private) {
					t.Fatal("private data leaked into outward scanner message")
				}
				if c.Content != "" && strings.Contains(strings.Join(findings, " "), private) {
					t.Fatal("private data leaked into scanner finding")
				}
			}
		})
	}
	for _, name := range strings.Fields("raw_credential_returns_rule_only assignment_returns_rule_only unexpected_finding_cannot_inject_diagnostics") {
		if !seen[name] {
			t.Fatalf("missing diagnostic fixture %q", name)
		}
	}
}
