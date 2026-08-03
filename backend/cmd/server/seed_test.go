package main

import "testing"

func TestDevelopmentSeedProfilesStrictFixture(t *testing.T) {
	profiles, err := loadSeedProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if profiles[0].Name != "core" || profiles[1].Name != "privacy" {
		t.Fatalf("unexpected profiles: %+v", profiles)
	}
}
