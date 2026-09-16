//go:build integration

package handler

// Mounted lifecycle test for the GitHub webhook ledger: it drives the REAL
// ReceiveGitHubWebhook handler, on the same route and method router.New mounts,
// against a real PostgreSQL and the generated queries. This is the seam the
// unit tests structurally cannot reach — a renamed parameter or a changed
// status token keeps a mock-based test green while the real write fails.
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:55432/village_test?sslmode=disable" \
//	  go test -tags=integration ./internal/handler/... -run TestGitHubWebhookLedgerLifecycle

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	gh "github.com/peasant-labs/village/backend/internal/github"
)

// TestGitHubWebhookLedgerLifecycle proves the resume contract end to end: a
// first delivery is handled and its payload recorded, a redelivery of the same
// id is a replay that does not dispatch, a failed attempt leaves the row failed
// for an operator, and a redelivery of it dispatches again and ends handled.
func TestGitHubWebhookLedgerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the mounted webhook ledger test")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Skipf("cannot create pool (%v); set TEST_DATABASE_URL", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("cannot reach test database (%v); set TEST_DATABASE_URL", err)
	}
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	secret := "integration-webhook-secret"
	body := []byte(`{"action":"opened"}`)
	signature := webhookSignature(secret, body)
	handled := "lifecycle-handled-" + uuid.NewString()
	retried := "lifecycle-retried-" + uuid.NewString()
	defer func() {
		if _, err := pool.Exec(ctx, "DELETE FROM github_webhook_deliveries WHERE delivery_id = ANY($1::text[])", []string{handled, retried}); err != nil {
			t.Errorf("cleanup deliveries: %v", err)
		}
	}()

	client, err := gh.NewClient(gh.Config{AppID: "123", PrivateKeyPEM: testAppPEM(t)})
	if err != nil {
		t.Fatalf("gh.NewClient: %v", err)
	}
	recorder := &webhookRecorder{}
	cfg := &config.Config{GitHubAppWebhookSecret: secret}
	h := &Handler{pool: pool, queries: sqlc.New(pool), cfg: cfg, gh: client, githubDispatcher: recorder}

	// The mount is the one router.New uses for this route.
	r := chi.NewRouter()
	r.Post("/api/v1/integrations/github/webhook", h.ReceiveGitHubWebhook)

	post := func(deliveryID, sig string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/github/webhook", bytes.NewReader(body))
		req.Header.Set("X-GitHub-Event", "pull_request")
		req.Header.Set("X-GitHub-Delivery", deliveryID)
		req.Header.Set("X-Hub-Signature-256", sig)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// 1. A first delivery is accepted, dispatched once, and recorded handled
	//    with the event type and exact raw payload.
	if w := post(handled, signature); w.Code != http.StatusAccepted {
		t.Fatalf("first delivery status = %d (%s), want 202", w.Code, w.Body.String())
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("dispatch calls = %v, want one", recorder.calls)
	}
	row := readDeliveryRow(t, ctx, pool, handled)
	if row.status != "handled" || row.attempts != 1 || row.eventType != "pull_request" || !bytes.Equal(row.payload, body) || !row.handledAt {
		t.Fatalf("ledger row = %+v, want handled with the payload and one attempt", row)
	}

	// 2. The same id again is a replay: acknowledged, not dispatched.
	if w := post(handled, signature); w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), "replay") {
		t.Fatalf("redelivery = %d (%s), want a 202 replay", w.Code, w.Body.String())
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("dispatch calls = %v, want still one: a handled delivery is not dispatched again", recorder.calls)
	}

	// 3. A failing attempt is a 500 with the row failed and the error kept.
	recorder.err = errors.New("dispatch failed")
	if w := post(retried, signature); w.Code != http.StatusInternalServerError {
		t.Fatalf("failing delivery status = %d, want 500", w.Code)
	}
	row = readDeliveryRow(t, ctx, pool, retried)
	if row.status != "failed" || row.attempts != 1 || row.lastError != "dispatch failed" || !row.failedAt {
		t.Fatalf("ledger row = %+v, want failed with the error kept", row)
	}

	// 4. Redelivering the failed id dispatches again and ends handled — the
	//    recovery the at-most-once ledger could not perform.
	recorder.err = nil
	if w := post(retried, signature); w.Code != http.StatusAccepted {
		t.Fatalf("resumed delivery status = %d (%s), want 202", w.Code, w.Body.String())
	}
	if len(recorder.calls) != 3 {
		t.Fatalf("dispatch calls = %v, want three: one handled, one failed, one resumed", recorder.calls)
	}
	row = readDeliveryRow(t, ctx, pool, retried)
	if row.status != "handled" || row.attempts != 2 {
		t.Fatalf("ledger row = %+v, want two attempts ending handled", row)
	}

	// 5. A bad signature is refused before anything is recorded or dispatched.
	dispatchBefore := len(recorder.calls)
	badDelivery := "lifecycle-bad-" + uuid.NewString()
	defer func() {
		if _, err := pool.Exec(ctx, "DELETE FROM github_webhook_deliveries WHERE delivery_id = $1", badDelivery); err != nil {
			t.Errorf("cleanup bad delivery: %v", err)
		}
	}()
	if w := post(badDelivery, webhookSignature("wrong-secret", body)); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature status = %d, want 401", w.Code)
	}
	if len(recorder.calls) != dispatchBefore {
		t.Fatal("a bad signature dispatched an event")
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM github_webhook_deliveries WHERE delivery_id = $1", badDelivery).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("a bad signature recorded a delivery")
	}
}

type deliveryLedgerRow struct {
	status    string
	attempts  int32
	eventType string
	payload   []byte
	lastError string
	handledAt bool
	failedAt  bool
}

func readDeliveryRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, deliveryID string) deliveryLedgerRow {
	t.Helper()
	var row deliveryLedgerRow
	var lastError pgtype.Text
	if err := pool.QueryRow(ctx, `
		SELECT status, attempts, event_type, payload, last_error,
		       handled_at IS NOT NULL, failed_at IS NOT NULL
		FROM github_webhook_deliveries WHERE delivery_id = $1
	`, deliveryID).Scan(&row.status, &row.attempts, &row.eventType, &row.payload, &lastError, &row.handledAt, &row.failedAt); err != nil {
		t.Fatalf("read delivery %s: %v", deliveryID, err)
	}
	row.lastError = lastError.String
	return row
}
