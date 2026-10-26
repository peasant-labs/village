package handler

import (
	"context"
	_ "embed"
	"errors"
	"testing"
	"time"

	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/email-configuration.yaml
var emailConfigurationYAML []byte

type emailConfigurationCase struct {
	Name       string `yaml:"name"`
	APIKey     string `yaml:"api_key"`
	From       string `yaml:"from"`
	SigningKey string `yaml:"signing_key"`
	Enabled    bool   `yaml:"enabled"`
}

var requiredEmailConfigurationCases = []string{
	"nothing_set_is_off", "key_missing_is_off", "sender_missing_is_off", "signing_key_missing_is_off",
	"short_signing_key_is_off", "blank_values_are_off", "all_three_set_is_on",
}

// TestEmailIsOffUnlessFullyConfigured proves the production wiring turns email
// on only with every EMAIL_* variable set.
func TestEmailIsOffUnlessFullyConfigured(t *testing.T) {
	cases, err := decodeFixtureRows[emailConfigurationCase](emailConfigurationYAML)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		present[c.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "email-configuration", present, requiredEmailConfigurationCases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			mail := newMailService(&config.Config{EmailProviderAPIKey: c.APIKey, EmailFrom: c.From, EmailLinkSigningKey: c.SigningKey})
			if (mail != nil) != c.Enabled {
				t.Fatalf("email enabled = %v, want %v", mail != nil, c.Enabled)
			}
		})
	}
}

// TestDrainNoticesWaitsForEmailsInFlight proves a shutdown waits for an
// invitation email still being sent, and stops waiting when its time is up.
// Mutation check: returning nil at once from DrainNotices fails the first
// assertion.
func TestDrainNoticesWaitsForEmailsInFlight(t *testing.T) {
	h := &Handler{}
	h.notices.Add(1)
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := h.DrainNotices(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain with an email in flight returned %v, want the deadline", err)
	}
	finished := make(chan error, 1)
	go func() { finished <- h.DrainNotices(context.Background()) }()
	select {
	case err := <-finished:
		t.Fatalf("drain returned %v before the email finished", err)
	case <-time.After(20 * time.Millisecond):
	}
	h.notices.Done()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("drain after the email finished returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not return after the email finished")
	}
}
