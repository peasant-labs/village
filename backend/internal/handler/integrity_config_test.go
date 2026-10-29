package handler

import (
	_ "embed"
	"strings"
	"testing"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/scry"
)

//go:embed testdata/scry-configuration.yaml
var scryConfigurationYAML []byte

type scryConfigurationCase struct {
	Name     string   `yaml:"name"`
	URL      string   `yaml:"url"`
	Secret   string   `yaml:"secret"`
	Enabled  bool     `yaml:"enabled"`
	Problems []string `yaml:"problems"`
}

var requiredScryConfigurationCases = []string{
	"both_unset_is_off_quietly", "url_only_is_off", "secret_only_is_off", "short_secret_is_off",
	"bad_scheme_is_off", "both_set_is_on", "problems_name_only_variables",
}

// TestScryConfiguration proves the integrity check turns on only with both
// SCRY_* variables valid, and that the reasons it names are variables only.
func TestScryConfiguration(t *testing.T) {
	cases, err := decodeFixtureRows[scryConfigurationCase](scryConfigurationYAML)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		present[c.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "scry-configuration", present, requiredScryConfigurationCases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			svc := newIntegrityService(&config.Config{ScryURL: c.URL, ScrySharedSecret: c.Secret})
			if (svc != nil) != c.Enabled {
				t.Fatalf("integrity enabled = %v, want %v", svc != nil, c.Enabled)
			}
			if c.Enabled && svc.checker == nil {
				t.Fatal("enabled service has no checker")
			}
			problems := scry.Settings{URL: c.URL, SharedSecret: c.Secret}.Problems()
			if len(problems) != len(c.Problems) {
				t.Fatalf("problems = %v, want variables %v", problems, c.Problems)
			}
			for i, want := range c.Problems {
				if !strings.HasPrefix(problems[i], want+" ") {
					t.Fatalf("problem %q does not start with %s", problems[i], want)
				}
				if strings.Contains(problems[i], "SECRETVALUE") {
					t.Fatalf("problem %q echoes a value", problems[i])
				}
			}
		})
	}
}
