package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// TestVerifySignatureFailsClosed pins the trust boundary: only an exact
// HMAC-SHA256 over the raw bytes under a non-empty secret is accepted.
func TestVerifySignatureFailsClosed(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"zen":"non-blocking"}`)
	if !VerifySignature(secret, body, signBody(secret, body)) {
		t.Fatal("a valid signature was rejected")
	}

	for _, rejected := range []struct {
		name   string
		secret string
		body   []byte
		header string
	}{
		{"empty secret", "", body, signBody(secret, body)},
		{"wrong secret", "other", body, signBody(secret, body)},
		{"tampered body", secret, []byte(`{"zen":"mutated"}`), signBody(secret, body)},
		{"missing prefix", secret, body, strings.TrimPrefix(signBody(secret, body), "sha256=")},
		{"non-hex digest", secret, body, "sha256=not-hex"},
		{"wrong scheme", secret, body, "sha1=abcd"},
		{"empty header", secret, body, ""},
		{"prefix only", secret, body, "sha256="},
	} {
		if VerifySignature(rejected.secret, rejected.body, rejected.header) {
			t.Errorf("%s was accepted", rejected.name)
		}
	}
}

type recordingDispatcher struct{ calls []string }

func (d *recordingDispatcher) note(event string) { d.calls = append(d.calls, event) }

func (d *recordingDispatcher) Installation(context.Context, Event) error {
	d.note(EventInstallation)
	return nil
}
func (d *recordingDispatcher) PullRequest(context.Context, Event) error {
	d.note(EventPullRequest)
	return nil
}
func (d *recordingDispatcher) CheckRun(context.Context, Event) error {
	d.note(EventCheckRun)
	return nil
}
func (d *recordingDispatcher) IssueComment(context.Context, Event) error {
	d.note(EventIssueComment)
	return nil
}

// TestDispatchRoutesKnownEventsAndDropsUnknown proves each subscribed type
// reaches exactly its own method and an unsubscribed type reaches none.
func TestDispatchRoutesKnownEventsAndDropsUnknown(t *testing.T) {
	for _, event := range []string{EventInstallation, EventPullRequest, EventCheckRun, EventIssueComment} {
		d := &recordingDispatcher{}
		if err := Dispatch(context.Background(), d, Event{Type: event}); err != nil {
			t.Fatalf("Dispatch(%s): %v", event, err)
		}
		if len(d.calls) != 1 || d.calls[0] != event {
			t.Fatalf("Dispatch(%s) called %v, want exactly [%s]", event, d.calls, event)
		}
	}

	dropped := &recordingDispatcher{}
	if err := Dispatch(context.Background(), dropped, Event{Type: "workflow_run"}); err != nil {
		t.Fatalf("an unsubscribed event must not error: %v", err)
	}
	if len(dropped.calls) != 0 {
		t.Fatalf("an unsubscribed event dispatched %v, want nothing", dropped.calls)
	}
}
