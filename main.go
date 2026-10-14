// Guardrail - small health-check service with Prometheus metrics.
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

// Set by CI (image SHA). "dev" when running locally.
var Version = getEnv("APP_VERSION", "dev")

var (
	// How many requests we got, split by route and status code.
	// We use the route pattern ("/health") not the real URL to keep metrics small.
	requestCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "guardrail_requests_total",
			Help: "Total number of HTTP requests, labeled by path and status",
		},
		[]string{"path", "status"},
	)

	// How slow each route is.
	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "guardrail_request_duration_seconds",
			Help:    "Request latency in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path"},
	)
)

// Read env var or use fallback if not set.
func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// http doesn't give us the status code back, so we wrap the writer to remember it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	// If handler just calls Write() without WriteHeader(), it means 200.
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Wrap a handler to count requests, time them, and catch panics.
// Pass the route pattern like "/health" - not r.URL.Path, or metrics will blow up.
func instrument(route string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		defer func() {
			// One bad request shouldn't kill the whole server.
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

// Small helper to send JSON + status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode response failed: %v", err)
	}
}

// We only support GET on these endpoints. Returns false if it already sent the 405.
func requireGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return false
	}
	return true
}

// GET / - just says hi + version.
func homeHandler(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	// NOTE: "/" in Go matches everything, so check the path by hand.
	// Otherwise /foo would look like a hit on "/" in our metrics.
	if r.URL.Path != "/" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Hello from the Guardrail",
		"version": Version,
	})
}

// GET /health - for load balancers / uptime checks.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "healthy",
		"version": Version,
	})
}

// GET /live - k8s liveness probe. Just "am I running?"
// Don't check DB or anything here.
func liveHandler(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

// GET /ready - k8s readiness probe. "Can I take traffic?"
// If we add a DB later, check it here. Keep separate from /live so
// a bad DB takes us out of service instead of restarting us.
func readyHandler(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ready",
		"version": Version,
	})
}

// Set up all routes.
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

	// Wait for Ctrl-C / docker stop, then give running requests 10s to finish.
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
