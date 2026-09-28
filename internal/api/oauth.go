package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// tokenExpirySlack refreshes tokens slightly early so a token never expires
// mid-request.
const tokenExpirySlack = 30 * time.Second

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// AuthError is a failure from the OAuth token endpoint.
type AuthError struct {
	StatusCode  int
	Code        string // OAuth error code, e.g. "invalid_client"
	Description string
}

func (e *AuthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("oauth token request failed (%d %s): %s", e.StatusCode, e.Code, e.Description)
	}
	return fmt.Sprintf("oauth token request failed (%d %s)", e.StatusCode, e.Code)
}

func (e *AuthError) UserMessage() string {
	switch {
	case e.StatusCode == http.StatusNotFound:
		return "OAuth token endpoint not found at /oauth/token — is the simple_oauth module enabled?"
	case e.Code == "invalid_client" || e.StatusCode == http.StatusUnauthorized:
		return "Authentication failed — check your client ID and client secret."
	case e.Description != "":
		return "OAuth error: " + e.Description
	default:
		return fmt.Sprintf("OAuth token request failed (%d).", e.StatusCode)
	}
}

// accessToken returns a cached token or fetches a fresh one via the
// client_credentials grant.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}
	endpoint := *c.base
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/oauth/token"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(),
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("can't reach %s: %w", c.base.Host, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var oe struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(data, &oe)
		return "", &AuthError{StatusCode: resp.StatusCode, Code: oe.Error, Description: oe.Description}
	}

	var tok tokenResponse
	if err := json.Unmarshal(data, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("unexpected token response from /oauth/token")
	}
	c.token = tok.AccessToken
	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl > tokenExpirySlack {
		ttl -= tokenExpirySlack
	}
	c.tokenExp = time.Now().Add(ttl)
	return c.token, nil
}

// invalidateToken drops the cached token so the next request fetches a fresh
// one (used when the API rejects a token that should still be valid).
func (c *Client) invalidateToken() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = ""
	c.tokenExp = time.Time{}
}
