package idnbase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client wraps verified IDNBase v1.2.1 control-api endpoints.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new IDNBase control-plane client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// HealthStatus represents the response from /healthz.
type HealthStatus struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version"`
}

// CheckHealth verifies IDNBase control-api availability.
func (c *Client) CheckHealth(ctx context.Context) (*HealthStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("idnbase healthz returned status %d", resp.StatusCode)
	}
	var out HealthStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SessionInfo represents a verified IDNBase control session from GET /v1/auth/session.
type SessionInfo struct {
	SessionID     string  `json:"sessionId"`
	MFAVerifiedAt *string `json:"mfaVerifiedAt"`
	User          struct {
		ID          string   `json:"id"`
		Email       string   `json:"email"`
		DisplayName string   `json:"displayName"`
		Roles       []string `json:"roles"`
	} `json:"user"`
}

// VerifySession validates an IDNBase session cookie via /v1/auth/session.
func (c *Client) VerifySession(ctx context.Context, sessionCookie string) (*SessionInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/auth/session", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", sessionCookie)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("session verification failed with status %d", resp.StatusCode)
	}
	var out SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.SessionID == "" || out.User.ID == "" {
		return nil, fmt.Errorf("session not authenticated")
	}
	return &out, nil
}

// ProvisionProjectRequest defines the payload for POST /v1/projects.
type ProvisionProjectRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Environment string `json:"environment"`
}

// ProvisionProjectResponse represents the 202 response from POST /v1/projects.
type ProvisionProjectResponse struct {
	JobID     string `json:"jobId"`
	ProjectID string `json:"projectId"`
	State     string `json:"state"`
}

// ProvisionProject requests IDNBase to provision an isolated PostgreSQL database for an organization.
func (c *Client) ProvisionProject(ctx context.Context, sessionCookie, csrfToken, idempotencyKey string, payload ProvisionProjectRequest) (*ProvisionProjectResponse, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/projects", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", sessionCookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.Header.Set("Idempotency-Key", idempotencyKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("provision project failed (%d): %s", resp.StatusCode, string(raw))
	}

	var out ProvisionProjectResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProjectInfo represents a project returned by GET /v1/projects/:id.
type ProjectInfo struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	State string `json:"state"`
}

// GetProject fetches project status from IDNBase control-api.
func (c *Client) GetProject(ctx context.Context, sessionCookie, projectID string) (*ProjectInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/projects/"+projectID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", sessionCookie)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get project failed with status %d", resp.StatusCode)
	}
	var out ProjectInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
