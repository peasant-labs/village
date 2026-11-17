package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/config"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/health_probe.yaml
var healthProbeYAML []byte

type healthProbeCase struct {
	Name           string            `yaml:"name"`
	Stage          string            `yaml:"stage"`
	Traits         []string          `yaml:"traits"`
	Environment    map[string]string `yaml:"environment"`
	BuildRevision  string            `yaml:"build_revision"`
	AppliedVersion int               `yaml:"applied_version"`
	LaterVersion   int               `yaml:"later_version"`
}

type healthProbeFixtures struct {
	Cases []healthProbeCase `yaml:"cases"`
}

// Names are independent of the corpus, so deleting a case cannot delete its
// obligation. The next release activates its own stage without a skip event.
var requiredHealthProbeNames = []string{
	"health-trait-compiled-from-features",
	"health-trait-not-runtime-overridable",
	"health-n-omits-segmented-reads-v2",
	"health-migrations-report-applied-version",
	"health-migrations-not-compiled-target",
	"health-migrations-absent-when-unreadable",
	"health-build-revision-from-build-not-environment",
	"health-carries-no-transcript-fact",
	"health-response-members",
	"health-no-store-cache-control",
	"health-trait-absent-when-route-unmounted",
	"health-n1-serves-segmented-reads-v2",
	"health-serves-sorted-and-unique",
}

func loadHealthProbeFixtures(t *testing.T) []healthProbeCase {
	t.Helper()
	var fixture healthProbeFixtures
	if err := yaml.Unmarshal(healthProbeYAML, &fixture); err != nil {
		t.Fatalf("load health probe cases: %v", err)
	}
	found := make(map[string]bool)
	for _, c := range fixture.Cases {
		if c.Name == "" || found[c.Name] || (c.Stage != "n" && c.Stage != "n1") {
			t.Fatalf("health probe fixture has a missing or duplicate name or unknown stage: %+v", c)
		}
		found[c.Name] = true
	}
	for _, name := range requiredHealthProbeNames {
		if !found[name] {
			t.Fatalf("health probe fixture is missing required case %q", name)
		}
	}
	return fixture.Cases
}

func healthResponse(t *testing.T, h *Handler) (map[string]json.RawMessage, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	h.Health(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("health returned %d, want 200", w.Code)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	return body, w
}

func healthTraits(t *testing.T, body map[string]json.RawMessage) []string {
	t.Helper()
	var traits []string
	if err := json.Unmarshal(body["serves"], &traits); err != nil {
		t.Fatalf("decode compiled feature set: %v", err)
	}
	return traits
}

func setHealthBuildRevision(t *testing.T, revision string) {
	t.Helper()
	previous := buildRevision
	buildRevision = revision
	t.Cleanup(func() { buildRevision = previous })
}

func TestHealthProbeFixtures(t *testing.T) {
	if want := os.Getenv("HEALTH_TEST_EXPECT_REVISION"); want != "" && buildRevision != want {
		t.Fatalf("linked build revision = %q, want %q", buildRevision, want)
	}
	for _, c := range loadHealthProbeFixtures(t) {
		if c.Stage != "n" {
			continue
		}
		switch c.Name {
		case "health-n-omits-segmented-reads-v2",
			"health-migrations-report-applied-version",
			"health-migrations-not-compiled-target",
			"health-carries-no-transcript-fact",
			"health-response-members":
			// These cases execute on the mounted router or a real database.
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			for key, value := range c.Environment {
				t.Setenv(key, value)
			}
			cfg := &config.Config{FrontendURL: "https://example.test"}
			if c.Name == "health-trait-not-runtime-overridable" {
				cfg = config.Load()
			}
			h := NewWithTitlePipeline(cfg, nil, nil, nil)
			switch c.Name {
			case "health-trait-compiled-from-features", "health-trait-not-runtime-overridable":
				body, _ := healthResponse(t, h)
				if got := healthTraits(t, body); !reflect.DeepEqual(got, c.Traits) {
					t.Fatalf("compiled traits = %v, want %v", got, c.Traits)
				}
			case "health-migrations-absent-when-unreadable":
				pool, err := pgxpool.New(context.Background(), "postgres://test:test@127.0.0.1:1/test")
				if err != nil {
					t.Fatal(err)
				}
				pool.Close()
				h.pool = pool
				body, _ := healthResponse(t, h)
				if _, ok := body["migrations"]; ok {
					t.Fatal("an unreadable registry was reported as an applied migration")
				}
			case "health-build-revision-from-build-not-environment":
				setHealthBuildRevision(t, c.BuildRevision)
				body, _ := healthResponse(t, h)
				var got string
				if err := json.Unmarshal(body["buildRevision"], &got); err != nil || got != c.BuildRevision {
					t.Fatalf("linked revision = %q, want %q (err %v)", got, c.BuildRevision, err)
				}
				buildRevision = ""
				body, _ = healthResponse(t, h)
				if _, present := body["buildRevision"]; present {
					t.Fatal("an unlinked revision must be omitted, not read from the environment")
				}
			case "health-no-store-cache-control":
				_, w := healthResponse(t, h)
				if got := w.Header().Get("Cache-Control"); got != "no-store" {
					t.Fatalf("cache-control = %q, want no-store", got)
				}
				if got := w.Header().Get("Content-Type"); got != "application/json" {
					t.Fatalf("content-type = %q, want application/json", got)
				}
			default:
				t.Fatalf("stage n case %q has no owner", c.Name)
			}
		})
	}
}
