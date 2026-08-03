package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/storage_composition/cases.yaml
var storageCompositionCasesYAML []byte

type storageCompositionCase struct {
	Name  string `yaml:"name"`
	Error string `yaml:"error"`
}

func loadStorageCompositionCases(t *testing.T) []storageCompositionCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(storageCompositionCasesYAML))
	decoder.KnownFields(true)
	var cases []storageCompositionCase
	if err := decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("storage composition fixture trailing document: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("storage composition fixture rows=%d, want 1", len(cases))
	}
	return cases
}

func TestStorageConfigurationFailurePreventsDispatch(t *testing.T) {
	for _, tc := range loadStorageCompositionCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			cfg := &config.Config{S3Endpoint: "http://127.0.0.1:9000", S3Bucket: "transcripts", S3AccessKey: "access", S3SecretKey: "secret", S3UsePathStyle: true}
			original := constructObjectStore
			constructObjectStore = func(storage.S3Configuration) (*storage.S3ObjectStore, error) {
				return nil, errors.New("malformed SDK configuration")
			}
			t.Cleanup(func() { constructObjectStore = original })
			started := false
			err := withTranscriptStorage(cfg, nil, func(storage.TranscriptBlobStore) error { started = true; return nil })
			if err == nil || !strings.Contains(err.Error(), tc.Error) {
				t.Fatalf("error=%v, want %q", err, tc.Error)
			}
			if started {
				t.Fatal("mode starter invoked after object-store configuration failure")
			}
			if strings.Contains(output.String(), "transcript_encryption_authority_ready") {
				t.Fatalf("storage composition failure emitted readiness evidence: %q", output.String())
			}
		})
	}
}

func TestTranscriptEncryptionAuthorityReadyEventIsSecretSafe(t *testing.T) {
	const encodedTestKEK = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	const decodedTestKEK = "0123456789abcdef0123456789abcdef"
	keyring, err := config.ParseTranscriptKeyring("1", `{"1":"`+encodedTestKEK+`"}`)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	t.Setenv("VILLAGE_BUILD_REVISION", "ffffffffffffffffffffffffffffffffffffffff")
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	serveErr := errors.New("serve seam reached")
	originalStart := startHTTPServing
	startHTTPServing = func(_ context.Context, _ *config.Config, _ *pgxpool.Pool, blobs storage.TranscriptBlobStore, _ *redact.TitlePipeline) error {
		if blobs == nil {
			t.Fatal("serve dispatch reached listener seam without composed encrypted transcript storage")
		}
		loggedBeforeListener := output.String()
		if !strings.Contains(loggedBeforeListener, "msg=transcript_encryption_authority_ready") ||
			!strings.Contains(loggedBeforeListener, "stage=pre_listener") ||
			!strings.Contains(loggedBeforeListener, "active_key_version=1") ||
			!strings.Contains(loggedBeforeListener, "revision=ffffffffffffffffffffffffffffffffffffffff") {
			t.Fatalf("serve dispatch reached listener seam without readiness evidence: %q", loggedBeforeListener)
		}
		return serveErr
	}
	t.Cleanup(func() { startHTTPServing = originalStart })

	cfg := &config.Config{S3Endpoint: "http://127.0.0.1:9000", S3Bucket: "transcripts", S3AccessKey: "access", S3SecretKey: "secret", S3UsePathStyle: true}
	err = withTranscriptStorage(cfg, keyring, func(blobs storage.TranscriptBlobStore) error {
		titles, titleErr := redact.NewTitlePipeline()
		if titleErr != nil {
			return titleErr
		}
		return dispatchRuntime(context.Background(), runtimeSelection{mode: runtimeModeServe}, cfg, keyring, nil, blobs, titles)
	})
	if !errors.Is(err, serveErr) {
		t.Fatalf("serve dispatch error=%v, want listener seam error", err)
	}
	logged := output.String()
	if strings.Count(logged, "msg=transcript_encryption_authority_ready") != 1 {
		t.Fatalf("readiness event count=%d, want 1: %q", strings.Count(logged, "msg=transcript_encryption_authority_ready"), logged)
	}
	if strings.Contains(logged, encodedTestKEK) ||
		strings.Contains(logged, decodedTestKEK) ||
		strings.Contains(logged, `{"1":"`+encodedTestKEK+`"}`) {
		t.Fatalf("readiness event exposed key material: %q", logged)
	}
}
