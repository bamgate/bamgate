// Package deploy implements deployment of the bamgate signaling worker to
// Cloudflare Workers via the Cloudflare REST API (v4).
package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

const cfAPIBase = "https://api.cloudflare.com/client/v4"

// Client communicates with the Cloudflare REST API to deploy and manage
// bamgate signaling workers.
type Client struct {
	apiToken   string
	httpClient *http.Client
}

// NewClient creates a new Cloudflare API client with the given API token.
func NewClient(apiToken string) *Client {
	return &Client{
		apiToken:   apiToken,
		httpClient: &http.Client{},
	}
}

// VerifyToken checks that the API token is valid and active.
func (c *Client) VerifyToken(ctx context.Context) error {
	var resp struct {
		Success bool `json:"success"`
		Result  struct {
			Status string `json:"status"`
		} `json:"result"`
	}

	if err := c.doJSON(ctx, http.MethodGet, "/user/tokens/verify", nil, &resp); err != nil {
		return fmt.Errorf("verifying API token: %w", err)
	}

	if !resp.Success || resp.Result.Status != "active" {
		return fmt.Errorf("API token is not active (status: %s)", resp.Result.Status)
	}

	return nil
}

// Account represents a Cloudflare account.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListAccounts returns all accounts accessible by the API token.
func (c *Client) ListAccounts(ctx context.Context) ([]Account, error) {
	var resp struct {
		Success bool      `json:"success"`
		Result  []Account `json:"result"`
	}

	if err := c.doJSON(ctx, http.MethodGet, "/accounts", nil, &resp); err != nil {
		return nil, fmt.Errorf("listing accounts: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("listing accounts: API returned success=false")
	}

	return resp.Result, nil
}

// GetSubdomain returns the workers.dev subdomain for the given account.
// The full worker URL is https://<script_name>.<subdomain>.workers.dev.
func (c *Client) GetSubdomain(ctx context.Context, accountID string) (string, error) {
	var resp struct {
		Success bool `json:"success"`
		Result  struct {
			Subdomain string `json:"subdomain"`
		} `json:"result"`
	}

	path := fmt.Sprintf("/accounts/%s/workers/subdomain", accountID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return "", fmt.Errorf("getting workers subdomain: %w", err)
	}

	if !resp.Success || resp.Result.Subdomain == "" {
		return "", fmt.Errorf("no workers.dev subdomain configured for account %s", accountID)
	}

	return resp.Result.Subdomain, nil
}

// WorkerExists checks whether a worker script with the given name exists.
func (c *Client) WorkerExists(ctx context.Context, accountID, scriptName string) (bool, error) {
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s", accountID, scriptName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfAPIBase+path, nil)
	if err != nil {
		return false, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("checking worker existence: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck // drain body

	// 200 means the worker exists; 404 means it doesn't.
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}

	return false, fmt.Errorf("unexpected status %d checking worker %q", resp.StatusCode, scriptName)
}

// WorkerModule is a file to include in a worker upload.
type WorkerModule struct {
	Name        string // Part name / filename (e.g. "worker.mjs")
	Data        []byte
	ContentType string // e.g. "application/javascript+module" or "application/wasm"
}

// DeployWorkerInput contains all parameters for deploying a worker.
type DeployWorkerInput struct {
	AccountID  string
	ScriptName string
	Modules    []WorkerModule
	MainModule string // Must match one of the module names.
	AuthToken  string // The bamgate auth token to set as a plain text binding.
	TURNSecret string // Shared secret for TURN credential generation/validation.

	// IncludeMigration controls whether the DO migration is included.
	// Set to true on first deploy, false on re-deploys.
	IncludeMigration bool
}

// DeployWorker uploads a worker script with Durable Object bindings and
// optional migration. The auth token is set as a plain text environment
// variable binding (not a secret) so it can be read back later.
func (c *Client) DeployWorker(ctx context.Context, input DeployWorkerInput) error {
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s", input.AccountID, input.ScriptName)

	// Build metadata JSON.
	metadata := map[string]any{
		"main_module":         input.MainModule,
		"compatibility_date":  "2025-01-01",
		"compatibility_flags": []string{"nodejs_compat"},
		"bindings": []map[string]any{
			{
				"type":       "durable_object_namespace",
				"name":       "SIGNALING_ROOM",
				"class_name": "SignalingRoom",
			},
			{
				"type": "plain_text",
				"name": "AUTH_TOKEN",
				"text": input.AuthToken,
			},
			{
				"type": "plain_text",
				"name": "TURN_SECRET",
				"text": input.TURNSecret,
			},
		},
	}

	if input.IncludeMigration {
		metadata["migrations"] = map[string]any{
			"new_tag": "v1",
			"steps": []map[string]any{
				{
					"new_sqlite_classes": []string{"SignalingRoom"},
				},
			},
		}
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshaling metadata: %w", err)
	}

	// Build multipart form.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Metadata part.
	metaHeader := make(textproto.MIMEHeader)
	metaHeader.Set("Content-Disposition", `form-data; name="metadata"; filename="metadata.json"`)
	metaHeader.Set("Content-Type", "application/json")
	metaPart, err := writer.CreatePart(metaHeader)
	if err != nil {
		return fmt.Errorf("creating metadata part: %w", err)
	}
	if _, err := metaPart.Write(metadataJSON); err != nil {
		return fmt.Errorf("writing metadata: %w", err)
	}

	// Module parts.
	for _, mod := range input.Modules {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, mod.Name, mod.Name))
		header.Set("Content-Type", mod.ContentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return fmt.Errorf("creating part for %s: %w", mod.Name, err)
		}
		if _, err := part.Write(mod.Data); err != nil {
			return fmt.Errorf("writing part %s: %w", mod.Name, err)
		}
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("closing multipart writer: %w", err)
	}

	// Upload.
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, cfAPIBase+path, &body)
	if err != nil {
		return fmt.Errorf("creating upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("uploading worker: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deploying worker: HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var result struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &result); err == nil && !result.Success {
		msgs := make([]string, 0, len(result.Errors))
		for _, e := range result.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("deploying worker: %s", strings.Join(msgs, "; "))
	}

	return nil
}

// EnableWorkerSubdomain ensures the worker is reachable on the workers.dev subdomain.
func (c *Client) EnableWorkerSubdomain(ctx context.Context, accountID, scriptName string) error {
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s/subdomain", accountID, scriptName)
	body := map[string]bool{"enabled": true}
	var resp struct {
		Success bool `json:"success"`
	}

	if err := c.doJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
		return fmt.Errorf("enabling worker subdomain: %w", err)
	}

	return nil
}

// GetWorkerBindings retrieves the current bindings for a worker script.
// This is used to read back the AUTH_TOKEN plain text binding.
func (c *Client) GetWorkerBindings(ctx context.Context, accountID, scriptName string) ([]WorkerBinding, error) {
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s/settings", accountID, scriptName)

	var resp struct {
		Success bool `json:"success"`
		Result  struct {
			Bindings []WorkerBinding `json:"bindings"`
		} `json:"result"`
	}

	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("getting worker bindings: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("getting worker bindings: API returned success=false")
	}

	return resp.Result.Bindings, nil
}

// WorkerBinding represents a single binding on a Cloudflare Worker.
type WorkerBinding struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Text string `json:"text,omitempty"` // For plain_text bindings.
}

// GetAuthTokenFromBindings extracts the AUTH_TOKEN value from worker bindings.
func GetAuthTokenFromBindings(bindings []WorkerBinding) (string, bool) {
	for _, b := range bindings {
		if b.Name == "AUTH_TOKEN" && b.Type == "plain_text" {
			return b.Text, true
		}
	}
	return "", false
}

// GetTURNSecretFromBindings extracts the TURN_SECRET value from worker bindings.
func GetTURNSecretFromBindings(bindings []WorkerBinding) (string, bool) {
	for _, b := range bindings {
		if b.Name == "TURN_SECRET" && b.Type == "plain_text" {
			return b.Text, true
		}
	}
	return "", false
}

// WorkerURL constructs the full HTTPS URL for a deployed worker.
func WorkerURL(scriptName, subdomain string) string {
	return fmt.Sprintf("https://%s.%s.workers.dev", scriptName, subdomain)
}

// WorkerWSURL constructs the WebSocket signaling URL for a deployed worker.
func WorkerWSURL(scriptName, subdomain string) string {
	return fmt.Sprintf("wss://%s.%s.workers.dev/connect", scriptName, subdomain)
}

// doJSON performs an HTTP request to the Cloudflare API, optionally sending a
// JSON body and decoding the JSON response.
func (c *Client) doJSON(ctx context.Context, method, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, cfAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}

	return nil
}

// truncate shortens a string to at most maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
