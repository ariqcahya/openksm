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

// Client wraps verified IDNBase v1.2.1 control-api and End-User Auth v2 endpoints.
type Client struct {
	baseURL         string
	gatewayURL      string
	authProjectSlug string
	httpClient      *http.Client
}

// NewClient creates a new IDNBase control-plane client.
func NewClient(baseURL string, opts ...string) *Client {
	gw := "http://graphql-gateway:4000"
	if len(opts) > 0 && strings.TrimSpace(opts[0]) != "" {
		gw = strings.TrimRight(opts[0], "/")
	}
	slug := ""
	if len(opts) > 1 {
		slug = strings.TrimSpace(opts[1])
	}
	return &Client{
		baseURL:         strings.TrimRight(baseURL, "/"),
		gatewayURL:      gw,
		authProjectSlug: slug,
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

// VerifySession validates an IDNBase control-plane cookie or End-User Auth v2 token cookie.
func (c *Client) VerifySession(ctx context.Context, sessionCookie string) (*SessionInfo, error) {
	if strings.Contains(sessionCookie, "idnbase_session=") {
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

		if resp.StatusCode == http.StatusOK {
			var out SessionInfo
			if err := json.NewDecoder(resp.Body).Decode(&out); err == nil && out.SessionID != "" && out.User.ID != "" {
				return &out, nil
			}
		}
	}

	if token := extractCookieValue(sessionCookie, "openksm_enduser_token"); token != "" && c.gatewayURL != "" && c.authProjectSlug != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.gatewayURL+"/auth/v1/"+c.authProjectSlug+"/user", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var eu struct {
				User struct {
					ID    string `json:"id"`
					Email string `json:"email"`
				} `json:"user"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&eu); err == nil && eu.User.ID != "" {
				var out SessionInfo
				out.SessionID = "enduser"
				out.User.ID = eu.User.ID
				out.User.Email = eu.User.Email
				out.User.DisplayName = eu.User.Email
				return &out, nil
			}
		}
	}

	return nil, fmt.Errorf("session not authenticated")
}

func extractCookieValue(cookieHeader, name string) string {
	for _, part := range strings.Split(cookieHeader, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && kv[0] == name {
			return kv[1]
		}
	}
	return ""
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

// LoginResult holds the raw JSON and cookies from IDNBase /v1/auth/login or /v1/auth/mfa/verify.
type LoginResult struct {
	StatusCode int
	Body       []byte
	Cookies    []*http.Cookie
}

// Login forwards credentials to IDNBase control-api, falling back to End-User Auth v2.
func (c *Client) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	bodyBytes, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/auth/login", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusUnauthorized || c.gatewayURL == "" || c.authProjectSlug == "" {
		return &LoginResult{StatusCode: resp.StatusCode, Body: raw, Cookies: resp.Cookies()}, nil
	}

	// Fallback to End-User Auth v2 (/auth/v1/:slug/token)
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gatewayURL+"/auth/v1/"+c.authProjectSlug+"/token", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := c.httpClient.Do(req2)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()
	raw2, _ := io.ReadAll(io.LimitReader(resp2.Body, 1<<20))
	if resp2.StatusCode == http.StatusOK {
		var tok struct {
			AccessToken string `json:"accessToken"`
		}
		if json.Unmarshal(raw2, &tok) == nil && tok.AccessToken != "" {
			return &LoginResult{
				StatusCode: http.StatusOK,
				Body:       []byte(`{"authenticated":true}`),
				Cookies: []*http.Cookie{{
					Name:     "openksm_enduser_token",
					Value:    tok.AccessToken,
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
					MaxAge:   3600,
				}},
			}, nil
		}
	}
	return &LoginResult{StatusCode: resp2.StatusCode, Body: raw2}, nil
}

// VerifyMFA forwards TOTP verification to IDNBase /v1/auth/mfa/verify.
func (c *Client) VerifyMFA(ctx context.Context, challengeToken, code string) (*LoginResult, error) {
	bodyBytes, _ := json.Marshal(map[string]string{"challengeToken": challengeToken, "code": code})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/auth/mfa/verify", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &LoginResult{StatusCode: resp.StatusCode, Body: raw, Cookies: resp.Cookies()}, nil
}

// RequestSignedURL requests a short-lived upload or download URL from IDNBase storage.
func (c *Client) RequestSignedURL(ctx context.Context, sessionCookie, csrfToken, projectID, bucketName, action, objectKey string) (string, error) {
	bodyBytes, _ := json.Marshal(map[string]string{"action": action, "objectKey": objectKey})
	u := fmt.Sprintf("%s/v1/projects/%s/storage/buckets/%s/signed-url", c.baseURL, projectID, bucketName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", sessionCookie)
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("signed-url failed (%d): %s", resp.StatusCode, string(raw))
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// UploadToSignedURL uploads bytes to an IDNBase presigned URL path.
func (c *Client) UploadToSignedURL(ctx context.Context, signedPath, contentType string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+signedPath, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("storage upload failed with status %d", resp.StatusCode)
	}
	return nil
}
