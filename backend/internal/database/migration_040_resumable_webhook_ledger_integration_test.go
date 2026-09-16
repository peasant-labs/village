//go:build integration

package database

import (
	"bytes"
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// TestMigration040DeliveryLedgerTransitions proves the resumable ledger against
// real PostgreSQL: a delivery records as pending with its payload, an attempt
// counts when it ends, a handled delivery records as a replay, and a failed one
// is a candidate to resume rather than a tombstone.
//
// The old shape could not express the last of these, which is the whole point of
// the migration: a failed handling had no state to be resumed from.
func TestMigration040DeliveryLedgerTransitions(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	queries := sqlc.New(pool)

	payload := []byte(`{"action":"opened"}`)

	// A first delivery records pending, with the type and payload a resume needs.
	first, err := queries.RecordGitHubWebhookDelivery(ctx, sqlc.RecordGitHubWebhookDeliveryParams{
		DeliveryID: "delivery-1",
		EventType:  "pull_request",
		Payload:    payload,
	})
	if err != nil {
		t.Fatalf("record new delivery: %v", err)
	}
	if first.Status != "pending" || first.Attempts != 0 {
		t.Fatalf("new delivery = %+v, want pending with no attempts", first)
	}

	// Recording the same id again leaves the row alone: still pending, so a
	// redelivery is a resume rather than a replay.
	again, err := queries.RecordGitHubWebhookDelivery(ctx, sqlc.RecordGitHubWebhookDeliveryParams{
		DeliveryID: "delivery-1",
		EventType:  "pull_request",
		Payload:    payload,
	})
	if err != nil {
		t.Fatalf("record existing delivery: %v", err)
	}
	if again.Status != "pending" || again.Attempts != 0 {
		t.Fatalf("re-recorded delivery = %+v, want the existing pending row", again)
	}

	// Completing an attempt as handled counts it and stamps handled_at.
	if err := queries.CompleteGitHubWebhookDelivery(ctx, sqlc.CompleteGitHubWebhookDeliveryParams{
		DeliveryID: "delivery-1",
		Status:     "handled",
	}); err != nil {
		t.Fatalf("complete handled: %v", err)
	}
	assertDeliveryRow(t, ctx, pool, "delivery-1", deliveryRow{
		status: "handled", attempts: 1, payload: payload,
		handledAt: true, failedAt: false,
	})

	// A handled delivery now records as a replay.
	replay, err := queries.RecordGitHubWebhookDelivery(ctx, sqlc.RecordGitHubWebhookDeliveryParams{
		DeliveryID: "delivery-1",
		EventType:  "pull_request",
		Payload:    payload,
	})
	if err != nil {
		t.Fatalf("record handled delivery: %v", err)
	}
	if replay.Status != "handled" || replay.Attempts != 1 {
		t.Fatalf("handled redelivery = %+v, want handled with one attempt", replay)
	}

	// A failed attempt keeps the error and stays resumable.
	if _, err := queries.RecordGitHubWebhookDelivery(ctx, sqlc.RecordGitHubWebhookDeliveryParams{
		DeliveryID: "delivery-2",
		EventType:  "issue_comment",
		Payload:    payload,
	}); err != nil {
		t.Fatalf("record second delivery: %v", err)
	}
	if err := queries.CompleteGitHubWebhookDelivery(ctx, sqlc.CompleteGitHubWebhookDeliveryParams{
		DeliveryID: "delivery-2",
		Status:     "failed",
		LastError:  pgtype.Text{String: "dispatch failed", Valid: true},
	}); err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	assertDeliveryRow(t, ctx, pool, "delivery-2", deliveryRow{
		status: "failed", attempts: 1, payload: payload,
		handledAt: false, failedAt: true, lastError: "dispatch failed",
	})

	resume, err := queries.RecordGitHubWebhookDelivery(ctx, sqlc.RecordGitHubWebhookDeliveryParams{
		DeliveryID: "delivery-2",
		EventType:  "issue_comment",
		Payload:    payload,
	})
	if err != nil {
		t.Fatalf("record failed delivery for resume: %v", err)
	}
	if resume.Status != "failed" {
		t.Fatalf("failed redelivery = %+v, want failed so the receiver resumes it", resume)
	}

	// The resumed attempt ends handled, counting a second attempt and clearing
	// the error an operator was looking at.
	if err := queries.CompleteGitHubWebhookDelivery(ctx, sqlc.CompleteGitHubWebhookDeliveryParams{
		DeliveryID: "delivery-2",
		Status:     "handled",
	}); err != nil {
		t.Fatalf("complete resumed attempt: %v", err)
	}
	assertDeliveryRow(t, ctx, pool, "delivery-2", deliveryRow{
		status: "handled", attempts: 2, payload: payload,
		handledAt: true, failedAt: true,
	})

	// The status menu is enforced by the database, not only by Go.
	if _, err := pool.Exec(ctx,
		`UPDATE github_webhook_deliveries SET status = 'bogus' WHERE delivery_id = 'delivery-1'`); err == nil {
		t.Fatal("the ledger accepted a status outside its menu")
	}
}

type deliveryRow struct {
	status    string
	attempts  int32
	payload   []byte
	handledAt bool
	failedAt  bool
	lastError string
}

func assertDeliveryRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, deliveryID string, want deliveryRow) {
	t.Helper()
	var got deliveryRow
	var lastError pgtype.Text
	if err := pool.QueryRow(ctx, `
		SELECT status, attempts, payload, handled_at IS NOT NULL, failed_at IS NOT NULL, last_error
		FROM github_webhook_deliveries WHERE delivery_id = $1
	`, deliveryID).Scan(&got.status, &got.attempts, &got.payload, &got.handledAt, &got.failedAt, &lastError); err != nil {
		t.Fatalf("read delivery %s: %v", deliveryID, err)
	}
	got.lastError = lastError.String
	if got.status != want.status || got.attempts != want.attempts || !bytes.Equal(got.payload, want.payload) ||
		got.handledAt != want.handledAt || got.failedAt != want.failedAt || got.lastError != want.lastError {
		t.Fatalf("delivery %s = %+v, want %+v", deliveryID, got, want)
	}
}
