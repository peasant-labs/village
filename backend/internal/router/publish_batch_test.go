package router

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/publish_batch_refusal.yaml
var batchRefusalYAML []byte

type batchRefusalCase struct {
	Name          string `yaml:"name"`
	Authenticated bool   `yaml:"authenticated"`
	Body          string `yaml:"body"`
	Status        int    `yaml:"status"`
	Error         string `yaml:"error"`
}

func batchRefusalCases(t *testing.T) []batchRefusalCase {
	t.Helper()
	var fixture struct {
		Cases []batchRefusalCase `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(batchRefusalYAML))
	d.KnownFields(true)
	if err := d.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("batch fixture has trailing document: %v", err)
	}
	names := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || names[c.Name] || c.Error == "" {
			t.Fatalf("invalid batch refusal case %q", c.Name)
		}
		if c.Authenticated && c.Status != http.StatusNotImplemented || !c.Authenticated && c.Status != http.StatusUnauthorized {
			t.Fatalf("case %q changes existing batch auth/refusal policy", c.Name)
		}
		names[c.Name] = true
	}
	for _, name := range strings.Fields("anonymous-refused-before-stub authenticated-empty-batch-stays-unimplemented authenticated-malformed-body-stays-unimplemented") {
		if !names[name] {
			t.Fatalf("missing batch refusal fixture %q", name)
		}
	}
	return fixture.Cases
}

type forbiddenBatchBlobStore struct {
	storage.TranscriptBlobStore
	writes atomic.Int64
}

func (b *forbiddenBatchBlobStore) Write(context.Context, uuid.UUID, []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	b.writes.Add(1)
	return storage.BlobDescriptor{}, storage.ContentIdentity{}, fmt.Errorf("unimplemented batch route must not reach object persistence")
}

// assertRegisteredBatchRefusal drives one case through the production router.
// An authenticated case signs in as account, which must exist in pool: the
// auth middleware confirms a session's account before the route runs.
func assertRegisteredBatchRefusal(t *testing.T, pool *pgxpool.Pool, account uuid.UUID, c batchRefusalCase) *httptest.ResponseRecorder {
	t.Helper()
	cfg := &config.Config{JWTSecret: "batch-route-test-only-secret", FrontendURL: "https://example.test"}
	blobs := &forbiddenBatchBlobStore{}
	routes := New(cfg, pool, blobs, nil)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish/batch", strings.NewReader(c.Body))
	r.Header.Set("Content-Type", "application/json")
	if c.Authenticated {
		token, err := auth.CreateToken(cfg.JWTSecret, account, "batch-refusal")
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, r)
	var response struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != c.Status || response.Error != c.Error {
		t.Fatalf("registered batch response=%d %q want %d %q", w.Code, response.Error, c.Status, c.Error)
	}
	if blobs.writes.Load() != 0 {
		t.Fatal("batch refusal attempted an object write")
	}
	return w
}

func TestRegisteredPublishBatchRefusesBeforePersistence(t *testing.T) {
	for _, c := range batchRefusalCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			// The real router and auth middleware run here with no database. A
			// session's account is confirmed in the database before an
			// auth-required route runs, so with no pool a signed-in case cannot
			// reach the stub and must be refused as unconfirmed (503, retryable).
			// TestRegisteredPublishBatchLeavesDatabaseUnchanged runs the signed-in
			// cases against a real account and asserts their fixture answer.
			if !c.Authenticated {
				assertRegisteredBatchRefusal(t, nil, uuid.Nil, c)
				return
			}
			c.Status = http.StatusServiceUnavailable
			c.Error = "Could not confirm your signed-in account: the account lookup failed before the request ran, so nothing was read or changed. Retry in a few seconds; if it keeps failing, Village's database is unavailable."
			w := assertRegisteredBatchRefusal(t, nil, uuid.New(), c)
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("an unconfirmed session's 503 carries no Retry-After")
			}
		})
	}
}
