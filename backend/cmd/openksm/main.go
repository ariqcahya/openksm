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
)

type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Detail  string `json:"detail,omitempty"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "worker" {
		runWorker()
		return
	}
	runAPI()
}

func runWorker() {
	log.Println("openksm-worker started (M01 scaffold)")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Println("openksm-worker shutting down")
}

func runAPI() {
	addr := os.Getenv("OPENKSM_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	controlURL := os.Getenv("IDNBASE_CONTROL_URL")
	if controlURL == "" {
		controlURL = "http://control-api:3000"
	}

	client := &http.Client{Timeout: 3 * time.Second}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{
			Status:  "ok",
			Service: "openksm-api",
		})
	})

	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, controlURL+"/healthz", nil)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, healthResponse{
				Status:  "unavailable",
				Service: "openksm-api",
				Detail:  "invalid idnbase control url",
			})
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, healthResponse{
				Status:  "unavailable",
				Service: "openksm-api",
				Detail:  "idnbase control-api unreachable",
			})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			writeJSON(w, http.StatusServiceUnavailable, healthResponse{
				Status:  "unavailable",
				Service: "openksm-api",
				Detail:  "idnbase control-api not healthy",
			})
			return
		}

		writeJSON(w, http.StatusOK, healthResponse{
			Status:  "ready",
			Service: "openksm-api",
			Detail:  "idnbase control-api healthy",
		})
	})

	mux.HandleFunc("GET /api/v1/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"name":    "OpenKSM",
			"version": "0.1.0-dev",
		})
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("openksm-api listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
