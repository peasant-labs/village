package main

import (
	"strings"
	"testing"
)

// These tests exercise error propagation without a database. Every failure
// path must return an error from run, which main turns into a non-zero exit.

const unreachableAdmin = "postgres://test:test@127.0.0.1:1/postgres?sslmode=disable"

func TestRunRejectsUnknownDataset(t *testing.T) {
	err := run([]string{"seed", "-dataset", "bogus", "-out", t.TempDir(), "-admin-dsn", unreachableAdmin})
	if err == nil || !strings.Contains(err.Error(), "dataset must be one of") {
		t.Fatalf("want dataset error, got %v", err)
	}
}

func TestRunRejectsUnknownConfig(t *testing.T) {
	err := run([]string{"measure", "-dataset", "small", "-config", "ZZ", "-out", t.TempDir(), "-admin-dsn", unreachableAdmin})
	if err == nil || !strings.Contains(err.Error(), "config must be one of") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestRunRefusesNonLocalHost(t *testing.T) {
	err := run([]string{"seed", "-dataset", "small", "-out", t.TempDir(), "-admin-dsn", "postgres://test:test@db.example.invalid/postgres"})
	if err == nil || !strings.Contains(err.Error(), "refusing non-local") {
		t.Fatalf("want non-local refusal, got %v", err)
	}
}

func TestRunRequiresOutputDirectory(t *testing.T) {
	err := run([]string{"seed", "-dataset", "small", "-out", "", "-admin-dsn", unreachableAdmin})
	if err == nil || !strings.Contains(err.Error(), "output directory is required") {
		t.Fatalf("want output directory error, got %v", err)
	}
}

func TestMissingManifestIsAnError(t *testing.T) {
	if _, err := loadManifest(t.TempDir(), "small"); err == nil {
		t.Fatalf("missing manifest must be an error")
	}
}
