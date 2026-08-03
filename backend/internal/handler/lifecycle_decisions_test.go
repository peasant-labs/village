package handler

import (
	"bytes"
	_ "embed"
	"io"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/transcript_lifecycle/cases.yaml
var lifecycleCasesYAML []byte

type transactionCase struct {
	Operation  string `yaml:"operation"`
	Completion string `yaml:"completion"`
	Installed  bool   `yaml:"installed"`
	Expected   string `yaml:"expected"`
}
type descriptorRaceCase struct {
	Rows     int64  `yaml:"rows"`
	Expected string `yaml:"expected"`
}
type freshRetryCase struct {
	Missing    bool   `yaml:"missing"`
	Authorized bool   `yaml:"authorized"`
	Changed    bool   `yaml:"changed"`
	ETag       bool   `yaml:"etag"`
	Expected   string `yaml:"expected"`
}
type lifecycleCases struct {
	TransactionOutcomes []transactionCase    `yaml:"transaction_outcomes"`
	DescriptorRaces     []descriptorRaceCase `yaml:"descriptor_races"`
	FreshRetries        []freshRetryCase     `yaml:"fresh_retries"`
}

func loadLifecycleCases(t *testing.T) lifecycleCases {
	t.Helper()
	var cases lifecycleCases
	decoder := yaml.NewDecoder(bytes.NewReader(lifecycleCasesYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("decode transcript lifecycle fixtures: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("transcript lifecycle fixture must contain exactly one document: %v", err)
	}
	if len(cases.TransactionOutcomes) != 42 || len(cases.DescriptorRaces) != 9 || len(cases.FreshRetries) != 16 {
		t.Fatalf("lifecycle fixture counts = %d/%d/%d; want 42/9/16", len(cases.TransactionOutcomes), len(cases.DescriptorRaces), len(cases.FreshRetries))
	}
	return cases
}

func TestLifecycleDecisionFixtures(t *testing.T) {
	cases := loadLifecycleCases(t)
	for _, tc := range cases.TransactionOutcomes {
		completion := TransactionCommitted
		if tc.Completion == "rollback" {
			completion = TransactionKnownRollback
		} else if tc.Completion == "ambiguous" {
			completion = TransactionCommitAmbiguous
		}
		decision := cleanupDecision(tc.Operation, completion, tc.Installed)
		got := "none"
		if decision.Reconcile {
			got = "reconcile"
		} else if decision.DeleteCandidate {
			got = "candidate"
		} else if decision.DeleteSuperseded {
			got = "superseded"
		} else if decision.DeleteTarget {
			got = "target"
		}
		if got != tc.Expected {
			t.Errorf("%s/%s/installed=%v cleanup=%s; want %s", tc.Operation, tc.Completion, tc.Installed, got, tc.Expected)
		}
	}
	for _, tc := range cases.DescriptorRaces {
		got := "stale"
		if casOutcome(tc.Rows) == CASInstalled {
			got = "installed"
		}
		if got != tc.Expected {
			t.Errorf("rows=%d outcome=%s; want %s", tc.Rows, got, tc.Expected)
		}
	}
	for _, tc := range cases.FreshRetries {
		if got := decideFreshRead(tc.Missing, tc.Authorized, tc.Changed, tc.ETag); string(got) != tc.Expected {
			t.Errorf("fresh retry %+v = %s; want %s", tc, got, tc.Expected)
		}
	}
}
