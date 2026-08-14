package handler

import (
	_ "embed"
	"strings"
	"testing"
)

// discoveryOrderCase pins one sort mode to the exact ORDER BY clause the
// discovery listing must build for it. Cases live in a YAML fixture so a sort
// change updates one file, not an inline table.
type discoveryOrderCase struct {
	Name       string `yaml:"name"`
	Sort       string `yaml:"sort"`
	WantClause string `yaml:"wantClause"`
}

//go:embed testdata/discovery_pagination/order_clauses.yaml
var discoveryOrderClausesYAML []byte

// TestDiscoveryOrderClause_FixtureBacked proves the discovery ORDER BY builder
// maps every sort mode to its exact deterministic clause AND that every clause
// ends with the unique primary-key tie-breaker t.id DESC. The suffix assertion
// is the non-vacuity guard: dropping the tie-breaker (the ambiguous-order bug)
// makes both the exact-match and the suffix check fail.
func TestDiscoveryOrderClause_FixtureBacked(t *testing.T) {
	cases := loadFixtureRows[discoveryOrderCase](t, discoveryOrderClausesYAML, 5)

	seen := make(map[string]bool, len(cases))
	for _, c := range cases {
		if c.Name == "" {
			t.Fatalf("fixture case is missing a name; every ORDER BY case must be named")
		}
		if seen[c.Name] {
			t.Fatalf("duplicate fixture case name %q; case names must be unique", c.Name)
		}
		seen[c.Name] = true

		got := discoveryOrderClause(c.Sort)
		if got != c.WantClause {
			t.Errorf("case %q: discoveryOrderClause(%q) = %q, want %q", c.Name, c.Sort, got, c.WantClause)
		}
		if !strings.HasSuffix(got, "t.id DESC") {
			t.Errorf("case %q: clause %q must end with the unique t.id DESC tie-breaker so tied rows order deterministically", c.Name, got)
		}
	}
}
