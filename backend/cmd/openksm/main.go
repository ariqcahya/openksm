package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openksm/openksm/backend/internal/httpapi"
	"github.com/openksm/openksm/backend/internal/org"
	"github.com/openksm/openksm/backend/internal/outbox"
	"github.com/openksm/openksm/backend/internal/platform/idnbase"
	"github.com/openksm/openksm/backend/internal/tenantdb"
	"github.com/openksm/openksm/backend/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool := initRegistryDB(ctx)
	if pool != nil {
		defer pool.Close()
	}

	if len(os.Args) > 1 && os.Args[1] == "worker" {
		runWorker(ctx, pool)
		return
	}
	runAPI(ctx, pool)
}

func initRegistryDB(ctx context.Context) *pgxpool.Pool {
	dsn := os.Getenv("OPENKSM_REGISTRY_DATABASE_URL")
	if dsn == "" {
		log.Println("OPENKSM_REGISTRY_DATABASE_URL not set; running without registry DB")
		return nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("registry db connect error: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("registry db ping error: %v", err)
	}
	for _, m := range []string{
		"registry/001_init_registry.sql",
		"tenant/001_init_tenant.sql",
		"tenant/002_m03_collections.sql",
	} {
		sqlBytes, err := migrations.FS.ReadFile(m)
		if err != nil {
			log.Fatalf("read migration %s: %v", m, err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			log.Fatalf("apply migration %s: %v", m, err)
		}
	}
	log.Println("registry database connected and migrated")
	return pool
}

func runWorker(ctx context.Context, pool *pgxpool.Pool) {
	log.Println("openksm-worker started (M02 outbox consumer)")
	if pool == nil {
		<-ctx.Done()
		return
	}
	w := outbox.NewWorker(pool, func(ctx context.Context, ev outbox.Event) error {
		var payload map[string]any
		_ = json.Unmarshal(ev.Payload, &payload)
		log.Printf("outbox processed event_id=%s type=%s aggregate=%s/%s", ev.ID, ev.EventType, ev.AggregateType, ev.AggregateID)
		return nil
	})
	w.Run(ctx)
	log.Println("openksm-worker shutting down")
}

func runAPI(ctx context.Context, pool *pgxpool.Pool) {
	addr := os.Getenv("OPENKSM_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	controlURL := os.Getenv("IDNBASE_CONTROL_URL")
	if controlURL == "" {
		controlURL = "http://control-api:3000"
	}
	gatewayURL := os.Getenv("IDNBASE_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://graphql-gateway:4000"
	}
	authProjectSlug := os.Getenv("OPENKSM_AUTH_PROJECT_SLUG")

	idnClient := idnbase.NewClient(controlURL, gatewayURL, authProjectSlug)
	var orgSvc *org.Service
	var tenantMgr *tenantdb.Manager
	if pool != nil {
		orgSvc = org.NewService(pool)
		tenantMgr = tenantdb.NewManager(pool, os.Getenv("OPENKSM_TENANT_DB_BASE_URL"))
		defer tenantMgr.Close()
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewRouter(pool, tenantMgr, idnClient, orgSvc),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("openksm-api listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}
