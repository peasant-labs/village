package handler

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func loadFixtureRows[T any](t *testing.T, data []byte, wantRows int) []T {
	t.Helper()
	rows, err := decodeFixtureRows[T](data)
	if err != nil {
		t.Fatalf("load YAML fixture rows: %v", err)
	}
	if len(rows) != wantRows {
		t.Fatalf("fixture row count = %d, want %d; update the guard when intentionally changing compatibility coverage", len(rows), wantRows)
	}
	return rows
}

func decodeFixtureRows[T any](data []byte) ([]T, error) {
	var rows []T
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&rows); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("fixture must contain exactly one YAML document")
	}
	return rows, nil
}

type fixtureLoaderProbe struct {
	Name string `yaml:"name"`
}

//go:embed testdata/fixture_loader_unknown_field.yaml
var fixtureLoaderUnknownFieldYAML []byte

//go:embed testdata/fixture_loader_multiple_documents.yaml
var fixtureLoaderMultipleDocumentsYAML []byte

func TestDecodeFixtureRowsRejectsUnknownFields(t *testing.T) {
	_, err := decodeFixtureRows[fixtureLoaderProbe](fixtureLoaderUnknownFieldYAML)
	if err == nil || !strings.Contains(err.Error(), "field unknown") {
		t.Fatalf("unknown fixture field error = %v, want strict field rejection", err)
	}
}

func TestDecodeFixtureRowsRejectsMultipleDocuments(t *testing.T) {
	_, err := decodeFixtureRows[fixtureLoaderProbe](fixtureLoaderMultipleDocumentsYAML)
	if err == nil || !strings.Contains(err.Error(), "exactly one YAML document") {
		t.Fatalf("multiple-document fixture error = %v, want single-document rejection", err)
	}
}
