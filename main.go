package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Version is injected via APP_VERSION env (CI sets it to the image SHA).
// Defaults to "dev" so an un-injected binary never pretends to be a release.
var Version = getEnv("APP_VERSION", "dev")

// --- Metrics definitions ---

var (
	requestCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "guardrail_requests_total",
			Help: "Total number of HTTP requests, labeled by path and status",
		},
		[]string{"path", "status"},
	)

	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "guardrail_request_duration_seconds",
			Help:    "Request latency in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path"},
	)
)

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// statusRecorder captures the actual status code written by the handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// instrument wraps a handler to record real count + latency and recover panics.
// route must be the registered route pattern (low cardinality), NOT r.URL.Path.
func instrument(route string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic on %s %s: %v", r.Method, r.URL.Path, err)
				if rec.status == 0 {
					rec.WriteHeader(http.StatusInternalServerError)
				}
			}
			status := strconv.Itoa(rec.status)
			if rec.status == 0 {
				status = "200"
			}
			requestCount.WithLabelValues(route, status).Inc()
			requestDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
		}()

		handler(rec, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode response failed: %v", err)
	}
}

func requireGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return false
	}
	return true
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	// Exact-match: "/" previously acted as a catch-all and swallowed 404s
	// (polluting the "/" metric). Return 404 for anything else.
	if r.URL.Path != "/" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Hello from the Guardrail",
		"version": Version,
	})
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "healthy",
		"version": Version,
	})
}

// liveHandler is cheap: process is running. Used for liveness/startup probes.
// It must NOT check downstream dependencies.
func liveHandler(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

// readyHandler gates traffic. Add DB/dependency checks here in the future.
// Kept separate from /live so a failing dependency removes the pod from
// service instead of killing it.
func readyHandler(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ready",
		"version": Version,
	})
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", instrument("/", homeHandler))
	mux.HandleFunc("/health", instrument("/health", healthHandler))
	mux.HandleFunc("/live", instrument("/live", liveHandler))
	mux.HandleFunc("/ready", instrument("/ready", readyHandler))
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}

func main() {
	port := getEnv("PORT", "5000")

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           newMux(),
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("Server starting on :%s, version: %s", port, Version)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen failed: %v", err)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	log.Println("Server stopped")
}
