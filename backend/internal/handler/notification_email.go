package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/email"
)

// The notification address is personal contact data. It is returned only in
// the caller's own settings (VillageUserSettings), is never logged, and never
// reaches a response another viewer receives.

// mapVillageUserSettings maps the stored settings to the wire type. A person
// with no notification row has no address and invitation emails on, the
// default.
func mapVillageUserSettings(user sqlc.User, notification *sqlc.UserNotificationEmail) schema.VillageUserSettings {
	settings := schema.VillageUserSettings{
		PreviewBeforeAttach:    user.PreviewBeforeAttach,
		AutoAttachPullRequests: user.AutoAttachPullRequests,
		EmailInvitations:       true,
	}
	if notification != nil {
		settings.EmailInvitations = notification.EmailInvitations
		if notification.Address.Valid {
			settings.NotificationEmail = &schema.VillageNotificationEmail{
				Address: notification.Address.String, Verified: notification.VerifiedAt.Valid,
			}
		}
	}
	return settings
}

// readNotificationEmail returns the caller's notification row, or nil when
// they have none.
func readNotificationEmail(ctx context.Context, q Querier, userID pgtype.UUID) (*sqlc.UserNotificationEmail, error) {
	row, err := q.GetNotificationEmail(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// writeOwnSettings answers with the caller's own settings, read fresh.
func (h *Handler) writeOwnSettings(w http.ResponseWriter, r *http.Request, userID pgtype.UUID) {
	h.writeSettingsFrom(w, r, userID, nil, nil)
}

// writeSettingsFrom answers with the caller's own settings, reusing a row a
// write just returned and reading whichever half it did not.
func (h *Handler) writeSettingsFrom(w http.ResponseWriter, r *http.Request, userID pgtype.UUID, user *sqlc.User, notification *sqlc.UserNotificationEmail) {
	if user == nil {
		row, err := h.queries.GetUserByID(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not read your settings")
			return
		}
		user = &row
	}
	if notification == nil {
		row, err := readNotificationEmail(r.Context(), h.queries, userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Could not read your settings")
			return
		}
		notification = row
	}
	writeJSON(w, http.StatusOK, mapVillageUserSettings(*user, notification))
}

// verificationTokenHash is what Village stores of a verification token.
func verificationTokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func newVerificationToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// SetMyNotificationEmail sets the caller's notification address and emails a
// verification token to it. PUT /users/me/email (AuthRequired).
func (h *Handler) SetMyNotificationEmail(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	var req schema.VillageSetNotificationEmailRequest
	if !h.decodeContractBody(w, r, opSetNotificationEmail, &req) {
		return
	}
	if h.mail == nil {
		writeError(w, http.StatusNotImplemented, "This Village sends no email, so it keeps no notification address. Invitations are shown in the app.")
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "Enter one email address: one @ with text on both sides and no spaces")
		return
	}
	allowed, err := takeDailyQuota(r.Context(), h.queries, user.PgID(), quotaVerificationEmail, verificationEmailsPerUserPerDay)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not count your verification emails; nothing was saved. Retry the request.")
		return
	}
	if !allowed {
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("Village has sent %d verification emails for your account in the last day. Try again tomorrow.", verificationEmailsPerUserPerDay))
		return
	}
	token, err := newVerificationToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create a verification token; nothing was saved. Retry the request.")
		return
	}
	if _, err := h.queries.SetNotificationEmail(r.Context(), sqlc.SetNotificationEmailParams{
		UserID: user.PgID(), Address: pgtype.Text{String: req.Address, Valid: true},
		VerifyTokenHash: verificationTokenHash(token),
		VerifyExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(verificationTokenLifetime), Valid: true},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save your email address; retry the request")
		return
	}
	message, err := email.Verification{Token: token, VerifyURL: h.mail.verifyURL(token), Lifetime: verificationTokenLifetime}.Message(req.Address)
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), noticeDeliveryTimeout)
		err = h.mail.sender.Send(ctx, message)
		cancel()
	}
	if err != nil {
		slog.Warn("verification email not sent", "operation", "notification_email_verification", "consequence", "the address is saved unverified", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "Your address was saved, but the verification email could not be sent. Set the address again to retry.")
		return
	}
	h.writeOwnSettings(w, r, user.PgID())
}

// VerifyMyNotificationEmail confirms the caller's address with the token
// emailed to it. POST /users/me/email/verify (AuthRequired).
func (h *Handler) VerifyMyNotificationEmail(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	var req schema.VillageVerifyNotificationEmailRequest
	if !h.decodeContractBody(w, r, opVerifyNotificationEmail, &req) {
		return
	}
	_, err := h.queries.VerifyNotificationEmail(r.Context(), sqlc.VerifyNotificationEmailParams{UserID: user.PgID(), VerifyTokenHash: verificationTokenHash(req.Token)})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusBadRequest, "That code is wrong, has expired, or is for an address you have since replaced. Set your address again for a new one.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not verify your email address; retry the request")
		return
	}
	h.writeOwnSettings(w, r, user.PgID())
}

// RemoveMyNotificationEmail removes the caller's address; Village keeps no
// copy. DELETE /users/me/email (AuthRequired).
func (h *Handler) RemoveMyNotificationEmail(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if err := h.queries.RemoveNotificationEmail(r.Context(), user.PgID()); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not remove your email address; retry the request")
		return
	}
	h.writeOwnSettings(w, r, user.PgID())
}

// UnsubscribeInvitationEmails turns invitation emails off for the account a
// signed token names, signed out. POST /email/unsubscribe?token= (no auth).
// It is the RFC 8058 one-click target; any request body is ignored.
func (h *Handler) UnsubscribeInvitationEmails(w http.ResponseWriter, r *http.Request) {
	const invalid = "This unsubscribe link is invalid or has expired. You can turn invitation emails off in your settings."
	if h.mail == nil {
		writeError(w, http.StatusBadRequest, invalid)
		return
	}
	account, err := h.mail.links.VerifyUnsubscribe(r.URL.Query().Get("token"), time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, invalid)
		return
	}
	if err := h.queries.UnsubscribeInvitationEmails(r.Context(), toPgUUID(account)); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not turn invitation emails off; retry the link")
		return
	}
	writeJSON(w, http.StatusOK, schema.VillageStatusResponse{Status: "unsubscribed"})
}
