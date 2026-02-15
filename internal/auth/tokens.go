package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RegisterResponse is the response from POST /auth/register.
type RegisterResponse struct {
	DeviceID     string `json:"device_id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Address      string `json:"address"`
	Subnet       string `json:"subnet"`
	TURNSecret   string `json:"turn_secret"`
	ServerURL    string `json:"server_url"`
}

// Register exchanges a transient GitHub access token for bamgate device
// credentials by calling POST /auth/register on the signaling server.
func Register(ctx context.Context, serverURL, githubToken, deviceName string) (*RegisterResponse, error) {
	body, err := json.Marshal(map[string]string{
		"github_token": githubToken,
		"device_name":  deviceName,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		serverURL+"/auth/register", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling /auth/register: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("registration failed: %s", errResp.Error)
		}
		return nil, fmt.Errorf("registration failed: HTTP %d", resp.StatusCode)
	}

	var result RegisterResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &result, nil
}

// Device represents a single device in the list response.
type Device struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Address    string `json:"address"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt *int64 `json:"last_seen_at"`
	Revoked    bool   `json:"revoked"`
}

// ListDevicesResponse is the response from GET /auth/devices.
type ListDevicesResponse struct {
	Devices []Device `json:"devices"`
}

// ListDevices fetches all devices registered under the authenticated user.
func ListDevices(ctx context.Context, serverURL, jwt string) (*ListDevicesResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		serverURL+"/auth/devices", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling /auth/devices: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("listing devices failed: %s", errResp.Error)
		}
		return nil, fmt.Errorf("listing devices failed: HTTP %d", resp.StatusCode)
	}

	var result ListDevicesResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &result, nil
}

// RevokeDevice revokes (deactivates) a device by ID.
func RevokeDevice(ctx context.Context, serverURL, jwt, deviceID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		serverURL+"/auth/devices/"+deviceID, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("calling /auth/devices/%s: %w", deviceID, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("revoking device failed: %s", errResp.Error)
		}
		return fmt.Errorf("revoking device failed: HTTP %d", resp.StatusCode)
	}

	return nil
}

// RefreshResponse is the response from POST /auth/refresh.
type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// Refresh exchanges a refresh token for a new JWT access token and a
// rotated refresh token by calling POST /auth/refresh.
func Refresh(ctx context.Context, serverURL, deviceID, refreshToken string) (*RefreshResponse, error) {
	body, err := json.Marshal(map[string]string{
		"device_id":     deviceID,
		"refresh_token": refreshToken,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		serverURL+"/auth/refresh", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling /auth/refresh: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("token refresh failed: %s", errResp.Error)
		}
		return nil, fmt.Errorf("token refresh failed: HTTP %d", resp.StatusCode)
	}

	var result RefreshResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &result, nil
}
