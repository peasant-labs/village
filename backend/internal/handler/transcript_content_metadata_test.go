package handler

import (
	_ "embed"
	"testing"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/content_source_metadata.yaml
var contentSourceMetadataYAML []byte

type contentSourceMetadataCase struct {
	Name       string                          `yaml:"name"`
	Validation schema.TranscriptReadValidation `yaml:"validation"`
	Document   string                          `yaml:"document"`
	Session    string                          `yaml:"session"`
	Usage      schema.TranscriptUsagePresence  `yaml:"usage"`
	Native     schema.TranscriptNativePresence `yaml:"native"`
	History    int                             `yaml:"history"`
}

func TestContentSourceMetadata(t *testing.T) {
	cases, err := decodeFixtureRows[contentSourceMetadataCase](contentSourceMetadataYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"strict-main-evidence": true, "migrated-history-evidence": true}
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate fixture %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			x, err := newTranscriptIndex(transcriptIndexInput{Bytes: []byte(tc.Document), Revision: 3, Validation: tc.Validation})
			if err != nil {
				t.Fatal(err)
			}
			if x.Validation != tc.Validation || x.ContentSessionID == nil || *x.ContentSessionID != tc.Session || x.NativePresence != tc.Native || len(x.History) != tc.History {
				t.Fatalf("unexpected metadata: %+v", x)
			}
			p, err := x.Partition(mainPartitionKey(), noTranscriptBuildReserve)
			if err != nil {
				t.Fatal(err)
			}
			if p.UsagePresent != tc.Usage {
				t.Fatalf("usage %q, want %q", p.UsagePresent, tc.Usage)
			}
			a := x.Representation(schema.TranscriptViewerProjectionAnonymous, false, x.NativePresence)
			b := x.Representation(schema.TranscriptViewerProjectionAuthenticated, false, x.NativePresence)
			if a == b || a == "" {
				t.Fatal("representation must identify the viewer projection")
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing fixtures: %v", required)
	}
}
