package email

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const testSigningKey = "test-signing-key-test-signing-key-0123"

func TestUnsubscribeTokenRoundTrip(t *testing.T) {
	signer := NewLinkSigner(testSigningKey)
	account := uuid.New()
	now := time.Unix(1_800_000_000, 0)
	token := signer.UnsubscribeToken(account, now.Add(time.Hour))
	got, err := signer.VerifyUnsubscribe(token, now)
	if err != nil || got != account {
		t.Fatalf("verify = %v, %v; want %v", got, err, account)
	}
}

func TestUnsubscribeTokenRefusesExpiredForgedAndMalformed(t *testing.T) {
	signer := NewLinkSigner(testSigningKey)
	account := uuid.New()
	now := time.Unix(1_800_000_000, 0)
	token := signer.UnsubscribeToken(account, now.Add(time.Hour))

	if _, err := signer.VerifyUnsubscribe(token, now.Add(time.Hour)); !errors.Is(err, ErrInvalidLinkToken) {
		t.Fatalf("an expired token verified: %v", err)
	}
	if _, err := NewLinkSigner(testSigningKey+"x").VerifyUnsubscribe(token, now); !errors.Is(err, ErrInvalidLinkToken) {
		t.Fatalf("a token signed with another key verified: %v", err)
	}
	// Swap the account inside the payload and keep the signature.
	other := NewLinkSigner(testSigningKey).UnsubscribeToken(uuid.New(), now.Add(time.Hour))
	forged := strings.SplitN(other, ".", 2)[0] + "." + strings.SplitN(token, ".", 2)[1]
	if _, err := signer.VerifyUnsubscribe(forged, now); !errors.Is(err, ErrInvalidLinkToken) {
		t.Fatalf("a payload with another account's signature verified: %v", err)
	}
	if _, err := signer.VerifyUnsubscribe("not-a-token", now); !errors.Is(err, ErrInvalidLinkToken) {
		t.Fatalf("a malformed token verified: %v", err)
	}
	if _, err := (LinkSigner{}).VerifyUnsubscribe(token, now); !errors.Is(err, ErrInvalidLinkToken) {
		t.Fatalf("a signer with no key verified a token: %v", err)
	}
}

func TestSettingsProblemsNameVariablesOnly(t *testing.T) {
	full := Settings{ProviderAPIKey: "re_secret_value", From: "village <v@example.org>", LinkSigningKey: testSigningKey}
	if problems := full.Problems(); len(problems) != 0 || full.Unset() {
		t.Fatalf("a full configuration reports %v", problems)
	}
	if !(Settings{}).Unset() || len((Settings{}).Problems()) != 3 {
		t.Fatal("an empty configuration must be unset and name all three variables")
	}
	short := Settings{ProviderAPIKey: "re_secret_value", From: "v@example.org", LinkSigningKey: "short-secret"}
	problems := strings.Join(short.Problems(), "; ")
	if !strings.Contains(problems, "EMAIL_LINK_SIGNING_KEY") || strings.Contains(problems, "short-secret") || strings.Contains(problems, "re_secret_value") {
		t.Fatalf("problems %q must name the variable and never a value", problems)
	}
}

func TestInvitationNoticeVoiceAndHeaders(t *testing.T) {
	notice := InvitationNotice{
		InviterHandle: "octo", CollectiveName: "Night <Shift>\nBcc: x",
		InvitationURL: "https://village.example/collectives?invitation=1", UnsubscribeURL: "https://village.example/email/unsubscribe?token=t",
		OneClickUnsubscribeURL: "https://api.example/api/v1/email/unsubscribe?token=t",
	}
	message, err := notice.Message("person@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if message.To != "person@example.org" || strings.ContainsAny(message.Subject, "\r\n") {
		t.Fatalf("message %+v", message)
	}
	if !strings.HasPrefix(message.Text, "@octo invited you to Night <Shift> Bcc: x on village.") {
		t.Fatalf("text %q", message.Text)
	}
	if strings.Contains(message.HTML, "<Shift>") || !strings.Contains(message.HTML, "Night &lt;Shift&gt;") {
		t.Fatalf("the collective name is not escaped in the HTML part: %s", message.HTML)
	}
	if message.Headers["List-Unsubscribe"] != "<"+notice.OneClickUnsubscribeURL+">" || message.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("headers %v", message.Headers)
	}
	for _, tracking := range []string{"<img", "utm_", "pixel"} {
		if strings.Contains(message.HTML, tracking) {
			t.Fatalf("the HTML part carries %q", tracking)
		}
	}
	anonymous, err := InvitationNotice{CollectiveName: "Night Shift"}.Message("person@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(anonymous.Text, "an owner of Night Shift invited you") {
		t.Fatalf("anonymous text %q", anonymous.Text)
	}
}

func TestResendSendsWithoutEchoingTheRecipient(t *testing.T) {
	var got resendRequest
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		if got.Subject == "refuse" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"invalid to: person@example.org"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer server.Close()
	sender := NewResend("re_test_key", "village <v@example.org>")
	sender.endpoint = server.URL
	if err := sender.Send(context.Background(), Message{To: "person@example.org", Subject: "hello", Text: "t"}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer re_test_key" || got.From != "village <v@example.org>" || len(got.To) != 1 || got.To[0] != "person@example.org" {
		t.Fatalf("request %+v auth %q", got, auth)
	}
	err := sender.Send(context.Background(), Message{To: "person@example.org", Subject: "refuse", Text: "t"})
	if !errors.Is(err, ErrDeliveryRefused) || strings.Contains(err.Error(), "person@example.org") || strings.Contains(err.Error(), "re_test_key") {
		t.Fatalf("refusal error %v must not name the recipient or the key", err)
	}
}
