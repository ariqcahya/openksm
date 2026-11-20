package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openksm/openksm/backend/internal/collection"
	"github.com/openksm/openksm/backend/internal/finance"
	"github.com/openksm/openksm/backend/internal/iam"
	"github.com/openksm/openksm/backend/internal/org"
	"github.com/openksm/openksm/backend/internal/platform/idnbase"
	"github.com/openksm/openksm/backend/internal/tenantdb"
)

// SessionVerifier abstracts IDNBase session verification for testability.
type SessionVerifier interface {
	CheckHealth(ctx context.Context) (*idnbase.HealthStatus, error)
	VerifySession(ctx context.Context, sessionCookie string) (*idnbase.SessionInfo, error)
	ProvisionProject(ctx context.Context, sessionCookie, csrfToken, idempotencyKey string, payload idnbase.ProvisionProjectRequest) (*idnbase.ProvisionProjectResponse, error)
}

type ctxKey string

const userRefIDKey ctxKey = "user_ref_id"

// NewRouter builds the OpenKSM HTTP API router.
func NewRouter(pool *pgxpool.Pool, tenantMgr *tenantdb.Manager, verifier SessionVerifier, orgSvc *org.Service) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(15 * time.Second))

	r.Get("/health/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": "openksm-api",
		})
	})

	r.Get("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		if _, err := verifier.CheckHealth(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "idnbase control-api unreachable")
			return
		}
		if pool != nil {
			if err := pool.Ping(r.Context()); err != nil {
				writeError(w, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "registry database unreachable")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ready",
			"service": "openksm-api",
			"detail":  "idnbase control-api and registry db healthy",
		})
	})

	r.Get("/api/v1/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"name":    "OpenKSM",
			"version": "0.2.0-dev",
		})
	})

	getTenantCol := func(ctx context.Context, orgID string) (*collection.Service, error) {
		if tenantMgr != nil {
			tp, err := tenantMgr.PoolForOrg(ctx, orgID)
			if err != nil {
				return nil, err
			}
			return collection.NewService(tp), nil
		}
		return collection.NewService(pool), nil
	}

	getTenantFin := func(ctx context.Context, orgID string) (*finance.Service, error) {
		if tenantMgr != nil {
			tp, err := tenantMgr.PoolForOrg(ctx, orgID)
			if err != nil {
				return nil, err
			}
			return finance.NewService(tp), nil
		}
		return finance.NewService(pool), nil
	}

	r.Route("/api/v1/organizations", func(r chi.Router) {
		r.Use(authMiddleware(verifier, orgSvc))

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			userID := r.Context().Value(userRefIDKey).(string)
			items, err := orgSvc.ListForUser(r.Context(), userID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list organizations")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		})

		r.Post("/", func(w http.ResponseWriter, r *http.Request) {
			idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if len(idemKey) < 16 || len(idemKey) > 128 {
				writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key header (16-128 chars) is required")
				return
			}
			var in org.ApplyInput
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			userID := r.Context().Value(userRefIDKey).(string)
			created, err := orgSvc.Apply(r.Context(), userID, in)
			if errors.Is(err, org.ErrInvalidInput) {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			if err != nil {
				writeError(w, http.StatusConflict, "CONFLICT", "unable to create organization")
				return
			}
			writeJSON(w, http.StatusCreated, created)
		})

		r.Post("/{orgId}/decisions", func(w http.ResponseWriter, r *http.Request) {
			idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if len(idemKey) < 16 || len(idemKey) > 128 {
				writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key header (16-128 chars) is required")
				return
			}
			var body struct {
				Decision string `json:"decision"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			userID := r.Context().Value(userRefIDKey).(string)
			updated, err := orgSvc.Decide(r.Context(), orgID, userID, body.Decision)
			switch {
			case errors.Is(err, org.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "organization not found")
			case errors.Is(err, iam.ErrSelfApproval):
				writeError(w, http.StatusForbidden, "MAKER_CHECKER_VIOLATION", err.Error())
			case errors.Is(err, org.ErrInvalidTransition):
				writeError(w, http.StatusUnprocessableEntity, "INVALID_TRANSITION", err.Error())
			case errors.Is(err, org.ErrInvalidInput):
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
			case err != nil:
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "decision failed")
			default:
				writeJSON(w, http.StatusOK, updated)
			}
		})

		r.Post("/{orgId}/units", requireOrgPerm(orgSvc, iam.PermUnitManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Name     string `json:"name"`
				UnitType string `json:"unit_type"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			userID := r.Context().Value(userRefIDKey).(string)
			unit, err := orgSvc.CreateUnit(r.Context(), orgID, userID, body.Name, body.UnitType)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, unit)
		}))

		r.Post("/{orgId}/service-areas", requireOrgPerm(orgSvc, iam.PermAreaManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Name       string `json:"name"`
				RegionCode string `json:"region_code"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			userID := r.Context().Value(userRefIDKey).(string)
			sa, err := orgSvc.CreateServiceArea(r.Context(), orgID, userID, body.Name, body.RegionCode)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, sa)
		}))

		r.Delete("/{orgId}/memberships/{membershipId}", requireOrgPerm(orgSvc, iam.PermMembershipRev, func(w http.ResponseWriter, r *http.Request) {
			orgID := chi.URLParam(r, "orgId")
			membershipID := chi.URLParam(r, "membershipId")
			userID := r.Context().Value(userRefIDKey).(string)
			err := orgSvc.RevokeMembership(r.Context(), orgID, userID, membershipID)
			if errors.Is(err, org.ErrNotFound) {
				writeError(w, http.StatusNotFound, "NOT_FOUND", "membership not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to revoke membership")
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
		}))

		r.Post("/{orgId}/provision", func(w http.ResponseWriter, r *http.Request) {
			idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if len(idemKey) < 16 || len(idemKey) > 128 {
				writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key header (16-128 chars) is required")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			userID := r.Context().Value(userRefIDKey).(string)
			cookie := r.Header.Get("Cookie")
			csrf := r.Header.Get("X-CSRF-Token")
			updated, err := orgSvc.Provision(r.Context(), orgID, userID, idemKey, cookie, csrf, verifier)
			switch {
			case errors.Is(err, org.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "organization not found")
			case errors.Is(err, org.ErrInvalidTransition):
				writeError(w, http.StatusUnprocessableEntity, "INVALID_TRANSITION", err.Error())
			case err != nil:
				writeError(w, http.StatusBadGateway, "PROVISION_FAILED", err.Error())
			default:
				writeJSON(w, http.StatusOK, updated)
			}
		})

		r.Get("/{orgId}/memberships", requireOrgPerm(orgSvc, iam.PermMembershipInv, func(w http.ResponseWriter, r *http.Request) {
			orgID := chi.URLParam(r, "orgId")
			items, err := orgSvc.ListMemberships(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list memberships")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		}))

		r.Post("/{orgId}/memberships", requireOrgPerm(orgSvc, iam.PermMembershipInv, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Email         string `json:"email"`
				RoleCode      string `json:"role_code"`
				ServiceAreaID string `json:"service_area_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			userID := r.Context().Value(userRefIDKey).(string)
			m, err := orgSvc.InviteMembership(r.Context(), orgID, userID, body.Email, body.RoleCode, body.ServiceAreaID)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, m)
		}))

		// --- M03: Customers, Service Points, Collections & Offline Sync ---
		r.Get("/{orgId}/customers", requireOrgPerm(orgSvc, iam.PermCustomerManage, func(w http.ResponseWriter, r *http.Request) {
			orgID := chi.URLParam(r, "orgId")
			colSvc, err := getTenantCol(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			items, err := colSvc.ListCustomers(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list customers")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		}))

		r.Post("/{orgId}/customers", requireOrgPerm(orgSvc, iam.PermCustomerManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				DisplayName string `json:"display_name"`
				PhoneMasked string `json:"phone_masked"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			colSvc, err := getTenantCol(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			c, err := colSvc.CreateCustomer(r.Context(), body.DisplayName, body.PhoneMasked)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, c)
		}))

		r.Get("/{orgId}/service-points", requireOrgPerm(orgSvc, iam.PermCustomerManage, func(w http.ResponseWriter, r *http.Request) {
			orgID := chi.URLParam(r, "orgId")
			colSvc, err := getTenantCol(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			items, err := colSvc.ListServicePoints(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list service points")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		}))

		r.Post("/{orgId}/service-points", requireOrgPerm(orgSvc, iam.PermCustomerManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				CustomerID    string `json:"customer_id"`
				Label         string `json:"label"`
				AddressText   string `json:"address_text"`
				ServiceAreaID string `json:"service_area_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			if body.ServiceAreaID != "" {
				ok, err := orgSvc.ServiceAreaExists(r.Context(), orgID, body.ServiceAreaID)
				if err != nil {
					writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid service_area_id")
					return
				}
				if !ok {
					writeError(w, http.StatusUnprocessableEntity, "OUTSIDE_SERVICE_AREA", "service_area_id is outside organization authorized service areas")
					return
				}
			}
			colSvc, err := getTenantCol(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			sp, err := colSvc.CreateServicePoint(r.Context(), body.CustomerID, body.Label, body.AddressText, body.ServiceAreaID)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, sp)
		}))

		r.Get("/{orgId}/collections/jobs", requireOrgPerm(orgSvc, iam.PermCollectionExecute, func(w http.ResponseWriter, r *http.Request) {
			orgID := chi.URLParam(r, "orgId")
			colSvc, err := getTenantCol(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			userID := r.Context().Value(userRefIDKey).(string)
			jobs, err := colSvc.ListAssignedJobs(r.Context(), userID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list jobs")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": jobs})
		}))

		r.Post("/{orgId}/collections/jobs", requireOrgPerm(orgSvc, iam.PermCollectionAssign, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Title           string    `json:"title"`
				AssignedUserRef string    `json:"assigned_user_ref"`
				ServiceAreaID   string    `json:"service_area_id"`
				ScheduledAt     time.Time `json:"scheduled_at"`
				StopPointIDs    []string  `json:"stop_point_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			colSvc, err := getTenantCol(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			userID := r.Context().Value(userRefIDKey).(string)
			job, err := colSvc.CreateJob(r.Context(), userID, body.Title, body.AssignedUserRef, body.ServiceAreaID, body.ScheduledAt, body.StopPointIDs)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, job)
		}))

		r.Post("/{orgId}/collections/sync", requireOrgPerm(orgSvc, iam.PermCollectionExecute, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Events []collection.OfflinePickupEvent `json:"events"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			colSvc, err := getTenantCol(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			userID := r.Context().Value(userRefIDKey).(string)
			results, err := colSvc.SyncPickupEvents(r.Context(), userID, body.Events)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "SYNC_ERROR", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"results": results})
		}))

		r.Get("/{orgId}/tasks", requireOrgPerm(orgSvc, iam.PermTaskRead, func(w http.ResponseWriter, r *http.Request) {
			orgID := chi.URLParam(r, "orgId")
			colSvc, err := getTenantCol(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			userID := r.Context().Value(userRefIDKey).(string)
			tasks, err := colSvc.ListTasks(r.Context(), userID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list tasks")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": tasks})
		}))

		// --- M04: Tariffs, Subscriptions, Unique Invoices, Payments (Maker-Checker) & Ledger ---
		r.Post("/{orgId}/tariffs", requireOrgPerm(orgSvc, iam.PermBillingManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Label         string `json:"label"`
				AmountIDR     int64  `json:"amount_idr"`
				EffectiveFrom string `json:"effective_from"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			finSvc, err := getTenantFin(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			t, err := finSvc.CreateTariff(r.Context(), body.Label, body.AmountIDR, body.EffectiveFrom)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, t)
		}))

		r.Post("/{orgId}/subscriptions", requireOrgPerm(orgSvc, iam.PermBillingManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				ServicePointID string `json:"service_point_id"`
				TariffID       string `json:"tariff_id"`
				StartedAt      string `json:"started_at"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			finSvc, err := getTenantFin(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			sub, err := finSvc.CreateSubscription(r.Context(), body.ServicePointID, body.TariffID, body.StartedAt)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, sub)
		}))

		r.Get("/{orgId}/invoices", requireOrgPerm(orgSvc, iam.PermFinanceRead, func(w http.ResponseWriter, r *http.Request) {
			orgID := chi.URLParam(r, "orgId")
			finSvc, err := getTenantFin(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			items, err := finSvc.ListInvoices(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list invoices")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		}))

		r.Post("/{orgId}/invoices", requireOrgPerm(orgSvc, iam.PermBillingManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				SubscriptionID string `json:"subscription_id"`
				BillingPeriod  string `json:"billing_period"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			finSvc, err := getTenantFin(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			userID := r.Context().Value(userRefIDKey).(string)
			inv, err := finSvc.IssueInvoice(r.Context(), userID, body.SubscriptionID, body.BillingPeriod)
			switch {
			case errors.Is(err, finance.ErrDuplicateInvoice):
				writeError(w, http.StatusConflict, "DUPLICATE_INVOICE", err.Error())
			case errors.Is(err, finance.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "active subscription not found")
			case err != nil:
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
			default:
				writeJSON(w, http.StatusCreated, inv)
			}
		}))

		r.Post("/{orgId}/payments", requireOrgPerm(orgSvc, iam.PermPaymentRecord, func(w http.ResponseWriter, r *http.Request) {
			idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if len(idemKey) < 16 || len(idemKey) > 128 {
				writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key header (16-128 chars) is required")
				return
			}
			var body struct {
				InvoiceID string `json:"invoice_id"`
				AmountIDR int64  `json:"amount_idr"`
				Method    string `json:"method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			finSvc, err := getTenantFin(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			userID := r.Context().Value(userRefIDKey).(string)
			p, err := finSvc.RecordPayment(r.Context(), userID, body.InvoiceID, idemKey, body.Method, body.AmountIDR)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, p)
		}))

		r.Post("/{orgId}/payments/{paymentId}/verify", requireOrgPerm(orgSvc, iam.PermPaymentVerify, func(w http.ResponseWriter, r *http.Request) {
			orgID := chi.URLParam(r, "orgId")
			paymentID := chi.URLParam(r, "paymentId")
			finSvc, err := getTenantFin(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			userID := r.Context().Value(userRefIDKey).(string)
			p, err := finSvc.VerifyAndPostPayment(r.Context(), userID, paymentID)
			switch {
			case errors.Is(err, finance.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "payment not found")
			case errors.Is(err, iam.ErrSelfApproval):
				writeError(w, http.StatusForbidden, "MAKER_CHECKER_VIOLATION", err.Error())
			case errors.Is(err, finance.ErrInvalidTransition):
				writeError(w, http.StatusUnprocessableEntity, "INVALID_TRANSITION", err.Error())
			case err != nil:
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			default:
				writeJSON(w, http.StatusOK, p)
			}
		}))

		r.Post("/{orgId}/payments/{paymentId}/reverse", requireOrgPerm(orgSvc, iam.PermPaymentReverse, func(w http.ResponseWriter, r *http.Request) {
			idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if len(idemKey) < 16 || len(idemKey) > 128 {
				writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key header (16-128 chars) is required")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			paymentID := chi.URLParam(r, "paymentId")
			finSvc, err := getTenantFin(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			userID := r.Context().Value(userRefIDKey).(string)
			p, err := finSvc.ReversePayment(r.Context(), userID, paymentID)
			switch {
			case errors.Is(err, finance.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "payment not found")
			case errors.Is(err, finance.ErrInvalidTransition):
				writeError(w, http.StatusUnprocessableEntity, "INVALID_TRANSITION", err.Error())
			case err != nil:
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			default:
				writeJSON(w, http.StatusOK, p)
			}
		}))

		r.Get("/{orgId}/ledger/summary", requireOrgPerm(orgSvc, iam.PermFinanceRead, func(w http.ResponseWriter, r *http.Request) {
			orgID := chi.URLParam(r, "orgId")
			finSvc, err := getTenantFin(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			sum, err := finSvc.GetLedgerSummary(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to query ledger summary")
				return
			}
			writeJSON(w, http.StatusOK, sum)
		}))

		r.Post("/{orgId}/adjustments", requireOrgPerm(orgSvc, iam.PermBillingManage, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				InvoiceID      string `json:"invoice_id"`
				AmountDeltaIDR int64  `json:"amount_delta_idr"`
				Reason         string `json:"reason"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			finSvc, err := getTenantFin(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			userID := r.Context().Value(userRefIDKey).(string)
			adj, err := finSvc.RequestAdjustment(r.Context(), userID, body.InvoiceID, body.Reason, body.AmountDeltaIDR)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, adj)
		}))

		r.Post("/{orgId}/adjustments/{adjId}/decide", requireOrgPerm(orgSvc, iam.PermPaymentVerify, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Decision string `json:"decision"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_JSON", "malformed request body")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			adjID := chi.URLParam(r, "adjId")
			finSvc, err := getTenantFin(r.Context(), orgID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TENANT_DB_UNAVAILABLE", err.Error())
				return
			}
			userID := r.Context().Value(userRefIDKey).(string)
			adj, err := finSvc.DecideAdjustment(r.Context(), userID, adjID, body.Decision)
			switch {
			case errors.Is(err, finance.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "adjustment request not found")
			case errors.Is(err, iam.ErrSelfApproval):
				writeError(w, http.StatusForbidden, "MAKER_CHECKER_VIOLATION", err.Error())
			case errors.Is(err, finance.ErrInvalidTransition):
				writeError(w, http.StatusUnprocessableEntity, "INVALID_TRANSITION", err.Error())
			case err != nil:
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
			default:
				writeJSON(w, http.StatusOK, adj)
			}
		}))
	})

	return r
}

func authMiddleware(verifier SessionVerifier, orgSvc *org.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookieHeader := r.Header.Get("Cookie")
			if cookieHeader == "" {
				writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing session credentials")
				return
			}
			sess, err := verifier.VerifySession(r.Context(), cookieHeader)
			if err != nil || sess.User.ID == "" {
				writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid or expired session")
				return
			}
			userRefID, err := orgSvc.EnsureUserRef(r.Context(), "idnbase", sess.User.ID, sess.User.Email)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to resolve user identity")
				return
			}
			ctx := context.WithValue(r.Context(), userRefIDKey, userRefID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func requireOrgPerm(orgSvc *org.Service, perm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := chi.URLParam(r, "orgId")
		userID := r.Context().Value(userRefIDKey).(string)
		mc, err := orgSvc.GetMembershipContext(r.Context(), orgID, userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to check membership")
			return
		}
		if err := iam.Authorize(mc, orgID, "", perm, time.Now().UTC()); err != nil {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "permission denied for this organization")
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{
		"code":    code,
		"message": msg,
	})
}
