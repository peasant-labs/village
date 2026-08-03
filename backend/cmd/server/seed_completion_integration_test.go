//go:build integration

package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/handler"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/development_seed/completions.yaml
var seedCompletionCasesYAML []byte

type seedCompletionCase struct {
	Name        string `yaml:"name"`
	Completion  string `yaml:"completion"`
	WantDeleted int    `yaml:"want_deleted"`
	WantError   bool   `yaml:"want_error"`
	WantEvent   bool   `yaml:"want_event"`
}

func loadSeedCompletionCases(t *testing.T) []seedCompletionCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(seedCompletionCasesYAML))
	decoder.KnownFields(true)
	var cases []seedCompletionCase
	if err := decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("seed completion fixture trailing document: %v", err)
	}
	if len(cases) != 3 {
		t.Fatalf("seed completion fixture rows=%d, want 3", len(cases))
	}
	return cases
}

type fixedSystemCreator struct{ completion handler.TransactionCompletion }

func (c fixedSystemCreator) CreateTranscriptAsSystemResult(context.Context, sqlc.CreateTranscriptParams) handler.SystemTranscriptCreateResult {
	result := handler.SystemTranscriptCreateResult{Completion: c.completion}
	if c.completion != handler.TransactionCommitted {
		result.Err = errors.New("injected database completion")
	}
	return result
}

type trackingSeedStore struct {
	storage.TranscriptBlobStore
	deleted int
}

func (s *trackingSeedStore) Delete(ctx context.Context, d storage.BlobDescriptor) error {
	s.deleted++
	return s.TranscriptBlobStore.Delete(ctx, d)
}

func TestSeedCompletionFixtureProductionPath(t *testing.T) {
	databaseURL, endpoint := requiredSeedIntegrationEnvironment(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	baseStore := newSeedIntegrationStore(t, databaseURL, endpoint)
	for _, tc := range loadSeedCompletionCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			completion := handler.TransactionCommitted
			switch tc.Completion {
			case "known-rollback":
				completion = handler.TransactionKnownRollback
			case "ambiguous":
				completion = handler.TransactionCommitAmbiguous
			}
			tracked := &trackingSeedStore{TranscriptBlobStore: baseStore}
			var logs bytes.Buffer
			prior := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(prior)
			err := runSeedWithCreator(ctx, runtimeModeSeedCore, pool, tracked, fixedSystemCreator{completion: completion})
			if (err != nil) != tc.WantError {
				t.Fatalf("error=%v want_error=%v", err, tc.WantError)
			}
			if tracked.deleted != tc.WantDeleted {
				t.Fatalf("deleted=%d want=%d", tracked.deleted, tc.WantDeleted)
			}
			hasEvent := strings.Contains(logs.String(), "transcript_blob_reconciliation_required")
			if hasEvent != tc.WantEvent {
				t.Fatalf("event=%v want=%v logs=%q", hasEvent, tc.WantEvent, logs.String())
			}
		})
	}
}

func requiredSeedIntegrationEnvironment(t *testing.T) (string, string) {
	t.Helper()
	databaseURL := getenvRequired(t, "TEST_DATABASE_URL")
	return databaseURL, getenvRequired(t, "TEST_S3_ENDPOINT")
}
func getenvRequired(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		t.Fatalf("%s is required; refusing to skip seed completion proof", key)
	}
	return value
}
func newSeedIntegrationStore(t *testing.T, databaseURL, endpoint string) storage.TranscriptBlobStore {
	t.Helper()
	cfg := &config.Config{DatabaseURL: databaseURL, S3Endpoint: endpoint, S3Bucket: getenvRequired(t, "TEST_S3_BUCKET"), S3AccessKey: getenvRequired(t, "TEST_S3_ACCESS_KEY"), S3SecretKey: getenvRequired(t, "TEST_S3_SECRET_KEY"), S3UsePathStyle: true}
	keys, err := config.ParseTranscriptKeyring("1", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := storage.NewS3ObjectStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewEncryptedTranscriptStore(objects, keys)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
