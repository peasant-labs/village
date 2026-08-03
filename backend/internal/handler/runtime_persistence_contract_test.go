package handler

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/runtime_persistence/contracts.yaml
var runtimePersistenceContractsYAML []byte

type runtimePersistenceContract struct {
	Name       string `yaml:"name"`
	Kind       string `yaml:"kind"`
	Completion string `yaml:"completion"`
}

type runtimePersistenceContracts struct {
	Cases []runtimePersistenceContract `yaml:"cases"`
}

func loadRuntimePersistenceContracts(t *testing.T) []runtimePersistenceContract {
	t.Helper()
	var fixture runtimePersistenceContracts
	decoder := yaml.NewDecoder(bytes.NewReader(runtimePersistenceContractsYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode runtime persistence contracts: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("runtime persistence fixture must contain exactly one YAML document: %v", err)
	}
	if len(fixture.Cases) != 9 {
		t.Fatalf("runtime persistence fixture count=%d, want 9", len(fixture.Cases))
	}
	return fixture.Cases
}

func TestRuntimePersistenceContracts(t *testing.T) {
	for _, contract := range loadRuntimePersistenceContracts(t) {
		t.Run(contract.Name, func(t *testing.T) {
			switch contract.Kind {
			case "pending_row":
				row := sqlc.ListTranscriptsMissingContentHashRow{ID: toPgUUID(uuid.New()), BlobKey: "transcripts/generation.bin", WrappedDataKey: []byte("wrapped"), EncryptionAlgorithm: "aes-256-gcm-random-nonce-v1", KeyVersion: 2, BlobSizeBytes: pgtype.Int8{}}
				if !row.ID.Valid || row.BlobKey == "" || len(row.WrappedDataKey) == 0 || row.EncryptionAlgorithm == "" || row.KeyVersion <= 0 || row.BlobSizeBytes.Valid {
					t.Fatalf("pending row omitted descriptor or nullable prior size: %+v", row)
				}
			case "rewrap_cursor":
				cursor := sqlc.ListTranscriptDescriptorsForRewrapParams{ActiveKeyVersion: 4, AfterKeyVersion: 2, AfterID: toPgUUID(uuid.New()), BatchSize: 100}
				if cursor.ActiveKeyVersion <= cursor.AfterKeyVersion || !cursor.AfterID.Valid || cursor.BatchSize != 100 {
					t.Fatalf("rewrap cursor does not express bounded (key_version,id) pagination below active: %+v", cursor)
				}
			case "injected_store":
				injected := &mockTranscriptBlobStore{}
				h := New(minimalConfig(), nil, injected)
				if h.blobs != injected {
					t.Fatal("handler did not retain the single injected transcript store")
				}
			case "system_create":
				want := sqlc.Transcript{ID: toPgUUID(uuid.New())}
				q := &mockQuerier{createTranscript: func(context.Context, sqlc.CreateTranscriptParams) (sqlc.Transcript, error) { return want, nil }}
				h := newTestHandler(q, nil)
				got := h.CreateTranscriptAsSystemResult(context.Background(), sqlc.CreateTranscriptParams{})
				if got.Err != nil || got.Completion != TransactionCommitted || got.Row.ID != want.ID {
					t.Fatalf("system create result=%+v, want committed transcript %+v", got, want)
				}
			case "system_create_api":
				assertTypedSystemCreateAPI(t)
			case "system_create_outcome":
				assertSystemCreateOutcome(t, contract)
			case "transaction_cleanup":
				assertTransactionCleanupPolicy(t)
			default:
				t.Fatalf("unknown runtime persistence contract kind %q", contract.Kind)
			}
		})
	}
}

func assertTypedSystemCreateAPI(t *testing.T) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "tx.go", nil, 0)
	if err != nil {
		t.Fatalf("parse production transaction API: %v", err)
	}
	foundTyped := false
	for _, declaration := range file.Decls {
		method, ok := declaration.(*ast.FuncDecl)
		if !ok || method.Recv == nil {
			continue
		}
		if method.Name.Name == "CreateTranscriptAsSystem" {
			t.Fatal("error-only CreateTranscriptAsSystem remains selectable; remove it so callers must inspect typed transaction completion")
		}
		if method.Name.Name != "CreateTranscriptAsSystemResult" {
			continue
		}
		if method.Type.Results == nil || len(method.Type.Results.List) != 1 {
			t.Fatalf("CreateTranscriptAsSystemResult results=%v, want one SystemTranscriptCreateResult", method.Type.Results)
		}
		result, ok := method.Type.Results.List[0].Type.(*ast.Ident)
		if !ok || result.Name != "SystemTranscriptCreateResult" {
			t.Fatalf("CreateTranscriptAsSystemResult return=%T/%v, want SystemTranscriptCreateResult", method.Type.Results.List[0].Type, method.Type.Results.List[0].Type)
		}
		foundTyped = true
	}
	if !foundTyped {
		t.Fatal("typed CreateTranscriptAsSystemResult production boundary is missing")
	}
}

type fixtureTxBeginner struct{ tx pgx.Tx }

func (b fixtureTxBeginner) Begin(context.Context) (pgx.Tx, error) { return b.tx, nil }

type fixtureTx struct {
	pgx.Tx
	commitErr           error
	rollbackErr         error
	rollbackCalls       int
	rollbackHadDeadline bool
}

func (tx *fixtureTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("SELECT 1"), nil
}
func (tx *fixtureTx) Commit(context.Context) error { return tx.commitErr }
func (tx *fixtureTx) Rollback(ctx context.Context) error {
	tx.rollbackCalls++
	deadline, ok := ctx.Deadline()
	tx.rollbackHadDeadline = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 5*time.Second
	if tx.rollbackErr != nil {
		return tx.rollbackErr
	}
	return pgx.ErrTxClosed
}

func assertSystemCreateOutcome(t *testing.T, contract runtimePersistenceContract) {
	t.Helper()
	wantRow := sqlc.Transcript{ID: toPgUUID(uuid.New())}
	tx := &fixtureTx{}
	create := func(Querier) (sqlc.Transcript, error) { return wantRow, nil }
	wantCompletion := TransactionCommitted
	wantErr := false
	switch contract.Completion {
	case "committed":
	case "rollback":
		create = func(Querier) (sqlc.Transcript, error) {
			return sqlc.Transcript{}, errors.New("fixture mutation rejected")
		}
		wantCompletion, wantErr = TransactionKnownRollback, true
	case "ambiguous":
		tx.commitErr = errors.New("fixture connection lost after commit request")
		wantCompletion, wantErr = TransactionCommitAmbiguous, true
	default:
		t.Fatalf("unknown completion %q", contract.Completion)
	}
	h := newTestHandler(&mockQuerier{}, nil)
	got := h.createTranscriptAsSystem(context.Background(), fixtureTxBeginner{tx: tx}, sqlc.CreateTranscriptParams{}, create)
	if got.Completion != wantCompletion || (got.Err != nil) != wantErr {
		t.Fatalf("completion=%v err=%v, want completion=%v hasErr=%v", got.Completion, got.Err, wantCompletion, wantErr)
	}
	if contract.Completion == "committed" && got.Row.ID != wantRow.ID {
		t.Fatalf("committed row=%+v, want %+v", got.Row, wantRow)
	}
}

func assertTransactionCleanupPolicy(t *testing.T) {
	t.Helper()
	tx := &fixtureTx{rollbackErr: errors.New("fixture rollback unavailable")}
	h := newTestHandler(&mockQuerier{}, nil)
	var logs bytes.Buffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prior) })
	got := h.createTranscriptAsSystem(context.Background(), fixtureTxBeginner{tx: tx}, sqlc.CreateTranscriptParams{}, func(Querier) (sqlc.Transcript, error) {
		return sqlc.Transcript{}, errors.New("fixture mutation rejected")
	})
	if got.Completion != TransactionKnownRollback || got.Err == nil {
		t.Fatalf("cleanup fixture completion=%v err=%v, want known rollback", got.Completion, got.Err)
	}
	if tx.rollbackCalls != 1 || !tx.rollbackHadDeadline {
		t.Fatalf("rollback calls=%d bounded=%v, want one rollback with a live deadline no later than five seconds", tx.rollbackCalls, tx.rollbackHadDeadline)
	}
	logLine := logs.String()
	if !strings.Contains(logLine, "handler transaction cleanup failed") ||
		!strings.Contains(logLine, "operation=encrypted_transcript_mutation") ||
		!strings.Contains(logLine, "stage=rollback") ||
		!strings.Contains(logLine, "consequence=") ||
		!strings.Contains(logLine, "remediation=") ||
		!strings.Contains(logLine, "transaction rollback failed: fixture rollback unavailable") {
		t.Fatalf("rollback cleanup log is missing required structured evidence: %q", logLine)
	}
}
