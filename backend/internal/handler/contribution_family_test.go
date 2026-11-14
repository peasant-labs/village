package handler

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

//go:embed testdata/contribution-family-fragments.yaml
var contributionFamilyFragmentsYAML []byte

type contributionFamilyFragmentCase struct {
	Name      string `yaml:"name"`
	File      string `yaml:"file"`
	Statement string `yaml:"statement"`
	Kind      string `yaml:"kind"`
	Alias     string `yaml:"alias"`
	A         string `yaml:"a"`
	B         string `yaml:"b"`
}

var requiredContributionFamilyFragments = []string{
	"picker_projects_root_condition",
	"picker_counts_root_condition",
	"picker_counts_member_match",
	"picker_counts_root_name_match",
	"picker_counts_provider_match",
	"picker_counts_latest_attempt",
	"picker_counts_live",
	"picker_facets_root_condition",
	"picker_facets_member_match",
	"picker_facets_root_name_match",
	"paged_root_condition",
	"paged_member_match",
	"paged_root_name_match",
	"paged_provider_match",
	"paged_latest_attempt",
	"paged_live",
}

func squeezeSQL(s string) string { return strings.Join(strings.Fields(s), "") }

// statementOf returns the text of one named sqlc statement, comments included.
func statementOf(t *testing.T, file, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "database", "queries", file))
	if err != nil {
		t.Fatal(err)
	}
	marker := "-- name: " + name + " "
	text := string(raw)
	start := strings.Index(text, marker)
	if start < 0 {
		t.Fatalf("%s has no statement %s", file, name)
	}
	body := text[start:]
	if end := strings.Index(body[len(marker):], "-- name: "); end >= 0 {
		body = body[:len(marker)+end]
	}
	return body
}

func canonicalFragment(t *testing.T, c contributionFamilyFragmentCase) string {
	t.Helper()
	switch c.Kind {
	case "root":
		return contributionRootCondition(c.Alias)
	case "member":
		return contributionMemberMatchSQL(c.Alias, c.A, c.B)
	case "root_name":
		return contributionRootNameMatchSQL(c.Alias, c.A)
	case "provider":
		return contributionProviderMatchSQL(c.Alias, c.A)
	case "latest":
		return contributionLatestAttemptSQL(c.Alias, c.A, c.B)
	case "live":
		return contributionLiveSQL(c.Alias)
	}
	t.Fatalf("case %s names the unknown fragment kind %q", c.Name, c.Kind)
	return ""
}

// The picker and the paged view are sqlc statements, which cannot include the
// canonical fragments, so each carries a copy. This holds every copy equal to
// the text the snapshot is built from.
func TestContributionFamilyFragmentsMatchTheStatements(t *testing.T) {
	cases, err := decodeFixtureRows[contributionFamilyFragmentCase](contributionFamilyFragmentsYAML)
	if err != nil {
		t.Fatalf("load the fragment corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		present[c.Name] = true
	}
	for _, required := range requiredContributionFamilyFragments {
		if !present[required] {
			t.Fatalf("the fragment corpus no longer contains %q; restore it rather than dropping it from the manifest", required)
		}
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			want := squeezeSQL(canonicalFragment(t, c))
			got := squeezeSQL(statementOf(t, c.File, c.Statement))
			if !strings.Contains(got, want) {
				t.Errorf("%s no longer carries the canonical %s fragment:\n%s", c.Statement, c.Kind, canonicalFragment(t, c))
			}
		})
	}
}

// The snapshot is built from the same builders, so it holds the one text the
// sqlc statements are compared with above.
func TestContributionSnapshotSQLIsBuiltFromTheCanonicalFragments(t *testing.T) {
	filter := contributionFamilyFilter{Query: "needle", QueryProjects: []string{"h"}, Provider: "codex"}
	sql, args := contributionSnapshotSQL(filter, pgtype.UUID{}, pgtype.UUID{}, "hash")
	if len(args) != 3+4 {
		t.Fatalf("the snapshot passes %d arguments, want the owner, project, collective, pattern, text, projects and provider", len(args))
	}
	got := squeezeSQL(sql)
	for name, fragment := range map[string]string{
		"root condition":      contributionRootCondition("r"),
		"root member match":   contributionMemberMatchSQL("r", "$4::text", "$5::text"),
		"root project name":   contributionRootNameMatchSQL("r", "$6::text[]"),
		"helper member match": contributionMemberMatchSQL("c", "$4::text", "$5::text"),
		"root provider":       contributionProviderMatchSQL("r", "$7::text"),
		"helper provider":     contributionProviderMatchSQL("c", "$7::text"),
		"latest attempt":      contributionLatestAttemptSQL("f", "$3", "a"),
	} {
		if !strings.Contains(got, squeezeSQL(fragment)) {
			t.Errorf("the snapshot statement lacks the canonical %s:\n%s\nin\n%s", name, fragment, sql)
		}
	}
	if strings.Contains(got, squeezeSQL(contributionRootNameMatchSQL("c", "$6::text[]"))) {
		t.Error("a helper's own project name must not match the search")
	}
}
