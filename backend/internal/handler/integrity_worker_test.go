package handler

import (
	_ "embed"
	"os"
	"regexp"
	"strings"
	"testing"
)

//go:embed testdata/integrity-versions.yaml
var integrityVersionsYAML []byte

type integrityVersionCase struct {
	Name     string `yaml:"name"`
	Why      string `yaml:"why"`
	Current  string `yaml:"current"`
	Source   string `yaml:"source"`
	Observed string `yaml:"observed"`
	Want     string `yaml:"want"`
}

var requiredIntegrityVersionCases = []string{
	"first_health_sets", "health_moves_back", "newer_answer_moves_forward", "answer_order_is_numeric",
	"older_answer_does_not_rewind", "same_answer_keeps", "first_answer_sets",
}

func TestIntegrityVersionTracking(t *testing.T) {
	cases, err := decodeFixtureRows[integrityVersionCase](integrityVersionsYAML)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		present[c.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "integrity-versions", present, requiredIntegrityVersionCases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			s := newIntegrityServiceFor(nil)
			if c.Current != "" {
				s.setVersion(c.Current)
			}
			switch c.Source {
			case "health":
				s.setVersion(c.Observed)
			case "answer":
				s.observeVersion(c.Observed)
			default:
				t.Fatalf("unknown source %q", c.Source)
			}
			got, ok := s.currentVersion()
			if !ok || got != c.Want {
				t.Fatalf("version = %q (known %v), want %q (%s)", got, ok, c.Want, c.Why)
			}
		})
	}
}

// TestCheckedSetPredicateIsShared keeps the membership test the worker runs
// before each read the same SQL text as the candidate list, so the two cannot
// disagree about which transcripts others can read.
func TestCheckedSetPredicateIsShared(t *testing.T) {
	raw, err := os.ReadFile("../database/queries/integrity.sql")
	if err != nil {
		t.Fatal(err)
	}
	const predicate = "(t.visibility = 'public' OR EXISTS (SELECT 1 FROM transcript_shares s WHERE s.transcript_id = t.id))"
	queries := map[string]string{}
	for _, block := range regexp.MustCompile(`(?m)^-- name: `).Split(string(raw), -1)[1:] {
		name, body, _ := strings.Cut(block, " ")
		queries[name] = body
	}
	for _, name := range []string{"ListIntegrityCandidates", "IsInIntegrityCheckedSet"} {
		body, ok := queries[name]
		if !ok {
			t.Fatalf("query %s is missing", name)
		}
		if strings.Count(body, predicate) != 1 {
			t.Fatalf("query %s does not carry the checked-set predicate exactly once", name)
		}
	}
}
