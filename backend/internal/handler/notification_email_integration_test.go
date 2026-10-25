//go:build integration

package handler

// The notification address and invitation emails, driven through the
// production handlers against a real PostgreSQL with a fake sender: setting
// and verifying an address, which invitations are emailed, the one-click
// unsubscribe, and that the address never reaches any response but the
// person's own settings.

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/email"
)

//go:embed testdata/invitation-emails.yaml
var invitationEmailsYAML []byte

type invitationEmailCase struct {
	Name             string `yaml:"name"`
	Address          string `yaml:"address"`
	EmailInvitations bool   `yaml:"email_invitations"`
	Inviter          string `yaml:"inviter"`
	Invitee          string `yaml:"invitee"`
	Want             struct {
		Sent bool   `yaml:"sent"`
		Lead string `yaml:"lead"`
	} `yaml:"want"`
}

var requiredInvitationEmailCases = []string{
	"verified_and_opted_in_is_emailed",
	"hidden_inviter_is_an_owner_of",
	"unverified_address_is_not_emailed",
	"opted_out_is_not_emailed",
	"no_address_is_not_emailed",
	"unknown_name_is_not_emailed",
}

const testEmailSigningKey = "integration-signing-key-0123456789abcdef"

// fakeSender records every message instead of sending it.
type fakeSender struct {
	mu   sync.Mutex
	sent []email.Message
}

func (f *fakeSender) Send(_ context.Context, message email.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, message)
	return nil
}

func (f *fakeSender) messages() []email.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]email.Message(nil), f.sent...)
}

func withFakeMail(w *invitationWorld) *fakeSender {
	sender := &fakeSender{}
	w.h.mail = newMailServiceWith(sender, testEmailSigningKey, &config.Config{FrontendURL: "https://village.example.test", BaseURL: "https://api.example.test"})
	return sender
}

func (w *invitationWorld) setAddress(t *testing.T, ctx context.Context, person pgtype.UUID, address string, verified, invitations bool) {
	t.Helper()
	verifiedAt := "NULL"
	if verified {
		verifiedAt = "now()"
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO user_notification_emails (user_id, address, verified_at, email_invitations) VALUES ($1, $2, `+verifiedAt+`, $3)`,
		person, address, invitations); err != nil {
		t.Fatal(err)
	}
}

// TestInvitationEmails drives every row of testdata/invitation-emails.yaml.
func TestInvitationEmails(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	cases := loadNamedFixture(t, invitationEmailsYAML, "invitation-emails", func(c invitationEmailCase) string { return c.Name }, requiredInvitationEmailCases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			w := newInvitationWorld(t, ctx, pool, "curated")
			defer w.cleanup(t, ctx)
			sender := withFakeMail(w)
			if c.Inviter == "hidden" {
				if _, err := pool.Exec(ctx, `UPDATE users SET is_discoverable = false WHERE id = $1`, w.ownerID); err != nil {
					t.Fatal(err)
				}
			}
			handle := "inv-nobody-" + w.suffix
			address := "invitee-" + w.suffix + "@example.org"
			if c.Invitee == "account" {
				invitee := w.person(t, ctx, "inv-invitee", false)
				handle = w.handle(t, ctx, invitee)
				if c.Address != "none" {
					w.setAddress(t, ctx, invitee, address, c.Address == "verified", c.EmailInvitations)
				}
			}
			status, body := w.call(t, w.owner, http.MethodPost, "/groups/"+pgIDString(w.group)+"/invitations", map[string]string{"username": handle})
			if status != http.StatusAccepted || string(body) != "{\"status\":\"invited\"}\n" {
				t.Fatalf("invite answered %d %q", status, body)
			}
			w.h.waitForNotices()
			sent := sender.messages()
			if !c.Want.Sent {
				if len(sent) != 0 {
					t.Fatalf("sent %d emails, want none", len(sent))
				}
				return
			}
			if len(sent) != 1 || sent[0].To != address {
				t.Fatalf("sent %+v, want one email to the invitee", sent)
			}
			var invitation pgtype.UUID
			if err := pool.QueryRow(ctx, `SELECT id FROM collective_invitations WHERE group_id = $1 AND invited_username = $2`, w.group, handle).Scan(&invitation); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(sent[0].Text, "https://village.example.test/collectives?invitation="+pgIDString(invitation)) {
				t.Fatalf("the email does not link the invitation: %s", sent[0].Text)
			}
			ownerHandle := w.handle(t, ctx, w.ownerID)
			switch c.Want.Lead {
			case "handle":
				if !strings.HasPrefix(sent[0].Text, "@"+ownerHandle+" invited you") {
					t.Fatalf("text %q", sent[0].Text)
				}
			case "owner_of":
				if !strings.HasPrefix(sent[0].Text, "an owner of ") || strings.Contains(sent[0].Text+sent[0].HTML+sent[0].Subject, ownerHandle) {
					t.Fatalf("a hidden inviter is named in the email: %q", sent[0].Text)
				}
			default:
				t.Fatalf("unknown lead %q", c.Want.Lead)
			}
			// Inviting again notifies no one.
			if status, _ := w.call(t, w.owner, http.MethodPost, "/groups/"+pgIDString(w.group)+"/invitations", map[string]string{"username": handle}); status != http.StatusAccepted {
				t.Fatalf("re-invite %d", status)
			}
			w.h.waitForNotices()
			if len(sender.messages()) != 1 {
				t.Fatal("re-inviting an open invitation sent another email")
			}
		})
	}
}

// TestInvitationEmailsPerRecipientCap proves the fourth invitation in a day is
// created but not emailed, and the owner's answer does not change.
func TestInvitationEmailsPerRecipientCap(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	w := newInvitationWorld(t, ctx, pool, "curated")
	defer w.cleanup(t, ctx)
	sender := withFakeMail(w)
	invitee := w.person(t, ctx, "inv-invitee", false)
	handle := w.handle(t, ctx, invitee)
	w.setAddress(t, ctx, invitee, "capped-"+w.suffix+"@example.org", true, true)
	groups := []pgtype.UUID{w.group}
	for i := 1; i <= invitationEmailsPerRecipientPerDay; i++ {
		groups = append(groups, w.collective(t, ctx, w.ownerID, "open"))
	}
	for i, group := range groups {
		if i == len(groups)-1 {
			// The count is in the database: a restarted server still skips
			// the email over the cap.
			w.restart()
		}
		status, body := w.call(t, w.owner, http.MethodPost, "/groups/"+pgIDString(group)+"/invitations", map[string]string{"username": handle})
		if status != http.StatusAccepted || string(body) != "{\"status\":\"invited\"}\n" {
			t.Fatalf("invite answered %d %q", status, body)
		}
		w.h.waitForNotices()
	}
	if got := len(sender.messages()); got != invitationEmailsPerRecipientPerDay {
		t.Fatalf("sent %d emails, want %d", got, invitationEmailsPerRecipientPerDay)
	}
	var open int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM collective_invitations WHERE invitee_id = $1`, invitee).Scan(&open); err != nil || open != len(groups) {
		t.Fatalf("%d invitations (%v), want %d: the cap must never skip the invitation itself", open, err, len(groups))
	}
}

// TestNotificationEmailLifecycleAndUnsubscribe sets, verifies and removes an
// address, unsubscribes through the emailed link, and proves the address is
// never in a response other than the person's own settings.
func TestNotificationEmailLifecycleAndUnsubscribe(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	w := newInvitationWorld(t, ctx, pool, "curated")
	defer w.cleanup(t, ctx)
	sender := withFakeMail(w)
	inviteeID := w.person(t, ctx, "inv-invitee", true)
	invitee := &AuthUser{ID: uuid.UUID(inviteeID.Bytes)}
	address := "Person." + w.suffix + "@Example.org"

	settings := func(body []byte) schema.VillageUserSettings {
		t.Helper()
		var s schema.VillageUserSettings
		if err := json.Unmarshal(body, &s); err != nil {
			t.Fatalf("decode settings %s: %v", body, err)
		}
		return s
	}

	// Set: unverified, and one verification email with the token.
	status, body := w.call(t, invitee, http.MethodPut, "/users/me/email", map[string]string{"address": address})
	if status != http.StatusOK {
		t.Fatalf("set %d: %s", status, body)
	}
	if s := settings(body); s.NotificationEmail == nil || s.NotificationEmail.Address != address || s.NotificationEmail.Verified || !s.EmailInvitations {
		t.Fatalf("settings after set %s", body)
	}
	sent := sender.messages()
	if len(sent) != 1 || sent[0].To != address {
		t.Fatalf("verification emails %+v", sent)
	}
	link := sent[0].Text[strings.Index(sent[0].Text, "https://village.example.test/settings?verify_email="):]
	link = strings.Fields(link)[0]
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	token := parsed.Query().Get("verify_email")
	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT verify_token_hash FROM user_notification_emails WHERE user_id = $1`, inviteeID).Scan(&stored); err != nil || string(stored) == token {
		t.Fatalf("the token must be stored only as a hash (%v)", err)
	}

	// A wrong token is 400; the emailed one verifies.
	if status, body := w.call(t, invitee, http.MethodPost, "/users/me/email/verify", map[string]string{"token": "wrong"}); status != http.StatusBadRequest {
		t.Fatalf("wrong token %d: %s", status, body)
	}
	status, body = w.call(t, invitee, http.MethodPost, "/users/me/email/verify", map[string]string{"token": token})
	if status != http.StatusOK || !settings(body).NotificationEmail.Verified {
		t.Fatalf("verify %d: %s", status, body)
	}
	if status, _ := w.call(t, invitee, http.MethodPost, "/users/me/email/verify", map[string]string{"token": token}); status != http.StatusBadRequest {
		t.Fatalf("a used token verified again: %d", status)
	}

	// An invitation is emailed now; the email carries a signed unsubscribe.
	handle := w.handle(t, ctx, inviteeID)
	if status, _ := w.call(t, w.owner, http.MethodPost, "/groups/"+pgIDString(w.group)+"/invitations", map[string]string{"username": handle}); status != http.StatusAccepted {
		t.Fatal("invite failed")
	}
	w.h.waitForNotices()
	sent = sender.messages()
	if len(sent) != 2 {
		t.Fatalf("emails %d, want the verification and one invitation", len(sent))
	}
	oneClick := strings.Trim(sent[1].Headers["List-Unsubscribe"], "<>")
	if !strings.HasPrefix(oneClick, "https://api.example.test/api/v1/email/unsubscribe?token=") || sent[1].Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("unsubscribe headers %v", sent[1].Headers)
	}
	unsubscribe, err := url.Parse(oneClick)
	if err != nil {
		t.Fatal(err)
	}

	// The address appears in no response another viewer receives, and in
	// none of the invitee's own responses but their settings.
	inviteeHandle := handle
	invitationPaths := []struct {
		actor *AuthUser
		path  string
	}{
		{w.owner, "/groups/" + pgIDString(w.group) + "/invitations"},
		{w.owner, "/groups/" + pgIDString(w.group)},
		{w.owner, "/users/" + inviteeHandle},
		{nil, "/users/" + inviteeHandle},
		{invitee, "/users/me/invitations"},
		{invitee, "/users/" + inviteeHandle},
	}
	for _, read := range invitationPaths {
		status, body := w.call(t, read.actor, http.MethodGet, read.path, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s answered %d: %s", read.path, status, body)
		}
		lower := strings.ToLower(string(body))
		if strings.Contains(lower, strings.ToLower(address)) || strings.Contains(lower, "example.org") {
			t.Fatalf("GET %s carries the notification address: %s", read.path, body)
		}
	}

	// Accepting does not reveal it either, to the owner's roster.
	var invitation pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM collective_invitations WHERE group_id = $1 AND invitee_id = $2`, w.group, inviteeID).Scan(&invitation); err != nil {
		t.Fatal(err)
	}
	if status, body := w.call(t, invitee, http.MethodPost, "/users/me/invitations/"+pgIDString(invitation)+"/accept", nil); status != http.StatusOK || strings.Contains(strings.ToLower(string(body)), "example.org") {
		t.Fatalf("accept %d: %s", status, body)
	}
	if _, body := w.call(t, w.owner, http.MethodGet, "/groups/"+pgIDString(w.group), nil); strings.Contains(strings.ToLower(string(body)), "example.org") {
		t.Fatalf("the roster carries the notification address: %s", body)
	}

	// Unsubscribe: a tampered token is 400 and changes nothing; the emailed
	// one, signed out, turns invitation emails off and nothing else.
	if status, _ := w.call(t, nil, http.MethodPost, "/email/unsubscribe?token="+url.QueryEscape(unsubscribe.Query().Get("token")+"x"), nil); status != http.StatusBadRequest {
		t.Fatalf("a tampered unsubscribe answered %d", status)
	}
	status, body = w.call(t, nil, http.MethodPost, "/email/unsubscribe?"+unsubscribe.RawQuery, "List-Unsubscribe=One-Click")
	if status != http.StatusOK || string(body) != "{\"status\":\"unsubscribed\"}\n" {
		t.Fatalf("unsubscribe %d: %s", status, body)
	}
	_, body = w.call(t, invitee, http.MethodGet, "/users/me/settings", nil)
	if s := settings(body); s.EmailInvitations || s.NotificationEmail == nil || !s.NotificationEmail.Verified {
		t.Fatalf("settings after unsubscribe %s", body)
	}

	// Turning it back on, then removing the address, keeps the choice and
	// keeps no copy of the address.
	if status, body := w.call(t, invitee, http.MethodPatch, "/users/me/settings", map[string]bool{"email_invitations": true}); status != http.StatusOK || !settings(body).EmailInvitations {
		t.Fatalf("turn invitation emails on %d: %s", status, body)
	}
	status, body = w.call(t, invitee, http.MethodDelete, "/users/me/email", nil)
	if s := settings(body); status != http.StatusOK || s.NotificationEmail != nil || !s.EmailInvitations {
		t.Fatalf("remove %d: %s", status, body)
	}
	var kept int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_notification_emails WHERE address IS NOT NULL AND user_id = $1`, inviteeID).Scan(&kept); err != nil || kept != 0 {
		t.Fatalf("the removed address is still stored (%d, %v)", kept, err)
	}
}

// TestSetNotificationEmailWithoutEmailIsNotImplemented proves a Village with
// no email configuration answers 501 and stores nothing.
func TestSetNotificationEmailWithoutEmailIsNotImplemented(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	w := newInvitationWorld(t, ctx, pool, "curated")
	defer w.cleanup(t, ctx)
	if w.h.mail != nil {
		t.Fatal("a handler built with no email configuration has email on")
	}
	person := w.person(t, ctx, "inv-person", true)
	status, body := w.call(t, &AuthUser{ID: uuid.UUID(person.Bytes)}, http.MethodPut, "/users/me/email", map[string]string{"address": "nobody@example.org"})
	if status != http.StatusNotImplemented {
		t.Fatalf("set without email %d: %s", status, body)
	}
	var stored int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_notification_emails WHERE user_id = $1`, person).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("stored %d rows (%v), want none", stored, err)
	}
	if status, _ := w.call(t, nil, http.MethodPost, "/email/unsubscribe?token=anything", nil); status != http.StatusBadRequest {
		t.Fatalf("unsubscribe without email %d, want 400", status)
	}
}

// TestVerificationEmailsPerDayCap proves the sixth verification email in a
// day is refused with 429, also after a restart, and stores nothing new.
func TestVerificationEmailsPerDayCap(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	w := newInvitationWorld(t, ctx, pool, "curated")
	defer w.cleanup(t, ctx)
	sender := withFakeMail(w)
	person := w.person(t, ctx, "inv-person", true)
	actor := &AuthUser{ID: uuid.UUID(person.Bytes)}
	for i := 0; i < verificationEmailsPerUserPerDay; i++ {
		if status, body := w.call(t, actor, http.MethodPut, "/users/me/email", map[string]string{"address": "v" + w.suffix + "@example.org"}); status != http.StatusOK {
			t.Fatalf("set %d answered %d: %s", i, status, body)
		}
	}
	w.restart()
	status, body := w.call(t, actor, http.MethodPut, "/users/me/email", map[string]string{"address": "other" + w.suffix + "@example.org"})
	if status != http.StatusTooManyRequests {
		t.Fatalf("the sixth verification answered %d: %s", status, body)
	}
	if got := len(sender.messages()); got != verificationEmailsPerUserPerDay {
		t.Fatalf("sent %d verification emails, want %d", got, verificationEmailsPerUserPerDay)
	}
	var address string
	if err := pool.QueryRow(ctx, `SELECT address FROM user_notification_emails WHERE user_id = $1`, person).Scan(&address); err != nil || !strings.HasPrefix(address, "v") {
		t.Fatalf("a refused request replaced the address with %q (%v)", address, err)
	}
}
