package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/handler"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/development_seed/profiles.yaml
var seedProfilesYAML []byte

type seedProfile struct {
	Name        string           `yaml:"name"`
	Transcripts []seedTranscript `yaml:"transcripts"`
}
type seedTranscript struct{ ID, OwnerID, LocalID, Title, Visibility, Provider string }

type systemTranscriptCreator interface {
	CreateTranscriptAsSystemResult(context.Context, sqlc.CreateTranscriptParams) handler.SystemTranscriptCreateResult
}

func loadSeedProfiles() ([]seedProfile, error) {
	dec := yaml.NewDecoder(bytes.NewReader(seedProfilesYAML))
	dec.KnownFields(true)
	var profiles []seedProfile
	if err := dec.Decode(&profiles); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("development seed fixture has trailing document: %v", err)
	}
	if len(profiles) != 2 {
		return nil, fmt.Errorf("development seed fixture has %d profiles, want exactly 2", len(profiles))
	}
	return profiles, nil
}

func runSeed(ctx context.Context, mode runtimeMode, cfg *config.Config, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore) error {
	return runSeedWithCreator(ctx, mode, pool, blobs, handler.New(cfg, pool, blobs))
}

func runSeedWithCreator(ctx context.Context, mode runtimeMode, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, creator systemTranscriptCreator) error {
	profiles, err := loadSeedProfiles()
	if err != nil {
		return fmt.Errorf("development seed failed because its strict fixture is invalid in runSeed before persistence; no seed job started; repair the two-profile YAML fixture: %w", err)
	}
	want := "core"
	if mode == runtimeModeSeedPrivacy {
		want = "privacy"
	}
	var selected *seedProfile
	for i := range profiles {
		if profiles[i].Name == want {
			selected = &profiles[i]
		}
	}
	if selected == nil {
		return fmt.Errorf("development seed failed because profile %q is absent in runSeed before persistence; no transcript was written; restore the named profile", want)
	}
	for _, item := range selected.Transcripts {
		id, err := uuid.Parse(item.ID)
		if err != nil {
			return err
		}
		owner, err := uuid.Parse(item.OwnerID)
		if err != nil {
			return err
		}
		githubBase := int64(1000)
		if want == "privacy" {
			githubBase = 2000
		}
		username := "seed-" + item.LocalID
		if _, err = pool.Exec(ctx, "INSERT INTO users (id,github_id,github_username,display_name,provider,provider_user_id) VALUES ($1,$2,$3,$4,'github',$5) ON CONFLICT (id) DO NOTHING", owner, githubBase+int64(owner[15]), username, item.Title, username); err != nil {
			return fmt.Errorf("development seed failed because owner preparation failed in runSeed before encrypted transcript creation; no plaintext object was used; repair PostgreSQL and retry: %w", err)
		}
		body := []byte(fmt.Sprintf(`{"version":"2","session":{"id":%q,"title":%q}}`, item.LocalID, item.Title))
		descriptor, identity, err := blobs.Write(ctx, id, body)
		if err != nil {
			return err
		}
		// The seed body carries no turns, so there is no evidence of who drove
		// these sessions. They take the fail-safe menu value, which lists exactly
		// like a person's session. Every writer of this row must name a menu
		// member: the column's CHECK rejects the Go zero value outright, so a
		// caller cannot leave the question unanswered by accident.
		params := sqlc.CreateTranscriptParams{ID: pgtype.UUID{Bytes: id, Valid: true}, OwnerID: pgtype.UUID{Bytes: owner, Valid: true}, LocalID: item.LocalID, Title: pgtype.Text{String: item.Title, Valid: true}, Visibility: item.Visibility, ModelProvider: item.Provider, BlobKey: string(descriptor.ObjectKey()), BlobSizeBytes: pgtype.Int8{Int64: identity.PlaintextSize(), Valid: true}, SchemaVersion: "2", ContentHash: pgtype.Text{String: string(identity.Hash()), Valid: true}, WrappedDataKey: descriptor.WrappedDEK(), EncryptionAlgorithm: string(descriptor.Algorithm()), KeyVersion: int32(descriptor.KeyVersion()), SessionOrigin: sessionorigin.Unknown.String()}
		result := creator.CreateTranscriptAsSystemResult(ctx, params)
		if result.Completion != handler.TransactionCommitted && result.Err == nil {
			result.Err = errors.New("system transcript persistence returned a non-committed completion without a cause; inspect the persistence implementation")
		}
		switch result.Completion {
		case handler.TransactionCommitted:
			if result.Err != nil {
				return fmt.Errorf("development seed failed because PostgreSQL reported an error with a committed system transcript in runSeedWithCreator after encrypted persistence; the referenced ciphertext was retained and the seed stopped; inspect database health and the wrapped error before retrying: %w", result.Err)
			}
		case handler.TransactionKnownRollback:
			if deleteErr := blobs.Delete(ctx, descriptor); deleteErr != nil {
				return fmt.Errorf("development seed cleanup failed because the known-rollback ciphertext candidate could not be deleted in runSeedWithCreator after database rejection; no row was installed but encrypted orphan storage remains; repair object-store access, delete the opaque candidate, and retry: %w", deleteErr)
			}
			return fmt.Errorf("development seed failed because PostgreSQL proved the system transcript transaction rolled back in runSeedWithCreator after ciphertext upload; the unreferenced candidate was deleted and no transcript was installed; correct the database cause and retry: %w", result.Err)
		case handler.TransactionCommitAmbiguous:
			slog.Error("transcript_blob_reconciliation_required",
				"operation", "development_seed_create",
				"transcript_id", id.String(),
				"object_key", string(descriptor.ObjectKey()),
				"completion", result.Completion,
				"meaning", "ciphertext was retained because the database commit may have installed its descriptor",
				"remediation", "after the transaction recovery window, query the authoritative primary for the opaque object key and delete only when no row or in-flight transaction references it")
			return fmt.Errorf("development seed outcome is ambiguous because PostgreSQL did not confirm the system transcript commit in runSeedWithCreator after ciphertext upload; the candidate was retained because a committed row may reference it; reconcile the opaque object key against the authoritative primary before retrying: %w", result.Err)
		default:
			return fmt.Errorf("development seed failed because system transcript completion %d is unknown in runSeedWithCreator after ciphertext upload; the candidate was conservatively retained because installation cannot be ruled out; upgrade or repair the persistence contract and reconcile the opaque object key before retrying", result.Completion)
		}
	}
	return nil
}
