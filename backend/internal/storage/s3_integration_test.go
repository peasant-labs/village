//go:build integration

package storage

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/peasant-labs/village/backend/internal/config"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/replacement.yaml
var replacementFixtureYAML []byte

type replacementFixture struct {
	Name        string `yaml:"name"`
	InitialKey  string `yaml:"initial_key"`
	UpdatedKey  string `yaml:"updated_key"`
	Initial     string `yaml:"initial"`
	Replacement string `yaml:"replacement"`
	ContentType string `yaml:"content_type"`
}

func TestS3ContentAddressedObjectsRemainIndependent(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI must provide TEST_S3_ENDPOINT and the MinIO service; skipping would disable content-addressed object evidence")
		}
		t.Skip("set TEST_S3_ENDPOINT to run the real S3-compatible replacement test")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(replacementFixtureYAML))
	decoder.KnownFields(true)
	var fixture replacementFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("replacement fixture must contain exactly one document: %v", err)
	}
	if fixture.Name == "" || fixture.InitialKey == "" || fixture.UpdatedKey == "" || fixture.InitialKey == fixture.UpdatedKey || fixture.Initial == fixture.Replacement {
		t.Fatal("replacement fixture row guard failed")
	}
	objects, err := NewS3ObjectStore(&config.Config{S3Endpoint: endpoint, S3AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), S3SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), S3Bucket: os.Getenv("TEST_S3_BUCKET"), S3UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialKey := ObjectKey(fixture.InitialKey)
	updatedKey := ObjectKey(fixture.UpdatedKey)
	defer objects.Delete(ctx, initialKey)
	defer objects.Delete(ctx, updatedKey)
	if err := objects.Put(ctx, initialKey, []byte(fixture.Initial), fixture.ContentType); err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(ctx, updatedKey, []byte(fixture.Replacement), fixture.ContentType); err != nil {
		t.Fatal(err)
	}
	got, _, err := objects.Get(ctx, initialKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != fixture.Initial {
		t.Fatalf("initial content-addressed bytes=%q want=%q", got, fixture.Initial)
	}
}
