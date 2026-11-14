package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseContributionSnapshotTimeout(t *testing.T) {
	if d, err := ParseContributionSnapshotTimeout(""); err != nil || d != 0 {
		t.Fatalf("unset = %v, %v; want zero so the handler applies its default", d, err)
	}
	if d, err := ParseContributionSnapshotTimeout(" 30s "); err != nil || d != 30*time.Second {
		t.Fatalf("30s = %v, %v", d, err)
	}
	for _, bad := range []string{"soon", "0s", "-5s", "15"} {
		_, err := ParseContributionSnapshotTimeout(bad)
		if err == nil || !strings.Contains(err.Error(), "CONTRIBUTION_SNAPSHOT_TIMEOUT") {
			t.Errorf("%q: error = %v; want one naming the variable", bad, err)
		}
	}
}

func TestParseContributionSnapshotMaxConcurrent(t *testing.T) {
	if n, err := ParseContributionSnapshotMaxConcurrent(""); err != nil || n != 0 {
		t.Fatalf("unset = %d, %v; want zero so the handler derives a default", n, err)
	}
	if n, err := ParseContributionSnapshotMaxConcurrent("3"); err != nil || n != 3 {
		t.Fatalf("3 = %d, %v", n, err)
	}
	for _, bad := range []string{"many", "0", "-1", "1.5"} {
		_, err := ParseContributionSnapshotMaxConcurrent(bad)
		if err == nil || !strings.Contains(err.Error(), "CONTRIBUTION_SNAPSHOT_MAX_CONCURRENT") {
			t.Errorf("%q: error = %v; want one naming the variable", bad, err)
		}
	}
}
