// Package auth implements GitHub OAuth Device Authorization Grant (RFC 8628)
// and token management for bamgate's JWT-based authentication.
package auth

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

// GitHubClientID is the OAuth App client ID registered for the bamgate project.
// The Device Authorization Grant does not require a client secret for public clients.
const GitHubClientID = "Ov23liOEzb4I8AiZupu2"

// deviceCodeResponse is the response from GitHub's device/code endpoint.
type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// tokenResponse is the response from GitHub's access_token endpoint.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
}

// DeviceAuthResult contains the output of a successful Device Authorization flow.
type DeviceAuthResult struct {
	// AccessToken is the transient GitHub access token (used once for registration).
	AccessToken string
}

// DeviceAuth initiates the GitHub Device Authorization Grant flow (RFC 8628).
// It displays the user code and verification URL via the provided print function,
// then polls GitHub until the user authorizes or the flow expires.
//
// The printFn callback receives the user code and verification URL for display.
// It is called exactly once, before polling begins.
func DeviceAuth(ctx context.Context, printFn func(userCode, verificationURI string)) (*DeviceAuthResult, error) {
	// Step 1: Request device and user codes.
	dcResp, err := requestDeviceCode(ctx)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}

	// Display the code to the user.
	printFn(dcResp.UserCode, dcResp.VerificationURI)

	// Step 2: Poll for authorization.
	interval := time.Duration(dcResp.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	deadline := time.Now().Add(time.Duration(dcResp.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device authorization expired — please try again")
		}

		tokenResp, err := pollAccessToken(ctx, dcResp.DeviceCode)
		if err != nil {
			return nil, fmt.Errorf("polling for access token: %w", err)
		}

		switch tokenResp.Error {
		case "":
			// Success.
			return &DeviceAuthResult{AccessToken: tokenResp.AccessToken}, nil
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "expired_token":
			return nil, fmt.Errorf("device authorization expired — please try again")
		case "access_denied":
			return nil, fmt.Errorf("authorization was denied by the user")
		default:
			return nil, fmt.Errorf("unexpected error from GitHub: %s", tokenResp.Error)
		}
	}
}

// requestDeviceCode calls GitHub's device/code endpoint to start the flow.
func requestDeviceCode(ctx context.Context) (*deviceCodeResponse, error) {
	data := url.Values{
		"client_id": {GitHubClientID},
		"scope":     {"read:user"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://github.com/login/device/code",
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result deviceCodeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if result.DeviceCode == "" || result.UserCode == "" {
		return nil, fmt.Errorf("invalid response: missing device_code or user_code")
	}

	return &result, nil
}

// pollAccessToken calls GitHub's access_token endpoint to check if the user
// has authorized the device.
func pollAccessToken(ctx context.Context, deviceCode string) (*tokenResponse, error) {
	data := url.Values{
		"client_id":   {GitHubClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://github.com/login/oauth/access_token",
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var result tokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &result, nil
}
