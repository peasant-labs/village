package handler

import (
	_ "embed"
	"net/http"
	"strings"
	"testing"
)

//go:embed testdata/observed_model_preservation/read_evidence.yaml
var readEvidenceFixtureYAML []byte
var readEvidenceNames = [...]string{"previously_stored_non_assistant_is_opaque", "invalid_raw_model_bytes_are_rejected"}

type readEvidenceFixture struct {
	Cases []readEvidenceCase `yaml:"cases"`
}
type readEvidenceCase struct {
	Name        string `yaml:"name"`
	Content     string `yaml:"content"`
	WantStatus  int    `yaml:"wantStatus"`
	WantError   string `yaml:"wantError"`
	WantRewrite bool   `yaml:"wantRewrite"`
	Corrected   string `yaml:"corrected"`
}

func TestMountedReadEvidenceCompatibilityAndRecovery(t *testing.T) {
	for _, tc := range loadReadEvidenceFixtures(t) {
		t.Run(tc.Name, func(t *testing.T) {
			const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
			store := newFakeBlobStore()
			store.put(key, []byte(tc.Content))
			h := newTestHandler(publicTranscriptQuerier(key), store)
			response := getContent(t, h, mustFixtureUUID(t))
			if response.Code != tc.WantStatus {
				t.Fatalf("status=%d body=%s want=%d", response.Code, response.Body.String(), tc.WantStatus)
			}
			if tc.WantError != "" && !strings.Contains(decodeError(t, response.Body.Bytes()), tc.WantError) {
				t.Fatalf("body=%s lacks %q", response.Body.String(), tc.WantError)
			}
			if got := store.uploadCount() > 0; got != tc.WantRewrite {
				t.Fatalf("rewrite=%v want=%v", got, tc.WantRewrite)
			}
			store.put(key, []byte(tc.Corrected))
			recovered := getContent(t, newTestHandler(publicTranscriptQuerier(key), store), mustFixtureUUID(t))
			if recovered.Code != http.StatusOK {
				t.Fatalf("corrected fixture unreadable: %d %s", recovered.Code, recovered.Body.String())
			}
		})
	}
}
func loadReadEvidenceFixtures(t *testing.T) []readEvidenceCase {
	t.Helper()
	f, err := decodeFixtureDocument[readEvidenceFixture](readEvidenceFixtureYAML)
	if err != nil {
		t.Fatalf("decode the read evidence fixture: %v", err)
	}
	if len(f.Cases) != len(readEvidenceNames) {
		t.Fatalf("count=%d want=%d", len(f.Cases), len(readEvidenceNames))
	}
	r := map[string]bool{}
	for _, n := range readEvidenceNames {
		r[n] = true
	}
	s := map[string]bool{}
	for _, c := range f.Cases {
		if !r[c.Name] || s[c.Name] {
			t.Fatalf("unknown or duplicate %q", c.Name)
		}
		s[c.Name] = true
	}
	return f.Cases
}
