package email

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// UnsubscribeLifetime is how long an unsubscribe link keeps working after the
// email that carries it was sent.
const UnsubscribeLifetime = 90 * 24 * time.Hour

// unsubscribePurpose binds a signature to the one thing it may do, so a
// signature made for anything else can never turn into an unsubscribe.
const unsubscribePurpose = "village/unsubscribe-invitation-emails/v1"

// ErrInvalidLinkToken is a token that is malformed, forged, or expired. The
// three are one error on purpose: the answer does not help a forger.
var ErrInvalidLinkToken = errors.New("the link token is malformed, forged, or expired")

// LinkSigner signs the one link that must work signed out: the unsubscribe
// link. The invitation link carries no secret; it is a plain deep link that
// requires signing in as the invitee.
type LinkSigner struct {
	key []byte
}

// NewLinkSigner signs with EMAIL_LINK_SIGNING_KEY.
func NewLinkSigner(key string) LinkSigner {
	return LinkSigner{key: []byte(key)}
}

// UnsubscribeToken names an account and an expiry, signed with HMAC-SHA256
// over the purpose, the account and the expiry.
func (s LinkSigner) UnsubscribeToken(account uuid.UUID, expires time.Time) string {
	payload := make([]byte, 0, 24)
	payload = append(payload, account[:]...)
	payload = binary.BigEndian.AppendUint64(payload, uint64(expires.Unix()))
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(s.mac(payload))
}

// VerifyUnsubscribe returns the account an unexpired, authentic token names.
func (s LinkSigner) VerifyUnsubscribe(token string, now time.Time) (uuid.UUID, error) {
	if len(s.key) == 0 {
		return uuid.UUID{}, ErrInvalidLinkToken
	}
	encodedPayload, encodedMAC, found := strings.Cut(token, ".")
	if !found {
		return uuid.UUID{}, ErrInvalidLinkToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil || len(payload) != 24 {
		return uuid.UUID{}, ErrInvalidLinkToken
	}
	mac, err := base64.RawURLEncoding.DecodeString(encodedMAC)
	if err != nil || !hmac.Equal(mac, s.mac(payload)) {
		return uuid.UUID{}, ErrInvalidLinkToken
	}
	expires := time.Unix(int64(binary.BigEndian.Uint64(payload[16:])), 0)
	if !now.Before(expires) {
		return uuid.UUID{}, ErrInvalidLinkToken
	}
	account, err := uuid.FromBytes(payload[:16])
	if err != nil {
		return uuid.UUID{}, ErrInvalidLinkToken
	}
	return account, nil
}

func (s LinkSigner) mac(payload []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(unsubscribePurpose))
	m.Write([]byte{0})
	m.Write(payload)
	return m.Sum(nil)
}
