package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/yaml.v3"

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
	return file.Cases
}

type webhookRecorder struct {
	calls []string
	err   error
}

func (r *webhookRecorder) note(name string) error {
	r.calls = append(r.calls, name)
	return r.err
}
func (r *webhookRecorder) Installation(context.Context, gh.Event) error {
	return r.note("installation")
}
func (r *webhookRecorder) PullRequest(context.Context, gh.Event) error { return r.note("pull_request") }
func (r *webhookRecorder) CheckRun(context.Context, gh.Event) error    { return r.note("check_run") }
func (r *webhookRecorder) IssueComment(context.Context, gh.Event) error {
	return r.note("issue_comment")
}

func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// webhookHandler builds a handler whose delivery store is an in-memory set:
// the first record of an id returns 1, every replay returns 0.
func webhookHandler(t *testing.T, secret string, configured bool, dispatch gh.Dispatcher, recorded map[string]int) *Handler {
	t.Helper()
	q := &mockQuerier{recordGitHubWebhookDelivery: func(_ context.Context, id string) (int64, error) {
		if recorded[id] > 0 {
			return 0, nil
		}
		recorded[id] = 1
		return 1, nil
	}}
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
			recorded := map[string]int{}
			h := webhookHandler(t, tc.secret, tc.configured, &webhookRecorder{}, recorded)
			rec := postWebhook(h, "pull_request", "d1", webhookSignature("secret", body), body)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501", rec.Code)
			}
			if len(recorded) != 0 {
				t.Fatal("an unconfigured receiver recorded a delivery")
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
			recorded := map[string]int{}
			recorder := &webhookRecorder{}
			h := webhookHandler(t, secret, true, recorder, recorded)
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
			}
			if recorded["delivery-"+c.Name] != 1 {
				t.Fatalf("delivery was not recorded exactly once (recorded %d times)", recorded["delivery-"+c.Name])
			}
		})
	}
}

// TestReceiveGitHubWebhook_RejectsBadSignature proves an invalid signature is
// refused before anything is recorded or dispatched.
func TestReceiveGitHubWebhook_RejectsBadSignature(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"action":"opened"}`)
	recorded := map[string]int{}
	recorder := &webhookRecorder{}
	h := webhookHandler(t, secret, true, recorder, recorded)
	rec := postWebhook(h, "pull_request", "d1", webhookSignature("wrong-secret", body), body)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("a bad signature dispatched %v", recorder.calls)
	}
	if len(recorded) != 0 {
		t.Fatal("a bad signature recorded a delivery")
	}
}

// TestReceiveGitHubWebhook_ReplayIsNoOp proves a redelivered id is acknowledged
// but dispatched only once.
func TestReceiveGitHubWebhook_ReplayIsNoOp(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"action":"opened"}`)
	recorded := map[string]int{}
	recorder := &webhookRecorder{}
	h := webhookHandler(t, secret, true, recorder, recorded)
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
	if recorded["same-delivery"] != 1 {
		t.Fatalf("delivery recorded %d times, want 1", recorded["same-delivery"])
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
			h := webhookHandler(t, secret, true, &webhookRecorder{}, map[string]int{})
			rec := postWebhook(h, tc.event, tc.delivery, sig, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

// TestReceiveGitHubWebhook_OversizedBody proves the raw body is bounded.
func TestReceiveGitHubWebhook_OversizedBody(t *testing.T) {
	secret := "webhook-secret"
	recorded := map[string]int{}
	h := webhookHandler(t, secret, true, &webhookRecorder{}, recorded)
	body := bytes.Repeat([]byte("a"), maxGitHubWebhookBodyBytes+1)
	rec := postWebhook(h, "pull_request", "d1", webhookSignature(secret, body), body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if len(recorded) != 0 {
		t.Fatal("an oversized body recorded a delivery")
	}
}

// TestReceiveGitHubWebhook_DispatchFailureIsServerError keeps a handler that
// errors from being reported as accepted.
func TestReceiveGitHubWebhook_DispatchFailureIsServerError(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"action":"opened"}`)
	recorder := &webhookRecorder{err: errors.New("dispatch failed")}
	h := webhookHandler(t, secret, true, recorder, map[string]int{})
	rec := postWebhook(h, "pull_request", "d1", webhookSignature(secret, body), body)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
