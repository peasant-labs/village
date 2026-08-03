package backfill

import (
	"bytes"
	"context"
	_ "embed"
	"io"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/content_identity/cases.yaml
var identityCasesYAML []byte

//go:embed testdata/key_rewrap/cases.yaml
var rewrapCasesYAML []byte

type contractCase struct {
	Name    string `yaml:"name"`
	Outcome string `yaml:"outcome"`
}

func loadCases(t *testing.T, data []byte, want int) []contractCase {
	t.Helper()
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	var rows []contractCase
	if err := d.Decode(&rows); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("trailing fixture document: %v", err)
	}
	if len(rows) != want {
		t.Fatalf("fixture rows=%d want=%d", len(rows), want)
	}
	for _, r := range rows {
		if r.Name == "" || r.Outcome == "" {
			t.Fatalf("invalid fixture row: %+v", r)
		}
	}
	return rows
}
func TestContentIdentityContractFixture(t *testing.T) { loadCases(t, identityCasesYAML, 8) }
func TestKeyRewrapContractFixtureAndBounds(t *testing.T) {
	loadCases(t, rewrapCasesYAML, 6)
	if _, err := Rewrap(context.Background(), nil, nil, 1, 0); err == nil {
		t.Fatal("zero bound accepted")
	}
	if _, err := Rewrap(context.Background(), nil, nil, 1, 1001); err == nil {
		t.Fatal("excessive bound accepted")
	}
}
