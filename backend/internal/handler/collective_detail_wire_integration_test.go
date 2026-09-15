//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/collective_detail_wire.yaml
var collectiveDetailWireYAML []byte

// collectiveDetailWireCase is one raw collective-detail body plus the route that
// serves it. It declares the stored pull-request check settings its own raw group
// object carries, so the loader can prove the oracle agrees with itself and the
// test compares the canonical binding against that declaration instead of
// against another invocation of the projection under test.
type collectiveDetailWireCase struct {
	Name             string  `yaml:"name"`
	GroupID          string  `yaml:"group_id"`
	Query            string  `yaml:"query"`
	PostPromptsCheck *bool   `yaml:"post_prompts_check"`
	PromptsCheckMode *string `yaml:"prompts_check_mode"`
	ExpectedJSON     string  `yaml:"expected_json"`
}
type collectiveDetailWireFixtures struct {
	OwnerID   string                     `yaml:"owner_id"`
	PendingID string                     `yaml:"pending_id"`
	OwnerName string                     `yaml:"owner_name"`
	SeedSQL   string                     `yaml:"seed_sql"`
	Cases     []collectiveDetailWireCase `yaml:"cases"`
}

// collectiveDetailWireRawGroup is the subset of the raw oracle's group object
// this gate reads back, so absence and explicit values stay distinguishable.
type collectiveDetailWireRawGroup struct {
	PostPromptsCheck *bool   `json:"post_prompts_check"`
	PromptsCheckMode *string `json:"prompts_check_mode"`
}
type collectiveDetailWireRawBody struct {
	Group collectiveDetailWireRawGroup `json:"group"`
}

// requiredCollectiveDetailWireCases names the route/collective pairs this gate
// must keep covering. Renaming or deleting one fails the loader.
var requiredCollectiveDetailWireCases = []string{
	"legacy-exact-group-record-with-defaulted-check-settings",
	"grouped-exact-group-record-with-defaulted-check-settings",
	"legacy-exact-group-record-with-overridden-check-settings",
	"grouped-exact-group-record-with-overridden-check-settings",
}

func loadCollectiveDetailWireFixtures(t *testing.T) collectiveDetailWireFixtures {
	t.Helper()
	d := yaml.NewDecoder(bytes.NewReader(collectiveDetailWireYAML))
	d.KnownFields(true)
	var fixture collectiveDetailWireFixtures
	if err := d.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("expected one wire fixture document: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] || !json.Valid([]byte(c.ExpectedJSON)) {
			t.Fatalf("invalid or duplicate raw wire fixture %q", c.Name)
		}
		if _, err := uuid.Parse(c.GroupID); err != nil {
			t.Fatalf("raw wire fixture %q must name the collective it reads: %v", c.Name, err)
		}
		if c.PostPromptsCheck == nil || c.PromptsCheckMode == nil {
			t.Fatalf("raw wire fixture %q must declare the collective's stored pull-request check settings", c.Name)
		}
		if !schema.VillagePromptsCheckMode(*c.PromptsCheckMode).IsValid() {
			t.Fatalf("raw wire fixture %q declares a check mode outside the closed menu", c.Name)
		}
		var oracle collectiveDetailWireRawBody
		if err := json.Unmarshal([]byte(c.ExpectedJSON), &oracle); err != nil {
			t.Fatalf("raw wire fixture %q is not a collective detail body: %v", c.Name, err)
		}
		if oracle.Group.PostPromptsCheck == nil || oracle.Group.PromptsCheckMode == nil ||
			*oracle.Group.PostPromptsCheck != *c.PostPromptsCheck ||
			*oracle.Group.PromptsCheckMode != *c.PromptsCheckMode {
			t.Fatalf("raw wire fixture %q declares check settings that its own raw group object does not carry", c.Name)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredCollectiveDetailWireCases {
		if !seen[name] {
			t.Fatalf("missing required raw wire fixture %s", name)
		}
	}
	return fixture
}

func TestCollectiveDetailRawWireRegisteredRoutes(t *testing.T) {
	f := loadCollectiveDetailWireFixtures(t)
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	owner := uuid.MustParse(f.OwnerID)
	pending := uuid.MustParse(f.PendingID)
	defer cleanupOwners(t, ctx, pool, toPgUUID(owner), toPgUUID(pending))
	if _, err := pool.Exec(ctx, f.SeedSQL); err != nil {
		t.Fatal(err)
	}
	h := &Handler{cfg: minimalConfig(), pool: pool, queries: sqlc.New(pool), projectNames: projectname.Resolver{Label: schema.RemoteLabel}}
	routes := chi.NewRouter()
	routes.Route("/api/v1", func(r chi.Router) { h.RegisterTranscriptBrowseRoutes(r); h.RegisterCollectiveBrowseRoutes(r) })
	token, err := auth.CreateToken(h.cfg.JWTSecret, owner, f.OwnerName)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/groups/"+c.GroupID+c.Query, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d: %s", w.Code, w.Body)
			}
			var expected, actual any
			if err := json.Unmarshal([]byte(c.ExpectedJSON), &expected); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(w.Body.Bytes(), &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("registered detail differs from independent raw fixture\nactual: %s\nexpected: %s", w.Body, c.ExpectedJSON)
			}
			// Both schema-owned bindings must consume that exact response without
			// dropping or fabricating fields. The oracle is authored raw YAML,
			// not another invocation of the mapper being tested.
			var typed any
			var group schema.VillageGroupDetailRecord
			if c.Query == "" {
				var response schema.VillageGroupDetailResponse
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				typed, group = response, response.Group
			} else {
				var response schema.VillageGroupedGroupDetailResponse
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if err := response.Validate(); err != nil {
					t.Fatal(err)
				}
				typed, group = response, response.Group
			}
			// The stored pull-request check settings are facts on the row both
			// route variants serve. The canonical binding must carry exactly the
			// settings the fixture declares, never substituting a default or
			// dropping a stored one.
			if group.PostPromptsCheck == nil || group.PromptsCheckMode == nil {
				t.Fatalf("canonical detail binding dropped the declared check settings for %s: got (%v, %v)", c.Name, group.PostPromptsCheck, group.PromptsCheckMode)
			}
			if *group.PostPromptsCheck != *c.PostPromptsCheck || string(*group.PromptsCheckMode) != *c.PromptsCheckMode {
				t.Fatalf("canonical detail binding reported check settings (%t, %s) but the fixture declares (%t, %s)",
					*group.PostPromptsCheck, *group.PromptsCheckMode, *c.PostPromptsCheck, *c.PromptsCheckMode)
			}
			encoded, err := json.Marshal(typed)
			if err != nil {
				t.Fatal(err)
			}
			var bound any
			if err := json.Unmarshal(encoded, &bound); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(bound, expected) {
				t.Fatalf("canonical detail binding differs from raw fixture: %s", encoded)
			}
		})
	}
}
