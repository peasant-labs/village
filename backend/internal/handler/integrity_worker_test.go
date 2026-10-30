package handler

import (
	"bytes"
	_ "embed"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/peasant-labs/village/backend/internal/scry"
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
	"first_health_sets", "health_does_not_move_back", "newer_health_moves_forward", "newer_answer_moves_forward", "answer_order_is_numeric",
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
				s.observeVersion(c.Current)
			}
			switch c.Source {
			case "health", "answer":
				// The two sources follow the one rule; the case names which
				// report it models.
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

// TestRejectedSecretWarnsOnce: a refused secret warns once. Neither a healthy
// health call (health needs no secret) nor a failed check (a 502 or 503 can
// come from a proxy in front of the secret check) re-arms the warning; only a
// check the service answered does.
func TestRejectedSecretWarnsOnce(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	warnings := func() int { return strings.Count(logged.String(), "rejected the shared secret") }

	s := newIntegrityServiceFor(nil)
	refused := &scry.Error{Class: scry.ClassRejected, Status: http.StatusUnauthorized}
	forbidden := &scry.Error{Class: scry.ClassRejected, Status: http.StatusForbidden}
	if !s.noteCheckResult(refused) || !s.noteCheckResult(forbidden) || warnings() != 1 {
		t.Fatalf("two refusals logged %d warnings, want 1, and both must count as refusals", warnings())
	}
	healthy := newTestHandler(&mockQuerier{}, nil)
	healthy.integrity = s
	s.checker = &blockingChecker{}
	if err := healthy.integrityTick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !s.noteCheckResult(refused) || warnings() != 1 {
		t.Fatalf("a healthy health call re-armed the warning: %d warnings", warnings())
	}
	for _, status := range []int{http.StatusBadGateway, http.StatusServiceUnavailable} {
		if s.noteCheckResult(&scry.Error{Class: scry.ClassUnavailable, Status: status}) {
			t.Fatalf("a %d counted as a refused secret", status)
		}
	}
	if !s.noteCheckResult(refused) || warnings() != 1 {
		t.Fatalf("a failed check re-armed the warning: %d warnings", warnings())
	}
	if s.noteCheckResult(nil) {
		t.Fatal("an answered check counted as a refused secret")
	}
	if !s.noteCheckResult(refused) || warnings() != 2 {
		t.Fatalf("a refusal after an answered check logged %d warnings in all, want 2", warnings())
	}
}
