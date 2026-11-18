package handler

import (
	_ "embed"
	"testing"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/content_source_images.yaml
var contentSourceImagesYAML []byte

type contentSourceImagesCase struct {
	Name     string `yaml:"name"`
	Document string `yaml:"document"`
	Count    int    `yaml:"count"`
}

func TestContentSourceOmittedImages(t *testing.T) {
	cases, err := decodeFixtureRows[contentSourceImagesCase](contentSourceImagesYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"header-omitted-images-counts-typed-omission-parts": true}
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			// The current production rule protects every typed image row.
			// Inject a future cook that drops one to prove the count is still
			// a stored-document fact, independent of a partition verdict.
			cook := func(turns []schema.TranscriptCookTurn, harness schema.Harness) ([]schema.CookedTurn, error) {
				verdict, err := schema.CookTranscriptRows(turns, harness)
				if err != nil {
					return nil, err
				}
				verdict[1].Keep = false
				verdict[1].ServedPosition = -1
				verdict[1].DisplayNumber = ""
				return verdict, nil
			}
			index, err := newTranscriptIndex(transcriptIndexInput{Bytes: []byte(tc.Document), Cook: cook})
			if err != nil {
				t.Fatal(err)
			}
			if index.OmittedImages != tc.Count {
				t.Fatalf("omitted images = %d, want %d", index.OmittedImages, tc.Count)
			}
			if len(index.Partitions) != 0 {
				t.Fatal("image count unexpectedly opened a partition")
			}
			main, err := index.Partition(mainPartitionKey())
			if err != nil {
				t.Fatal(err)
			}
			if len(main.Rows) >= len(main.Verdict) {
				t.Fatal("fixture must include a cooked-out row")
			}
			if index.OmittedImages != tc.Count {
				t.Fatal("cooking changed the document count")
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing required cases: %v", required)
	}
}
