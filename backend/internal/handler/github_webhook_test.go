package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	gh "github.com/peasant-labs/village/backend/internal/github"
)

//go:embed testdata/github_webhook/cases.yaml
var githubWebhookCasesYAML []byte

type githubWebhookCase struct {
	Name     string `yaml:"name"`
	Event    string `yaml:"event"`
	Dispatch string `yaml:"dispatch"`
}

type githubWebhookCaseFile struct {
	Cases []githubWebhookCase `yaml:"cases"`
}

// requiredGitHubWebhookCaseNames is the name manifest for
// testdata/github_webhook/cases.yaml: one row per subscribed event type plus one
// unsubscribed type. Exact membership, never a count, so deleting an event row
// fails by name instead of silently shrinking the dispatch corpus.
var requiredGitHubWebhookCaseNames = []string{
	"installation_created",
	"pull_request_opened",
	"check_run_requested_action",
	"issue_comment_created",
	"unrecognized_event_acknowledged",
}

// loadGitHubWebhookCases reads the fixture strictly and refuses a row whose
// dispatch target is neither a subscribed method nor "none".
func loadGitHubWebhookCases(t *testing.T) []githubWebhookCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(githubWebhookCasesYAML))
	decoder.KnownFields(true)
	var file githubWebhookCaseFile
	if err := decoder.Decode(&file); err != nil {
		t.Fatalf("decode the github webhook fixture: %v", err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("the github webhook fixture is empty")
	}
	seen := map[string]bool{}
	for _, c := range file.Cases {
		if c.Name == "" {
			t.Fatal("every github webhook fixture row requires a name")
		}
		if seen[c.Name] {
			t.Fatalf("the github webhook fixture repeats %q", c.Name)
		}
		seen[c.Name] = true
		switch c.Dispatch {
		case "installation", "pull_request", "check_run", "issue_comment", "none":
		default:
			t.Fatalf("fixture row %q declares dispatch %q, which is not a dispatcher method or none", c.Name, c.Dispatch)
		}
	}
	declared := make(map[string]bool, len(requiredGitHubWebhookCaseNames))
	for _, name := range requiredGitHubWebhookCaseNames {
		declared[name] = true
	}
	var missing, undeclared []string
	for _, name := range requiredGitHubWebhookCaseNames {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	for name := range seen {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Fatalf("testdata/github_webhook/cases.yaml no longer carries %v, which its manifest declares: each row pins one event's dispatch. Restore the row under its exact name.", missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/github_webhook/cases.yaml carries %v, which its manifest does not declare: an undeclared case is unprotected, so add each new name to the manifest in the same change.", undeclared)
	}
	return file.Cases
}

type webhookRecorder struct {
	calls  []string
	events []gh.Event
	err    error
}

func (r *webhookRecorder) note(name string, event gh.Event) error {
	r.calls = append(r.calls, name)
	r.events = append(r.events, event)
	return r.err
}
func (r *webhookRecorder) Installation(_ context.Context, event gh.Event) error {
	return r.note("installation", event)
}
func (r *webhookRecorder) PullRequest(_ context.Context, event gh.Event) error {
	return r.note("pull_request", event)
}
func (r *webhookRecorder) CheckRun(_ context.Context, event gh.Event) error {
	return r.note("check_run", event)
}
func (r *webhookRecorder) IssueComment(_ context.Context, event gh.Event) error {
	return r.note("issue_comment", event)
}

func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// webhookLedger is an in-memory stand-in for the delivery ledger, so the
// receiver's record, resume, and replay branches can be exercised without
// PostgreSQL. It keeps the same contract as the generated queries: recording an
// existing delivery leaves its row and status alone, and completing one counts
// the attempt.
type webhookLedger struct {
	rows map[string]*webhookLedgerRow
}

type webhookLedgerRow struct {
	eventType string
	payload   []byte
	status    string
	attempts  int
	lastError string
}

func newWebhookLedger() *webhookLedger {
	return &webhookLedger{rows: map[string]*webhookLedgerRow{}}
}

func (l *webhookLedger) record(_ context.Context, arg sqlc.RecordGitHubWebhookDeliveryParams) (sqlc.RecordGitHubWebhookDeliveryRow, error) {
	if row, ok := l.rows[arg.DeliveryID]; ok {
		return sqlc.RecordGitHubWebhookDeliveryRow{Status: row.status, Attempts: int32(row.attempts)}, nil
	}
	l.rows[arg.DeliveryID] = &webhookLedgerRow{eventType: arg.EventType, payload: arg.Payload, status: webhookDeliveryPending}
	return sqlc.RecordGitHubWebhookDeliveryRow{Status: webhookDeliveryPending}, nil
}

func (l *webhookLedger) complete(_ context.Context, arg sqlc.CompleteGitHubWebhookDeliveryParams) error {
	row, ok := l.rows[arg.DeliveryID]
	if !ok {
		return fmt.Errorf("no delivery %q in the ledger", arg.DeliveryID)
	}
	row.status = arg.Status
	row.attempts++
	if arg.LastError.Valid {
		row.lastError = arg.LastError.String
	}
	return nil
}

func (l *webhookLedger) row(deliveryID string) *webhookLedgerRow {
	return l.rows[deliveryID]
}

// webhookHandler builds a handler whose delivery store is the in-memory ledger.
func webhookHandler(t *testing.T, secret string, configured bool, dispatch gh.Dispatcher, ledger *webhookLedger) *Handler {
	t.Helper()
	q := &mockQuerier{
		recordGitHubWebhookDelivery:   ledger.record,
		completeGitHubWebhookDelivery: ledger.complete,
	}
	h := newTestHandler(q, nil)
	if configured {
		client, err := gh.NewClient(gh.Config{AppID: "123", PrivateKeyPEM: testAppPEM(t)})
		if err != nil {
			t.Fatalf("gh.NewClient: %v", err)
		}
		h.gh = client
	}
	h.cfg.GitHubAppWebhookSecret = secret
	h.githubDispatcher = dispatch
	return h
}

func postWebhook(handler *Handler, event, delivery, signature string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/github/webhook", bytes.NewReader(body))
	if event != "" {
		req.Header.Set("X-GitHub-Event", event)
	}
	if delivery != "" {
		req.Header.Set("X-GitHub-Delivery", delivery)
	}
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	rec := httptest.NewRecorder()
	handler.ReceiveGitHubWebhook(rec, req)
	return rec
}

// TestReceiveGitHubWebhook_NotConfigured is the fail-closed path: with no App
// client or no secret the route answers 501 and touches nothing.
func TestReceiveGitHubWebhook_NotConfigured(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	for _, tc := range []struct {
		name       string
		configured bool
		secret     string
	}{
		{"no app", false, "secret"},
		{"no secret", true, ""},
		{"neither", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ledger := newWebhookLedger()
			recorder := &webhookRecorder{}
			h := webhookHandler(t, tc.secret, tc.configured, recorder, ledger)
			rec := postWebhook(h, "pull_request", "d1", webhookSignature("secret", body), body)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501", rec.Code)
			}
			if len(ledger.rows) != 0 {
				t.Fatal("an unconfigured receiver recorded a delivery")
			}
			if len(recorder.calls) != 0 {
				t.Fatalf("an unconfigured receiver dispatched %v, want nothing", recorder.calls)
			}
		})
	}
}

// TestReceiveGitHubWebhook_DispatchesEachEvent drives every subscribed event
// type plus one unsubscribed type and proves the delivery reached the right
// handling point exactly once.
func TestReceiveGitHubWebhook_DispatchesEachEvent(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"action":"opened"}`)
	for _, c := range loadGitHubWebhookCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			ledger := newWebhookLedger()
			recorder := &webhookRecorder{}
			h := webhookHandler(t, secret, true, recorder, ledger)
			rec := postWebhook(h, c.Event, "delivery-"+c.Name, webhookSignature(secret, body), body)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d (%s), want 202", rec.Code, rec.Body.String())
			}
			if c.Dispatch == "none" {
				if len(recorder.calls) != 0 {
					t.Fatalf("an unsubscribed event dispatched %v, want nothing", recorder.calls)
				}
			} else if len(recorder.calls) != 1 || recorder.calls[0] != c.Dispatch {
				t.Fatalf("dispatch = %v, want exactly [%s]", recorder.calls, c.Dispatch)
			} else {
				// The handling point receives the whole event, so a lost or
				// mutated delivery id or payload cannot pass on the method name
				// alone.
				got := recorder.events[0]
				if got.Type != c.Event || got.DeliveryID != "delivery-"+c.Name || !bytes.Equal(got.Payload, body) {
					t.Fatalf("dispatched event = %+v, want type %q, delivery %q, and the exact raw body", got, c.Event, "delivery-"+c.Name)
				}
			}
			row := ledger.row("delivery-" + c.Name)
			if row == nil || row.status != webhookDeliveryHandled || row.eventType != c.Event || !bytes.Equal(row.payload, body) {
				t.Fatalf("ledger row = %+v, want the delivery handled with its event type and exact raw payload", row)
			}
		})
	}
}

// TestReceiveGitHubWebhook_RejectsBadSignature proves an invalid signature is
// refused before anything is recorded or dispatched.
func TestReceiveGitHubWebhook_RejectsBadSignature(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"action":"opened"}`)
	ledger := newWebhookLedger()
	recorder := &webhookRecorder{}
	h := webhookHandler(t, secret, true, recorder, ledger)
	rec := postWebhook(h, "pull_request", "d1", webhookSignature("wrong-secret", body), body)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("a bad signature dispatched %v", recorder.calls)
	}
	if len(ledger.rows) != 0 {
		t.Fatal("a bad signature recorded a delivery")
	}
}

// TestReceiveGitHubWebhook_ReplayIsNoOp proves a redelivered id is acknowledged
// but dispatched only once.
func TestReceiveGitHubWebhook_ReplayIsNoOp(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"action":"opened"}`)
	ledger := newWebhookLedger()
	recorder := &webhookRecorder{}
	h := webhookHandler(t, secret, true, recorder, ledger)
	sig := webhookSignature(secret, body)

	first := postWebhook(h, "pull_request", "same-delivery", sig, body)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first delivery status = %d, want 202", first.Code)
	}
	second := postWebhook(h, "pull_request", "same-delivery", sig, body)
	if second.Code != http.StatusAccepted {
		t.Fatalf("replay status = %d, want 202", second.Code)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("delivery dispatched %d times, want 1", len(recorder.calls))
	}
	if row := ledger.row("same-delivery"); row == nil || row.status != webhookDeliveryHandled || row.attempts != 1 {
		t.Fatalf("ledger row = %+v, want one handled attempt", row)
	}
}

// TestReceiveGitHubWebhook_RequiresHeaders proves the routing headers are
// required after a valid signature.
func TestReceiveGitHubWebhook_RequiresHeaders(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"action":"opened"}`)
	sig := webhookSignature(secret, body)
	for _, tc := range []struct{ name, event, delivery string }{
		{"missing event", "", "d1"},
		{"missing delivery", "pull_request", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ledger := newWebhookLedger()
			recorder := &webhookRecorder{}
			h := webhookHandler(t, secret, true, recorder, ledger)
			rec := postWebhook(h, tc.event, tc.delivery, sig, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if len(ledger.rows) != 0 {
				t.Fatal("a request missing a routing header recorded a delivery")
			}
			if len(recorder.calls) != 0 {
				t.Fatalf("a request missing a routing header dispatched %v, want nothing", recorder.calls)
			}
		})
	}
}

// TestReceiveGitHubWebhook_OversizedBody proves the raw body is bounded.
func TestReceiveGitHubWebhook_OversizedBody(t *testing.T) {
	secret := "webhook-secret"
	ledger := newWebhookLedger()
	recorder := &webhookRecorder{}
	h := webhookHandler(t, secret, true, recorder, ledger)
	body := bytes.Repeat([]byte("a"), maxGitHubWebhookBodyBytes+1)
	rec := postWebhook(h, "pull_request", "d1", webhookSignature(secret, body), body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if len(ledger.rows) != 0 {
		t.Fatal("an oversized body recorded a delivery")
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("an oversized body dispatched %v, want nothing", recorder.calls)
	}
}

// TestReceiveGitHubWebhook_DispatchFailureIsServerError keeps a handler that
// errors from being reported as accepted, and records the attempt as failed.
func TestReceiveGitHubWebhook_DispatchFailureIsServerError(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"action":"opened"}`)
	recorder := &webhookRecorder{err: errors.New("dispatch failed")}
	ledger := newWebhookLedger()
	h := webhookHandler(t, secret, true, recorder, ledger)
	rec := postWebhook(h, "pull_request", "d1", webhookSignature(secret, body), body)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	row := ledger.row("d1")
	if row == nil || row.status != webhookDeliveryFailed || row.attempts != 1 {
		t.Fatalf("ledger row = %+v, want a failed attempt", row)
	}
	if row.lastError != "dispatch failed" {
		t.Errorf("last_error = %q, want the dispatcher error kept for an operator", row.lastError)
	}
}

// TestWebhookDeliveryStatusConstantsMatchTheLedgerMenu pins the Go constants to
// the three literal tokens migration 040's CHECK accepts (which that migration's
// structure test asserts independently). A rename on either side then fails a
// test instead of reaching production as a CHECK violation and a 500.
func TestWebhookDeliveryStatusConstantsMatchTheLedgerMenu(t *testing.T) {
	if webhookDeliveryPending != "pending" || webhookDeliveryHandled != "handled" || webhookDeliveryFailed != "failed" {
		t.Fatalf("status constants = %q/%q/%q, want pending/handled/failed to match the ledger CHECK",
			webhookDeliveryPending, webhookDeliveryHandled, webhookDeliveryFailed)
	}
}

// TestReceiveGitHubWebhook_FailedDeliveryResumesOnRedelivery is the regression
// for the old at-most-once ledger: a delivery whose attempt failed must be
// dispatched again when GitHub redelivers it, because a redelivery is the only
// recovery GitHub offers, and must then be handled.
func TestReceiveGitHubWebhook_FailedDeliveryResumesOnRedelivery(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"action":"opened"}`)
	recorder := &webhookRecorder{err: errors.New("dispatch failed")}
	ledger := newWebhookLedger()
	h := webhookHandler(t, secret, true, recorder, ledger)
	sig := webhookSignature(secret, body)

	first := postWebhook(h, "pull_request", "retry-me", sig, body)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first attempt status = %d, want 500", first.Code)
	}
	if row := ledger.row("retry-me"); row == nil || row.status != webhookDeliveryFailed {
		t.Fatalf("ledger row after failure = %+v, want failed", row)
	}

	recorder.err = nil
	second := postWebhook(h, "pull_request", "retry-me", sig, body)
	if second.Code != http.StatusAccepted {
		t.Fatalf("redelivery status = %d (%s), want 202", second.Code, second.Body.String())
	}
	if len(recorder.calls) != 2 {
		t.Fatalf("dispatch called %d times, want 2: the failed attempt then the resumed one", len(recorder.calls))
	}
	if row := ledger.row("retry-me"); row == nil || row.status != webhookDeliveryHandled || row.attempts != 2 {
		t.Fatalf("ledger row after resume = %+v, want two attempts ending handled", row)
	}

	// And once handled, a third delivery is a replay again.
	third := postWebhook(h, "pull_request", "retry-me", sig, body)
	if third.Code != http.StatusAccepted || !strings.Contains(third.Body.String(), "replay") {
		t.Fatalf("third delivery = %d (%s), want a 202 replay", third.Code, third.Body.String())
	}
	if len(recorder.calls) != 2 {
		t.Fatalf("dispatch called %d times, want 2: a handled delivery is not dispatched again", len(recorder.calls))
	}
}
