package handler

import (
	"bytes"
	_ "embed"
	"errors"
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

// decodeFixtureDocument is the one strict YAML fixture decoder for this
// package's tests. It decodes exactly one document into T, refuses a field T
// does not declare, and refuses a second document. Callers add the fixture's
// name to the returned error.
func decodeFixtureDocument[T any](data []byte) (T, error) {
	var document T
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return document, fmt.Errorf("decode the fixture with unknown fields refused; correct or remove the named YAML field: %w", err)
	}
	var trailing any
	switch err := decoder.Decode(&trailing); {
	case errors.Is(err, io.EOF):
		return document, nil
	case err == nil:
		return document, errors.New("fixture must contain exactly one YAML document, but it has a trailing YAML document; remove the trailing document")
	default:
		return document, fmt.Errorf("fixture must contain exactly one YAML document, but the content after the first document does not parse; remove it: %w", err)
	}
}

// decodeFixtureRows decodes a fixture whose single document is a list of rows.
// It is decodeFixtureDocument with a list type, not a second decoder.
func decodeFixtureRows[T any](data []byte) ([]T, error) {
	return decodeFixtureDocument[[]T](data)
}

type fixtureLoaderProbe struct {
	Name string `yaml:"name"`
}

//go:embed testdata/fixture_loader_unknown_field.yaml
var fixtureLoaderUnknownFieldYAML []byte

//go:embed testdata/fixture_loader_multiple_documents.yaml
var fixtureLoaderMultipleDocumentsYAML []byte

//go:embed testdata/fixture_loader_unparseable_trailing.yaml
var fixtureLoaderUnparseableTrailingYAML []byte

func TestDecodeFixtureDocumentRejectsUnknownFields(t *testing.T) {
	_, err := decodeFixtureDocument[[]fixtureLoaderProbe](fixtureLoaderUnknownFieldYAML)
	if err == nil || !strings.Contains(err.Error(), "field unknown") {
		t.Fatalf("unknown fixture field error = %v, want strict field rejection", err)
	}
}

func TestDecodeFixtureDocumentRejectsMultipleDocuments(t *testing.T) {
	_, err := decodeFixtureDocument[[]fixtureLoaderProbe](fixtureLoaderMultipleDocumentsYAML)
	if err == nil || !strings.Contains(err.Error(), "exactly one YAML document") || !strings.Contains(err.Error(), "trailing YAML document") {
		t.Fatalf("multiple-document fixture error = %v, want single-document rejection", err)
	}
}

func TestDecodeFixtureDocumentRejectsUnparseableTrailingContent(t *testing.T) {
	_, err := decodeFixtureDocument[[]fixtureLoaderProbe](fixtureLoaderUnparseableTrailingYAML)
	if err == nil || !strings.Contains(err.Error(), "exactly one YAML document") || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("unparseable trailing content error = %v, want single-document rejection naming the parse failure", err)
	}
}
