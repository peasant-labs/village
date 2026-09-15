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

type collectiveDetailWireCase struct {
	Name         string `yaml:"name"`
	Query        string `yaml:"query"`
	ExpectedJSON string `yaml:"expected_json"`
}
type collectiveDetailWireFixtures struct {
	OwnerID   string                     `yaml:"owner_id"`
	PendingID string                     `yaml:"pending_id"`
	GroupID   string                     `yaml:"group_id"`
	OwnerName string                     `yaml:"owner_name"`
	SeedSQL   string                     `yaml:"seed_sql"`
	Cases     []collectiveDetailWireCase `yaml:"cases"`
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
		seen[c.Name] = true
	}
	for _, name := range []string{"legacy-exact-group-record-without-prompts-settings", "grouped-exact-group-record-without-prompts-settings"} {
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
			r := httptest.NewRequest(http.MethodGet, "/api/v1/groups/"+f.GroupID+c.Query, nil)
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
			// The grouped route projects a partial group record and reads no
			// prompts settings, so it must omit them rather than fabricate
			// defaults. The legacy route reads the full row and supplies the
			// real settings; the round-trip comparison below proves that
			// neither route invents or drops a field.
			if c.Query != "" && (group.PostPromptsCheck != nil || group.PromptsCheckMode != nil) {
				t.Fatal("grouped collective detail acquired invented prompts settings")
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
