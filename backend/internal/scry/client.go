// Package scry is the client for the private integrity-check service. Village
// calls POST /v1/check and GET /healthz over private networking with a bearer
// secret. Nothing here logs or returns the secret, a request body, a response
// body or a raw transport error.
package scry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/peasant-labs/schema"
)

// MinSecretLength is the shortest accepted SCRY_SHARED_SECRET.
const MinSecretLength = 32

// maxResponseBytes bounds what the client reads from the service.
const maxResponseBytes = 1 << 20

// Settings is the scry configuration, read from the environment by the config
// package. The feature is on only when the URL is http or https with a host
// and the secret is long enough.
type Settings struct {
	URL          string
	SharedSecret string
}

// Unset reports whether nothing is set at all, so the server can say the
// feature is off quietly instead of warning about a partial configuration.
func (s Settings) Unset() bool {
	return strings.TrimSpace(s.URL) == "" && strings.TrimSpace(s.SharedSecret) == ""
}

// Problems names, by environment variable only, why the feature is off.
func (s Settings) Problems() []string {
	var problems []string
	switch raw := strings.TrimSpace(s.URL); {
	case raw == "":
		problems = append(problems, "SCRY_URL is not set")
	default:
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			problems = append(problems, "SCRY_URL is not an http or https URL")
		}
	}
	switch secret := strings.TrimSpace(s.SharedSecret); {
	case secret == "":
		problems = append(problems, "SCRY_SHARED_SECRET is not set")
	case len(secret) < MinSecretLength:
		problems = append(problems, fmt.Sprintf("SCRY_SHARED_SECRET is shorter than %d characters", MinSecretLength))
	}
	return problems
}

// Checker is the integrity service as village uses it.
type Checker interface {
	Check(ctx context.Context, req schema.ScryCheckRequest) (schema.ScryCheckResponse, error)
	Health(ctx context.Context) (schema.ScryHealthResponse, error)
}

// ErrorClass is the closed outcome class of a failed call. The strings equal
// the check ledger's last_error values.
type ErrorClass string

const (
	ClassTimeout         ErrorClass = "timeout"
	ClassUnavailable     ErrorClass = "unavailable"
	ClassRejected        ErrorClass = "rejected"
	ClassInvalidResponse ErrorClass = "invalid_response"
	ClassTooLarge        ErrorClass = "too_large"
)

// Error is a failed call. Its text carries the class and the status only.
type Error struct {
	Class  ErrorClass
	Status int
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("scry: %s (status %d)", e.Class, e.Status)
	}
	return fmt.Sprintf("scry: %s", e.Class)
}

// Classify maps any error from a Checker to its class. An error that is not
// from this package counts as unavailable, except a context deadline.
func Classify(err error) ErrorClass {
	var e *Error
	switch {
	case errors.As(err, &e):
		return e.Class
	case errors.Is(err, context.DeadlineExceeded):
		return ClassTimeout
	default:
		return ClassUnavailable
	}
}

// HTTPClient talks to the service over HTTP.
type HTTPClient struct {
	base   string
	secret string
	client *http.Client
}

var _ Checker = (*HTTPClient)(nil)

// NewHTTPClient builds a client. It never follows a redirect, so the bearer
// header cannot leave for another host.
func NewHTTPClient(s Settings) *HTTPClient {
	return &HTTPClient{
		base:   strings.TrimRight(strings.TrimSpace(s.URL), "/"),
		secret: strings.TrimSpace(s.SharedSecret),
		client: &http.Client{
			Timeout:       time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Check posts one transcript and returns the validated verdict.
func (c *HTTPClient) Check(ctx context.Context, req schema.ScryCheckRequest) (schema.ScryCheckResponse, error) {
	var out schema.ScryCheckResponse
	payload, err := json.Marshal(req)
	if err != nil {
		return out, &Error{Class: ClassRejected}
	}
	body, err := c.do(ctx, http.MethodPost, "/v1/check", payload)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return schema.ScryCheckResponse{}, &Error{Class: ClassInvalidResponse, Status: http.StatusOK}
	}
	if err := out.Validate(len(req.SessionDetail.Turns)); err != nil {
		return schema.ScryCheckResponse{}, &Error{Class: ClassInvalidResponse, Status: http.StatusOK}
	}
	return out, nil
}

// Health reads the service's versions. A service speaking another contract is
// rejected, so village sends it nothing.
func (c *HTTPClient) Health(ctx context.Context) (schema.ScryHealthResponse, error) {
	var out schema.ScryHealthResponse
	body, err := c.do(ctx, http.MethodGet, "/healthz", nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Validate() != nil {
		return schema.ScryHealthResponse{}, &Error{Class: ClassInvalidResponse, Status: http.StatusOK}
	}
	if out.ContractVersion != schema.ScryContractVersion {
		return schema.ScryHealthResponse{}, &Error{Class: ClassRejected, Status: http.StatusOK}
	}
	return out, nil
}

func (c *HTTPClient) do(ctx context.Context, method, path string, payload []byte) ([]byte, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, &Error{Class: ClassUnavailable}
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		// The transport error names the URL; drop it and keep only the class.
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return nil, &Error{Class: ClassTimeout}
		}
		return nil, &Error{Class: ClassUnavailable}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Class: classifyStatus(resp.StatusCode), Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &Error{Class: ClassTimeout}
		}
		return nil, &Error{Class: ClassUnavailable, Status: resp.StatusCode}
	}
	if len(body) > maxResponseBytes {
		return nil, &Error{Class: ClassInvalidResponse, Status: resp.StatusCode}
	}
	return body, nil
}

func classifyStatus(status int) ErrorClass {
	switch {
	case status == http.StatusRequestEntityTooLarge:
		return ClassTooLarge
	case status == http.StatusRequestTimeout, status == http.StatusTooManyRequests, status >= 500:
		return ClassUnavailable
	default:
		return ClassRejected
	}
}
