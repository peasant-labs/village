package handler

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/transcript_lifecycle/cleanup_failures.yaml
var cleanupFailureCasesYAML []byte

type cleanupFailureCase struct {
	Name       string `yaml:"name"`
	Operation  string `yaml:"operation"`
	Completion string `yaml:"completion"`
}

func cleanupOperationFromFixture(value string) (blobCleanupOperation, bool) {
	operation := blobCleanupOperation(value)
	switch operation {
	case cleanupCreateCandidate, cleanupRepublishCandidate, cleanupRepublishSuperseded,
		cleanupRewriteCandidate, cleanupRewriteSuperseded, cleanupDeleteTarget:
		return operation, true
	default:
		return "", false
	}
}

func loadCleanupFailureCases(t *testing.T) []cleanupFailureCase {
	t.Helper()
	var fixture struct {
		Cases []cleanupFailureCase `yaml:"cases"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(cleanupFailureCasesYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode cleanup failure fixtures: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("cleanup failure fixture must contain exactly one YAML document: %v", err)
	}
	if len(fixture.Cases) != 6 {
		t.Fatalf("cleanup failure fixture count=%d, want 6", len(fixture.Cases))
	}
	seen := make(map[blobCleanupOperation]struct{}, len(fixture.Cases))
	for _, tc := range fixture.Cases {
		operation, ok := cleanupOperationFromFixture(tc.Operation)
		if !ok {
			t.Fatalf("fixture operation=%q is unknown", tc.Operation)
		}
		if _, duplicate := seen[operation]; duplicate {
			t.Fatalf("fixture operation=%q appears more than once", tc.Operation)
		}
		seen[operation] = struct{}{}
	}
	return fixture.Cases
}

func TestBlobCleanupBoundaryEmitsSecretSafeReconciliationEvidence(t *testing.T) {
	for _, tc := range loadCleanupFailureCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			operation, ok := cleanupOperationFromFixture(tc.Operation)
			if !ok {
				t.Fatalf("fixture operation=%q is unknown", tc.Operation)
			}
			descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey("transcripts/"+uuid.NewString()+".bin"), []byte("test-wrapped-key"), storage.EncryptionAES256GCMRandomNonceV1, 1)
			if err != nil {
				t.Fatal(err)
			}
			store := &mockTranscriptBlobStore{deleteErr: errors.New("fixture object store unavailable")}
			h := newTestHandler(&mockQuerier{}, store)
			completion := TransactionCommitted
			switch tc.Completion {
			case "committed":
			case "known_rollback":
				completion = TransactionKnownRollback
			default:
				t.Fatalf("fixture completion=%q is unknown", tc.Completion)
			}
			var logs bytes.Buffer
			prior := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(prior) })
			if err := h.deleteBlobForCleanup(context.Background(), operation, toPgUUID(uuid.New()), descriptor, completion); err == nil {
				t.Fatal("cleanup error=nil, want object deletion failure")
			}
			if len(store.deletedKeys) != 1 || store.deletedKeys[0] != string(descriptor.ObjectKey()) {
				t.Fatalf("deleted keys=%v, want descriptor key", store.deletedKeys)
			}
			logLine := logs.String()
			if !strings.Contains(logLine, "transcript_blob_reconciliation_required") ||
				!strings.Contains(logLine, "operation="+tc.Operation) ||
				!strings.Contains(logLine, "transcript_id=") ||
				!strings.Contains(logLine, "object_key=") ||
				!strings.Contains(logLine, "completion="+tc.Completion) ||
				!strings.Contains(logLine, "meaning=") ||
				!strings.Contains(logLine, "remediation=") ||
				strings.Contains(logLine, "test-wrapped-key") ||
				strings.Contains(logLine, "wrapped_data_key") ||
				strings.Contains(logLine, "encryption_algorithm") ||
				strings.Contains(logLine, "key_version") ||
				strings.Contains(logLine, "fixture object store unavailable") {
				t.Fatalf("reconciliation log is missing safe evidence or leaked dependency detail: %q", logLine)
			}
		})
	}
}
