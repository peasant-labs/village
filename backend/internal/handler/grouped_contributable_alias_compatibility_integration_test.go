//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/grouped-contributable-alias-compatibility.yaml
var groupedContributableAliasCompatibilityYAML []byte

type groupedContributableAliasCase struct {
	Name      string `yaml:"name"`
	Prefix    string `yaml:"prefix"`
	Required  string `yaml:"required"`
	Forbidden string `yaml:"forbidden"`
}

// Route compatibility must preserve the released old parser's actual envelope,
// not merely decode both responses through the newest Go struct whose tag
// changed. Each family uses its explicit shared envelope, not a private adapter.
func TestGroupedContributableAliasWireCompatibility(t *testing.T) {
	cases, err := decodeFixtureRows[groupedContributableAliasCase](groupedContributableAliasCompatibilityYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"legacy-grouped-envelope-stays-groupId": false, "canonical-grouped-envelope-uses-group_id": false}
	for _, c := range cases {
		if _, ok := required[c.Name]; !ok || required[c.Name] {
			t.Fatalf("unknown/repeated compatibility case %q", c.Name)
		}
		required[c.Name] = true
	}
	for name, present := range required {
		if !present {
			t.Fatalf("missing required compatibility case %q", name)
		}
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	w := newCollectiveWorld(t, ctx, pool, collectiveWorldSpec{caseName: "grouped-alias-wire", dataAccess: "public", acceptanceMode: "open", discoverable: true, visibility: "public", transcripts: 0})
	defer w.cleanup(t, ctx)
	const secret = "disposable-grouped-alias-secret"
	h := New(&config.Config{JWTSecret: secret}, pool, nil)
	r := chi.NewRouter()
	r.Route("/api/v1", h.RegisterCollectiveBrowseRoutes)
	token, err := auth.CreateToken(secret, uuid.UUID(w.owner.Bytes), w.ownerName)
	if err != nil {
		t.Fatal(err)
	}
	var observedList json.RawMessage
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1"+c.Prefix+"/"+w.groupID()+"/contributable?view=grouped&page=1&limit=2", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("grouped contributable status %d: %s", rec.Code, rec.Body.String())
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			var id string
			if err := json.Unmarshal(raw[c.Required], &id); err != nil || id != w.groupID() {
				t.Fatalf("%s must preserve %s=%s for its released parser; actual body %s", c.Prefix, c.Required, w.groupID(), rec.Body.String())
			}
			if _, ok := raw[c.Forbidden]; ok {
				t.Fatalf("%s emitted incompatible field %s", c.Prefix, c.Forbidden)
			}
			if _, ok := raw["transcriptList"]; !ok {
				t.Fatal("grouped response lost transcriptList")
			}
			for key := range raw {
				if key != c.Required && key != "transcriptList" {
					t.Fatalf("unexpected envelope field %s", key)
				}
			}
			if observedList != nil && !bytes.Equal(observedList, raw["transcriptList"]) {
				t.Fatalf("resource family changed authorized list payload: %s / %s", observedList, raw["transcriptList"])
			}
			observedList = append(json.RawMessage(nil), raw["transcriptList"]...)
		})
	}
}
