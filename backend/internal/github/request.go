package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// doInstallationJSON makes one authenticated request through the installation
// token and decodes the JSON response into out when out is non-nil.
//
// op names the operation for error messages ("create check run"), so a failure
// reads like the call the caller made. A non-2xx response is returned as an
// error and nothing else happens: this client never retries and never mutates
// local state, so a caller keeps the attachment where it was and retries on the
// next delivery or a Refresh.
func (c *Client) doInstallationJSON(ctx context.Context, installationID int64, op, method, path string, payload any, out any) error {
	token, err := c.installationToken(ctx, installationID)
	if err != nil {
		return err
	}

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("github: encode %s: %w", op, err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github: request %s: %w", op, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return apiError(resp, strings.ToLower(op))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("github: decode %s: %w", op, err)
	}
	return nil
}
