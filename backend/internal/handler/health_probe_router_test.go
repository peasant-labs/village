package handler_test

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/router"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/health_probe.yaml
var healthProbeRoutesYAML []byte

func TestHealthProbeMountedRoutes(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name   string            `yaml:"name"`
			Stage  string            `yaml:"stage"`
			Traits []string          `yaml:"traits"`
			Paths  map[string]string `yaml:"paths"`
		} `yaml:"cases"`
	}
	if err := yaml.Unmarshal(healthProbeRoutesYAML, &fixture); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range fixture.Cases {
		if c.Name != "health-n-omits-segmented-reads-v2" {
			continue
		}
		found = true
		if c.Stage != "n" || !strings.HasSuffix(c.Paths["directory"], "/segments") ||
			!strings.HasSuffix(c.Paths["segment"], "/segments/0") || !strings.HasSuffix(c.Paths["window"], "/window") {
			t.Fatal("mounted health case must name the directory, segment and window stub paths")
		}
		routes := router.New(&config.Config{JWTSecret: "synthetic-health-test-secret-at-least-32-bytes"}, nil, nil, nil)
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
		var body struct {
			Serves []string `json:"serves"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || !reflect.DeepEqual(body.Serves, c.Traits) {
			t.Fatalf("mounted health = %d %s, want traits %v", w.Code, w.Body.String(), c.Traits)
		}
		for _, path := range c.Paths {
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusNotImplemented {
				t.Fatalf("mounted %s = %d, want 501", path, w.Code)
			}
		}
	}
	if !found {
		t.Fatal("missing mounted health fixture case")
	}
}
