package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openksm/openksm/backend/internal/platform/idnbase"
)

type stubVerifier struct {
	healthy bool
	userID  string
}

func (s *stubVerifier) CheckHealth(ctx context.Context) (*idnbase.HealthStatus, error) {
	if !s.healthy {
		return nil, context.DeadlineExceeded
	}
	return &idnbase.HealthStatus{Status: "ok", Service: "idnbase-control-api", Version: "1.2.1"}, nil
}

func (s *stubVerifier) VerifySession(ctx context.Context, sessionCookie string) (*idnbase.SessionInfo, error) {
	if !strings.Contains(sessionCookie, "valid_session") {
		return nil, context.Canceled
	}
	var out idnbase.SessionInfo
	out.SessionID = "sess-1"
	out.User.ID = s.userID
	out.User.Email = "tester@openksm.local"
	return &out, nil
}

func (s *stubVerifier) ProvisionProject(ctx context.Context, sessionCookie, csrfToken, idempotencyKey string, payload idnbase.ProvisionProjectRequest) (*idnbase.ProvisionProjectResponse, error) {
	return &idnbase.ProvisionProjectResponse{
		JobID:     "job-1",
		ProjectID: "00000000-0000-0000-0000-000000000001",
		State:     "QUEUED",
	}, nil
}

func TestHTTPAPI_HealthAndAuthGuards(t *testing.T) {
	router := NewRouter(nil, nil, &stubVerifier{healthy: true, userID: "user-1"}, nil)

	// 1. /health/live -> 200
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /health/live, got %d", rec.Code)
	}

	// 2. Unauthenticated /api/v1/organizations -> 401
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/organizations", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauthenticated request, got %d", rec2.Code)
	}

	// 3. Unauthenticated M03 /customers & /collections/jobs -> 401
	for _, path := range []string{
		"/api/v1/organizations/org-1/customers",
		"/api/v1/organizations/org-1/service-points",
		"/api/v1/organizations/org-1/collections/jobs",
		"/api/v1/organizations/org-1/tasks",
	} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 on %s, got %d", path, w.Code)
		}
	}
}
