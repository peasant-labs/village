//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/storage"
)

func TestDevelopmentSeedProfilesRealPostgresMinIO(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if databaseURL == "" || endpoint == "" {
		t.Fatal("TEST_DATABASE_URL and TEST_S3_ENDPOINT are required; refusing to skip production seed proof")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err = database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DatabaseURL: databaseURL, S3Endpoint: endpoint, S3Bucket: os.Getenv("TEST_S3_BUCKET"), S3AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), S3SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), S3UsePathStyle: true}
	keys, err := config.ParseTranscriptKeyring("1", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := storage.NewS3ObjectStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := storage.NewEncryptedTranscriptStore(objects, keys)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := loadSeedProfiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		mode := runtimeModeSeedCore
		if profile.Name == "privacy" {
			mode = runtimeModeSeedPrivacy
		}
		if err = runSeed(ctx, mode, cfg, pool, blobs); err != nil {
			t.Fatal(err)
		}
		for _, item := range profile.Transcripts {
			id := uuid.MustParse(item.ID)
			var key string
			var wrapped []byte
			var hash string
			var size int64
			if err = pool.QueryRow(ctx, "SELECT blob_key,wrapped_data_key,content_hash,blob_size_bytes FROM transcripts WHERE id=$1", id).Scan(&key, &wrapped, &hash, &size); err != nil {
				t.Fatal(err)
			}
			raw, contentType, err := objects.Get(ctx, storage.ObjectKey(key))
			if err != nil {
				t.Fatal(err)
			}
			if contentType != "application/octet-stream" || json.Valid(raw) || len(wrapped) == 0 || len(hash) != 64 || size <= 0 {
				t.Fatalf("profile %s transcript %s is not encrypted with complete descriptor/identity", profile.Name, item.ID)
			}
		}
	}
}
