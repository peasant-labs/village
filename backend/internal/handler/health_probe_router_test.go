package handler_test

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/handler"
	"github.com/peasant-labs/village/backend/internal/router"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/health_probe.yaml
var healthProbeRoutesYAML []byte

func TestHealthProbeMountedRoutes(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name   string   `yaml:"name"`
			Traits []string `yaml:"traits"`
		} `yaml:"cases"`
	}
	if err := yaml.Unmarshal(healthProbeRoutesYAML, &fixture); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"health-n-omits-segmented-reads-v2": false, "health-trait-absent-when-route-unmounted": false, "health-n1-serves-segmented-reads-v2": false, "health-serves-sorted-and-unique": false}
	for _, c := range fixture.Cases {
		if _, ok := required[c.Name]; !ok {
			continue
		}
		required[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			cfg := &config.Config{JWTSecret: "synthetic-health-test-secret-at-least-32-bytes"}
			var routes http.Handler
			switch c.Name {
			case "health-n-omits-segmented-reads-v2", "health-trait-absent-when-route-unmounted":
				mux := chi.NewRouter()
				h := handler.New(cfg, nil, nil)
				mux.Get("/health", h.Health)

				router.ConfigureHealthRoutes(mux, h)
				routes = mux
			default:
				routes = router.New(cfg, nil, nil, nil)
			}
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
			var body struct {
				Serves []string `json:"serves"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || !reflect.DeepEqual(body.Serves, c.Traits) {
				t.Fatalf("mounted health = %d %s, want traits %v", w.Code, w.Body.String(), c.Traits)
			}
			if !sort.StringsAreSorted(body.Serves) {
				t.Fatalf("traits not sorted: %v", body.Serves)
			}
			for i := 1; i < len(body.Serves); i++ {
				if body.Serves[i] == body.Serves[i-1] {
					t.Fatalf("duplicate trait: %v", body.Serves)
				}
			}
		})
	}
	for name, found := range required {
		if !found {
			t.Errorf("missing mounted health fixture %q", name)
		}
	}
}
