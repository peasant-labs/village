package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/email"
)

const (
	// invitationEmailsPerRecipientPerDay bounds how many invitation emails one
	// person receives in a day, from every collective together. Over it the
	// invitation is still created and listed; only the email is skipped, and
	// nobody is told.
	invitationEmailsPerRecipientPerDay = 3
	// verificationEmailsPerUserPerDay bounds how often one account can make
	// Village email a verification token, to any address.
	verificationEmailsPerUserPerDay = 5
	// verificationTokenLifetime is how long a verification token works.
	verificationTokenLifetime = 24 * time.Hour
	// noticeDeliveryTimeout bounds one invitation email, reads included. A
	// notice that does not go out in time is dropped, never retried.
	noticeDeliveryTimeout = 15 * time.Second
)

// mailService is Village's email, present only when the deployment configures
// it. A nil *mailService means email is off: setting an address answers 501
// and invitations exist in the app only.
type mailService struct {
	sender      email.Sender
	links       email.LinkSigner
	frontendURL string
	apiBaseURL  string
}

// newMailService reads the email settings and returns nil, with one log line
// naming only environment variables, when email is not fully configured.
func newMailService(cfg *config.Config) *mailService {
	settings := email.Settings{ProviderAPIKey: cfg.EmailProviderAPIKey, From: cfg.EmailFrom, LinkSigningKey: cfg.EmailLinkSigningKey}
	if settings.Unset() {
		log.Println("Email not configured; invitations are shown in the app only (set EMAIL_PROVIDER_API_KEY, EMAIL_FROM and EMAIL_LINK_SIGNING_KEY to enable)")
		return nil
	}
	if problems := settings.Problems(); len(problems) > 0 {
		log.Printf("Email configuration incomplete; email is off and invitations are shown in the app only: %s", strings.Join(problems, "; "))
		return nil
	}
	log.Println("Email configured; notification addresses and invitation emails are enabled")
	return newMailServiceWith(email.NewResend(settings.ProviderAPIKey, settings.From), settings.LinkSigningKey, cfg)
}

// newMailServiceWith wires a sender; production passes Resend, a test a fake.
func newMailServiceWith(sender email.Sender, signingKey string, cfg *config.Config) *mailService {
	return &mailService{
		sender:      sender,
		links:       email.NewLinkSigner(signingKey),
		frontendURL: strings.TrimRight(cfg.FrontendURL, "/"),
		apiBaseURL:  strings.TrimRight(cfg.BaseURL, "/"),
	}
}

func (m *mailService) invitationURL(invitationID pgtype.UUID) string {
	return m.frontendURL + "/collectives?invitation=" + uuid.UUID(invitationID.Bytes).String()
}

func (m *mailService) verifyURL(token string) string {
	return m.frontendURL + "/settings?verify_email=" + url.QueryEscape(token)
}

func (m *mailService) unsubscribeURLs(account pgtype.UUID, now time.Time) (page, oneClick string) {
	token := url.QueryEscape(m.links.UnsubscribeToken(uuid.UUID(account.Bytes), now.Add(email.UnsubscribeLifetime)))
	return m.frontendURL + "/email/unsubscribe?token=" + token, m.apiBaseURL + "/api/v1/email/unsubscribe?token=" + token
}

// invitationNotice is an invitation that may be emailed once the invite has
// committed and its response has been sent.
type invitationNotice struct {
	invitationID   pgtype.UUID
	inviteeID      pgtype.UUID
	inviterID      pgtype.UUID
	collectiveName string
}

// dispatchInvitationNotice emails one invitation off the request path. It is
// best effort: it is never retried, a failure is logged without the address,
// and nothing about it reaches the inviting owner.
func (h *Handler) dispatchInvitationNotice(notice invitationNotice) {
	if h.mail == nil || !notice.inviteeID.Valid {
		return
	}
	h.notices.Add(1)
	go func() {
		defer h.notices.Done()
		ctx, cancel := context.WithTimeout(context.Background(), noticeDeliveryTimeout)
		defer cancel()
		if err := h.deliverInvitationNotice(ctx, notice, time.Now()); err != nil {
			slog.Warn("invitation email not sent", "operation", "invitation_notice", "consequence", "the invitation is still shown in the app", "error", err.Error())
		}
	}()
}

// waitForNotices blocks until every dispatched notice has finished, so a
// shutdown or a test does not cut one off.
func (h *Handler) waitForNotices() { h.notices.Wait() }

func (h *Handler) deliverInvitationNotice(ctx context.Context, notice invitationNotice, now time.Time) error {
	address, err := h.queries.GetInvitationEmailRecipient(ctx, notice.inviteeID)
	if errors.Is(err, pgx.ErrNoRows) {
		// No verified address, or the person turned invitation emails off.
		return nil
	}
	if err != nil {
		return fmt.Errorf("read whether the invitee takes invitation emails: %w", err)
	}
	allowed, err := takeDailyQuota(ctx, h.queries, notice.inviteeID, quotaInvitationEmail, invitationEmailsPerRecipientPerDay)
	if err != nil {
		return err
	}
	if !allowed {
		// Over the daily cap the invitation still exists; it is not mailed.
		return nil
	}
	// The inviting owner as the invitee may see them, the same answer the
	// invitee's list gives.
	invitee := &AuthUser{ID: uuid.UUID(notice.inviteeID.Bytes)}
	inviter, err := h.resolvePublicOwner(ctx, invitee, notice.inviterID)
	if err != nil {
		return fmt.Errorf("resolve the inviting owner for the invitee: %w", err)
	}
	handle := ""
	if inviter.IsProfile() {
		handle = inviter.Username
	}
	page, oneClick := h.mail.unsubscribeURLs(notice.inviteeID, now)
	message, err := email.InvitationNotice{
		InviterHandle: handle, CollectiveName: notice.collectiveName,
		InvitationURL: h.mail.invitationURL(notice.invitationID), UnsubscribeURL: page, OneClickUnsubscribeURL: oneClick,
	}.Message(address)
	if err != nil {
		return err
	}
	return h.mail.sender.Send(ctx, message)
}
