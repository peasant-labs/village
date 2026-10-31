package router

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

//go:embed testdata/published_schema.yaml
var publishedSchemaYAML []byte

// The digest is the independently downloaded v0.39.0 GitHub release asset,
// not a checksum calculated from the server under test. Keep the literal
// version guard as well: both bytes and intended contract version must match.
func TestMountedSchemaMatchesPublishedReleaseAsset(t *testing.T) {
	fixture, err := decodeSingleYAMLDocument[struct {
		Name    string `yaml:"name"`
		Module  string `yaml:"module"`
		Version string `yaml:"version"`
		SHA256  string `yaml:"sha256"`
	}](publishedSchemaYAML)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.Name != "published-village-api-0.34.0" || fixture.Module != "v0.39.0" {
		t.Fatal("published artifact identity changed; review the actual release before updating this fixture")
	}
	rec := httptest.NewRecorder()
	productionRouter(t).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))
	if rec.Code != 200 {
		t.Fatalf("served contract status %d", rec.Code)
	}
	digest := sha256.Sum256(rec.Body.Bytes())
	if hex.EncodeToString(digest[:]) != fixture.SHA256 {
		t.Fatalf("mounted served bytes differ from published release asset: %x", digest)
	}
	var spec struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Info.Version != fixture.Version {
		t.Fatalf("served version %s differs from published %s", spec.Info.Version, fixture.Version)
	}
}
