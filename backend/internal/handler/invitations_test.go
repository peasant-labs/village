package handler

import (
	_ "embed"
	"testing"
)

//go:embed testdata/invited-usernames.yaml
var invitedUsernamesYAML []byte

type invitedUsernameCase struct {
	Name   string `yaml:"name"`
	Typed  string `yaml:"typed"`
	Stored string `yaml:"stored"`
}

var requiredInvitedUsernameCases = []string{
	"plain_handle", "leading_at_is_dropped", "case_is_folded", "surrounding_space_is_trimmed",
	"empty_is_refused", "only_an_at_is_refused", "inner_space_is_refused", "slash_is_refused",
	"control_character_is_refused", "one_hundred_characters_is_kept", "one_hundred_and_one_characters_is_refused",
}

func TestNormalizeInvitedUsername(t *testing.T) {
	cases, err := decodeFixtureRows[invitedUsernameCase](invitedUsernamesYAML)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		present[c.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "invited-usernames", present, requiredInvitedUsernameCases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			stored, ok := normalizeInvitedUsername(c.Typed)
			if ok != (c.Stored != "") || stored != c.Stored {
				t.Fatalf("normalize(%q) = %q, %v; want %q", c.Typed, stored, ok, c.Stored)
			}
		})
	}
}
