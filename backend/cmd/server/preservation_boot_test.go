package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/storage"
)

func testPreservationKeyring(t *testing.T) *config.TranscriptKeyring {
	t.Helper()
	const encodedTestKEK = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	keyring, err := config.ParseTranscriptKeyring("1", `{"1":"`+encodedTestKEK+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}

func capturePreservationLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &output
}

func TestServeEvaluatesPreservationProofsBeforeServing(t *testing.T) {
	var calls []string
	originalEvaluate := evaluatePreservationProofs
	evaluatePreservationProofs = func() (error, error) {
		calls = append(calls, "evaluate")
		return nil, nil
	}
	t.Cleanup(func() { evaluatePreservationProofs = originalEvaluate })

	serveErr := errors.New("serve seam reached")
	originalServe := startHTTPServing
	startHTTPServing = func(_ context.Context, _ *config.Config, _ *pgxpool.Pool, _ storage.TranscriptBlobStore, _ *redact.TitlePipeline) error {
		calls = append(calls, "serve")
		return serveErr
	}
	t.Cleanup(func() { startHTTPServing = originalServe })

	output := capturePreservationLogs(t)
	cfg := &config.Config{}
	err := dispatchRuntime(context.Background(), runtimeSelection{mode: runtimeModeServe}, cfg, testPreservationKeyring(t), nil, nil, nil)
	if !errors.Is(err, serveErr) {
		t.Fatalf("serve dispatch error=%v, want listener seam error", err)
	}
	if len(calls) != 2 || calls[0] != "evaluate" || calls[1] != "serve" {
		t.Fatalf("serve dispatch order=%v, want [evaluate serve]", calls)
	}
	logged := output.String()
	if !strings.Contains(logged, "msg=preservation_proofs_evaluated") {
		t.Fatalf("serve dispatch did not log preservation verdicts: %q", logged)
	}
	if !strings.Contains(logged, "base_preservation=pass") || !strings.Contains(logged, "provenance_preservation=pass") {
		t.Fatalf("serve dispatch did not log passing verdicts: %q", logged)
	}
}

func TestServeContinuesWhenPreservationProofsFail(t *testing.T) {
	originalEvaluate := evaluatePreservationProofs
	evaluatePreservationProofs = func() (error, error) {
		return errors.New("base preservation unavailable"), errors.New("provenance preservation unavailable")
	}
	t.Cleanup(func() { evaluatePreservationProofs = originalEvaluate })

	serveErr := errors.New("serve seam reached")
	served := false
	originalServe := startHTTPServing
	startHTTPServing = func(_ context.Context, _ *config.Config, _ *pgxpool.Pool, _ storage.TranscriptBlobStore, _ *redact.TitlePipeline) error {
		served = true
		return serveErr
	}
	t.Cleanup(func() { startHTTPServing = originalServe })

	output := capturePreservationLogs(t)
	cfg := &config.Config{}
	err := dispatchRuntime(context.Background(), runtimeSelection{mode: runtimeModeServe}, cfg, testPreservationKeyring(t), nil, nil, nil)
	if !errors.Is(err, serveErr) {
		t.Fatalf("failing proofs aborted boot with error=%v, want listener seam error", err)
	}
	if !served {
		t.Fatal("failing proofs prevented the listener from starting")
	}
	logged := output.String()
	if !strings.Contains(logged, "msg=preservation_proofs_evaluated") {
		t.Fatalf("failing proofs did not log preservation verdicts: %q", logged)
	}
	if !strings.Contains(logged, "base_preservation=fail") || !strings.Contains(logged, "provenance_preservation=fail") {
		t.Fatalf("failing proofs did not log failing verdicts: %q", logged)
	}
}
