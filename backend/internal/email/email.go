// Package email sends Village's few transactional emails: the verification
// token for a notification address, and the notice that a collective invited
// someone. Delivery goes through Sender, so tests use a fake and production
// uses Resend's HTTP API.
//
// Nothing in this package logs or wraps an address or a key into an error:
// an address is personal contact data and a key is a credential.
package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is one email. Headers carries the few extra headers Village sets,
// such as List-Unsubscribe; it never carries tracking.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
	Headers map[string]string
}

// Sender delivers one message. An error never names the recipient.
type Sender interface {
	Send(ctx context.Context, message Message) error
}

// MinLinkSigningKeyLength is the shortest accepted EMAIL_LINK_SIGNING_KEY.
const MinLinkSigningKeyLength = 32

// Settings is the email configuration, read from the environment by the
// config package. Email is on only when all three are set and the signing key
// is long enough.
type Settings struct {
	ProviderAPIKey string
	From           string
	LinkSigningKey string
}

// Problems names, by environment variable only, why email is off. It is empty
// when email is fully configured, and it lists every variable when none is set.
func (s Settings) Problems() []string {
	var problems []string
	if strings.TrimSpace(s.ProviderAPIKey) == "" {
		problems = append(problems, "EMAIL_PROVIDER_API_KEY is not set")
	}
	if strings.TrimSpace(s.From) == "" {
		problems = append(problems, "EMAIL_FROM is not set")
	}
	switch key := strings.TrimSpace(s.LinkSigningKey); {
	case key == "":
		problems = append(problems, "EMAIL_LINK_SIGNING_KEY is not set")
	case len(key) < MinLinkSigningKeyLength:
		problems = append(problems, fmt.Sprintf("EMAIL_LINK_SIGNING_KEY is shorter than %d characters", MinLinkSigningKeyLength))
	}
	return problems
}

// Unset reports whether nothing is set at all, so the server can say
// "email is off" quietly instead of warning about a partial configuration.
func (s Settings) Unset() bool {
	return strings.TrimSpace(s.ProviderAPIKey) == "" && strings.TrimSpace(s.From) == "" && strings.TrimSpace(s.LinkSigningKey) == ""
}

// resendEndpoint is Resend's send-email API.
const resendEndpoint = "https://api.resend.com/emails"

// Resend sends through Resend's HTTP API. Open and click tracking are turned
// off for the sending domain in Resend's dashboard; this client adds no
// tracking of its own.
type Resend struct {
	apiKey   string
	from     string
	endpoint string
	client   *http.Client
}

// NewResend builds a Resend sender with a short request timeout.
func NewResend(apiKey, from string) *Resend {
	return &Resend{apiKey: apiKey, from: from, endpoint: resendEndpoint, client: &http.Client{Timeout: 10 * time.Second}}
}

type resendRequest struct {
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	Text    string            `json:"text"`
	HTML    string            `json:"html,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ErrDeliveryRefused is a send the provider answered with a non-success
// status. The status is kept; the response body is not, because it can echo
// the recipient.
var ErrDeliveryRefused = errors.New("the email provider refused the message")

// Send posts one message. Errors carry the HTTP status at most.
func (r *Resend) Send(ctx context.Context, message Message) error {
	body, err := json.Marshal(resendRequest{
		From: r.from, To: []string{message.To}, Subject: message.Subject,
		Text: message.Text, HTML: message.HTML, Headers: message.Headers,
	})
	if err != nil {
		return fmt.Errorf("email.Resend.Send: encode the message: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("email.Resend.Send: build the request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+r.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		// The URL is the provider's fixed endpoint; the error names no
		// recipient and no key.
		return fmt.Errorf("email.Resend.Send: the provider could not be reached: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("email.Resend.Send: %w with status %d", ErrDeliveryRefused, response.StatusCode)
	}
	return nil
}
